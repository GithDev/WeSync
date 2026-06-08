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
// just decide WHEN a session opens (AlarmManager → OpenSyncSession), while
// on_change keeps the SERVICE resident (to host the file watcher) but lets
// ST itself sleep between sessions — WeSync's own watcher opens a session
// when a change settles, then ST wakes, syncs, and sleeps again.
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
// Not time-capped — a large transfer may outlast any fixed ceiling. A stall
// guard closes a peer-pull keepalive that moves no bytes (stuck transfer /
// wedged REST) instead. When the session closes, desiredRunning goes false and
// the loop stops ST.
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
	// A sync session is deliberately NOT time-capped: it stays open as long as
	// real work is happening — our own folder syncing/scanning, OR a connected
	// peer still pulling data from us (a 4 GB download to a client can outlast any
	// fixed cap). What bounds it instead is the stall guard: a peer-pull keepalive
	// that moves no bytes for a while (a stuck transfer, or a wedged ST REST that
	// reports "busy" on error) lets the session lapse so ST can sleep.
	//
	// stallPollLimit: consecutive polls with no transferred-byte progress before
	// a peer-pull keepalive counts as stalled.
	stallPollLimit = 3
	// stallFloorBytes: minimum transferred-byte delta between polls to count as
	// progress — above ST's idle keepalive/index chatter, far below any real
	// transfer (which moves megabytes per poll).
	stallFloorBytes = 64 * 1024
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
	triggerOnChange     = "on_change"
	triggerOnChangePoll = "on_change_poll"
)

// PowerHost lets the gate push run-state up to the platform wrapper so it
// can hold/release the radio (MulticastLock) and CPU (partial WakeLock)
// for exactly as long as ST needs to run. One source of truth: the gate
// decides, the host reacts. Implemented in Kotlin by WeSyncService.
type PowerHost interface {
	OnSyncActive(active bool)
	// OnWatcherActive is called when the on_change file watcher starts or
	// stops. The host must hold a PARTIAL_WAKE_LOCK for exactly as long as
	// the watcher is active — without it the Go runtime freezes under doze
	// and inotify events are never delivered to the consume() goroutine.
	// This lock is independent of OnSyncActive: it is held even while ST
	// sleeps between syncs, which is the whole point of on_change mode.
	OnWatcherActive(active bool)
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
	charging      bool
	metered       bool
	roaming       bool
	activeWifi    bool

	// The only genuinely stored state: when the current sync session
	// ends. Zero value means no session is open. sessionStartedAt marks when the
	// session opened — used for the connect-grace window (no fixed session cap).
	sessionStartedAt time.Time
	sessionEndsAt    time.Time

	// Stall guard for the keepalive: stallPolls counts consecutive polls with no
	// transferred-byte progress while a session is open; lastTransferBytes is the
	// cumulative in+out byte total at the previous poll. When stallPolls reaches
	// stallPollLimit a peer-pull keepalive is treated as stalled and the session
	// is allowed to lapse. Both reset when a session opens / ST stops.
	stallPolls        int
	lastTransferBytes int64

	// foregroundUntil keeps ST alive for a short grace after the app loses
	// foreground, so a transient background (folder picker, permission dialog,
	// settings page) doesn't tear ST down and make the next API call hit a
	// stopped Syncthing. Set on appForeground→false, cleared on →true. Zero
	// value means no grace pending.
	foregroundUntil time.Time

	// dirty: we have local changes not yet confirmed pushed to every peer. The
	// on_change file watcher SETS it (best-effort, while ST sleeps); the reconcile
	// loop RECONCILES it to ST's authoritative completion once ST is awake and
	// idle. It drives the on_change backstop tick — an all-sendonly device with
	// dirty==false has nothing to push and nothing to receive, so the tick lets
	// it keep sleeping. In-memory only: every cold start re-derives the truth via
	// a catch-up session, so there's nothing to persist or trust across restarts.
	dirty bool
	// dirtyGen counts watcher-observed changes. reconcileDirty snapshots it before
	// its off-lock probe of ST and only clears dirty if it's unchanged afterward —
	// so a file change that lands mid-probe isn't clobbered by a stale "all caught
	// up" result (the lost-update the dirty flag must not have).
	dirtyGen uint64

	// Reconcile machinery. kick is a buffered "something changed, re-
	// evaluate" signal; the loop owns all start/stop of ST.
	kick chan struct{}

	host              PowerHost
	lastSyncActive    bool // dedupe host notifications
	lastWatcherActive bool // dedupe watcher-lock notifications
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
	// Cold-start catch-up: in on_change, open one session so ST scans + syncs
	// everything that changed while we were down. This is what lets us NOT trust
	// the file watcher across process death — every restart fully reconciles
	// against ST, and the catch-up's completion check sets/clears dirty from the
	// real state. (refreshSettingsFromDB above already (re)started the watcher.)
	g.mu.Lock()
	trigger := g.settings.SyncTrigger
	g.mu.Unlock()
	switch trigger {
	case triggerOnChange, triggerOnChangePoll:
		// on_change: watcher can't be trusted across process death — scan everything.
		// on_change_poll: pollCheckChanged returns true on nil snapshot (cold start),
		// but waiting for the next alarm could leave a gap of up to PeriodicMinutes.
		// Open a session immediately so restarts don't silently delay sync.
		g.emitEvent("trigger", "cold-start catch-up sync")
		OpenSyncSession()
	}
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
	g.stallPolls = 0
	g.lastTransferBytes = 0
	g.mu.Unlock()
	stopWatcher()        // tear down the on_change file watcher + its inotify handles (also calls notifyWatcherHost(false))
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
