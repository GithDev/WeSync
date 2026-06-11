package mobile

import (
	"log"
	"sync"
	"time"

	"wesync/internal/stmanager"
	"wesync/internal/store"
)

// gate owns the single decision "should the bundled Syncthing process be
// running right now?" for the Android wrapper. It controls ST's process
// lifecycle ONLY — starting and stopping the subprocess. It never touches
// per-folder pause state: that's owned exclusively by the user via the WS
// UI. (Earlier versions paused folders from here, which caused a steady
// stream of state-desync bugs because two things owned the same flag.)
//
// Design
// ──────
//
// There is exactly ONE computed property — desiredRunning() — and exactly
// ONE place that acts on it — reconcileLoop(). Every input change just
// updates a field and kicks the loop; the loop recomputes the whole
// decision from scratch every time. No incremental "last applied" flags,
// no cached process state. The truth of "is ST running?" is read straight
// from stmanager; the only genuinely stored state is sessionEndsAt.
//
//	desiredRunning =
//	    appForeground                                   // UI needs the API
//	    || (session open && networkAllowed && battery)  // a trigger asked us to sync
//
// The trigger modes mostly don't appear in that decision: periodic/scheduled
// and on_change_poll all decide WHEN a session opens (AlarmManager /
// OnNetworkState → OpenSyncSession); ST then wakes, syncs, and sleeps.
//
// Session lifecycle
// ─────────────────
//
// OpenSyncSession sets sessionEndsAt = now + connectGrace. While the
// session is open the loop polls ST and extends the deadline:
//   - ST busy (scanning/syncing)            → extend (let the sync finish)
//   - a connected peer still needs our data → extend (let it finish pulling)
//   - ST idle but no peer connected yet     → hold open (waiting to connect)
//   - ST idle + connected + nobody behind   → let the deadline lapse → close
// Not time-capped and never interrupted — once ST is awake a sync always runs
// to completion; a large transfer may outlast any fixed ceiling. When the
// session closes on its own (ST idle, nobody behind), desiredRunning goes false
// and the loop stops ST.
//
// Code layout (this package)
// ──────────────────────────
//   - gate.go            — the gate type, its lifecycle (init/markStopped),
//                           reconcile trigger plumbing, and the event log.
//   - gate_decision.go   — the PURE snapshot + desiredRunning logic (testable).
//   - gate_reconcile.go  — the loop that ACTS on the decision (start/stop ST).
//   - gate_settings.go   — DB reads + pushing settings into ST (fsWatcher).
//   - events.go          — the input entry points called by the Android wrapper.

const (
	// How long to keep a freshly-opened session alive before ST has shown
	// any activity. Must cover the full cold connect path, which is now the
	// INTERNET path when connectivity is level 2-3 (global discovery + relay:
	// announce → remote lookup → relay handshake → BEP) — far slower than LAN
	// discovery. At the old LAN-sized 60s a background trigger could lapse
	// before a relayed peer connected, so ST slept without syncing. Active sync
	// is NOT capped by this: once ST is busy, nextSessionEnd extends in
	// activeSyncExtend chunks the session forward; there is no fixed ceiling
	// (the stall guard below bounds a no-progress session instead).
	connectGrace = 120 * time.Second
	// Once ST reports active work, extend the session in chunks of this
	// size so a long sync keeps us alive without us predicting its length.
	activeSyncExtend = 5 * time.Minute
	// A sync session is deliberately NOT time-capped and is never interrupted: it
	// stays open as long as real work is happening — our own folder
	// syncing/scanning, OR a connected peer still pulling data from us (a 4 GB
	// download to a client can outlast any fixed cap). It lapses only when ST is
	// genuinely idle with nobody behind. There is intentionally no stall guard:
	// once ST is awake we let the sync finish on its own.
	// How often the loop polls ST for activity while a session is open.
	syncPollInterval = 15 * time.Second
	// How long ST stays up after the app loses foreground. Covers a transient
	// background — the SAF folder picker, a permission dialog, the system
	// settings page — so ST isn't torn down only to be needed again seconds
	// later (e.g. the folder-create API call right after the picker returns).
	// Generous enough to outlast picker navigation; the service's own 5-min
	// shutdown grace keeps the process around regardless, so the only cost
	// here is ST itself running a minute longer after a real backgrounding.
	foregroundGrace = 60 * time.Second
)

// SyncTrigger values — mirror store.PowerSettings.SyncTrigger.
const (
	triggerPeriodic     = "periodic"
	triggerScheduled    = "scheduled"
	triggerOnChangePoll = "on_change_poll"
)

// PowerHost lets the gate push run-state up to the platform wrapper so it
// can hold/release the radio (MulticastLock) and CPU (partial WakeLock)
// for exactly as long as ST needs to run. One source of truth: the gate
// decides, the host reacts. Implemented in Kotlin by WeSyncService.
type PowerHost interface {
	OnSyncActive(active bool)
}

type gate struct {
	mu sync.Mutex

	stExePath string
	store     *store.Store
	settings  store.PowerSettings

	// Observed inputs from the Android wrapper.
	appForeground bool
	currentSSID   string
	hasWifi       bool
	hasMobile     bool
	batteryLow    bool
	metered       bool
	roaming       bool
	activeWifi    bool

	// The only genuinely stored state: when the current sync session
	// ends. Zero value means no session is open. sessionStartedAt marks when the
	// session opened — used for the connect-grace window (no fixed session cap).
	sessionStartedAt time.Time
	sessionEndsAt    time.Time

	// foregroundUntil keeps ST alive for a short grace after the app loses
	// foreground, so a transient background (folder picker, permission dialog,
	// settings page) doesn't tear ST down and make the next API call hit a
	// stopped Syncthing. Set on appForeground→false, cleared on →true. Zero
	// value means no grace pending.
	foregroundUntil time.Time

	// Reconcile machinery. kick is a buffered "something changed, re-
	// evaluate" signal; the loop owns all start/stop of ST.
	kick chan struct{}

	host           PowerHost
	lastSyncActive bool // dedupe host notifications
}

var g = &gate{}

// initGate is called once from mobile.Start after the backend goroutine
// has launched ST + opened the DB. It does NOT reset g.appForeground —
// MainActivity.onResume may have called OnAppForeground(true) before
// Mobile.start returned, and clobbering it would falsely stop ST.
func initGate(stExePath string) {
	dbPath := stmanager.DataDir() + "/wesync.db"
	s, err := store.Open(dbPath)
	if err != nil {
		log.Printf("gate: open store: %v", err)
	}
	g.mu.Lock()
	g.stExePath = stExePath
	g.store = s
	if g.kick == nil {
		g.kick = make(chan struct{}, 1)
		go g.reconcileLoop()
	}
	g.mu.Unlock()
	if err := refreshSettingsFromDB(); err != nil {
		log.Printf("gate: initial settings load: %v", err)
	}
	g.emitEvent("start", "gate initialized; ST running")
	// mobile.Start has already started ST; reconcile so the loop's view
	// of the world matches reality (and stops ST again if no session is
	// open and we're in the background).
	g.requestReconcile()
	go forceUnpauseAllFoldersOnce()
}

// markStopped is called from Mobile.Stop when the whole backend is being
// torn down. It clears the gate's authority to (re)start ST — stExePath
// empty makes reconcileOnce a no-op — and closes any open session, so a
// late kick can't resurrect ST after we've deliberately shut it down. The
// reconcile loop goroutine is left running (harmless with an empty path)
// and reused by the next initGate. This guarantee is local: it no longer
// rests on the Android service's "never stop while foreground" invariant.
func (g *gate) markStopped() {
	g.mu.Lock()
	g.stExePath = ""
	g.sessionStartedAt = time.Time{}
	g.sessionEndsAt = time.Time{}
	g.foregroundUntil = time.Time{}
	g.mu.Unlock()
	g.requestReconcile() // let the loop stop its poll ticker
	g.notifyHost(false)  // release the radio/CPU locks on the host
}

// requestReconcile pokes the loop without blocking. The buffered channel
// collapses bursts of input changes into a single re-evaluation.
func (g *gate) requestReconcile() {
	if g.kick == nil {
		return
	}
	select {
	case g.kick <- struct{}{}:
	default:
	}
}

func (g *gate) emitEvent(kind, msg string) {
	g.mu.Lock()
	s := g.store
	g.mu.Unlock()
	if s == nil {
		return
	}
	if err := s.AppendPowerEvent(kind, msg, 0); err != nil {
		log.Printf("gate: emit event: %v", err)
	}
}
