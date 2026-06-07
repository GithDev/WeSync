package mobile

import (
	"encoding/json"
	"log"
	"time"

	"wesync/internal/stmanager"
)

// Event entry points from the Android wrapper. Each one updates a single
// observed field under the lock and kicks the reconcile loop — it never
// blocks the caller on ST start/stop (that happens on the loop goroutine).
//
// All of these are no-ops on desktop; gomobile-bind generates Java/Kotlin
// stubs for them but no platform code ever calls them outside Android.

// OnAppForeground reports whether the WeSync UI activity is currently
// visible. While foregrounded the gate forces ST to be running so the
// WebView's API calls don't fail, regardless of network/battery gates.
func OnAppForeground(fg bool) {
	g.mu.Lock()
	if g.appForeground == fg {
		g.mu.Unlock()
		return
	}
	g.appForeground = fg
	if fg {
		// Truly foreground now — the grace is moot.
		g.foregroundUntil = time.Time{}
	} else {
		// Lost foreground. Keep ST up for a short grace so a transient
		// background (SAF folder picker, permission dialog, settings page)
		// doesn't stop ST and make the next API call — e.g. the folder-create
		// right after the picker returns — hit a stopped Syncthing.
		g.foregroundUntil = time.Now().Add(foregroundGrace)
	}
	g.mu.Unlock()
	// Diagnostic: make the foreground transition visible in Recent activity so
	// "ST won't sleep when backgrounded" can be traced (a missing "background"
	// line means onPause never reached the gate).
	if fg {
		g.emitEvent("foreground", "app foreground — ST forced on")
	} else {
		g.emitEvent("foreground", "app background — ST may stop after grace")
	}
	g.requestReconcile()
}

// OnNetworkState reports the current connectivity:
//   - ssid: empty if not on WiFi, else the SSID string
//   - hasWifi: true if any WiFi interface is connected
//   - hasMobile: true if mobile data is connected
//   - metered: the ACTIVE network is metered (Android marks ALL cellular this
//     way, plus any WiFi the user/hotspot marked metered)
//   - roaming: the active cellular link is on a foreign carrier
//   - activeWifi: the active (default) network's transport is WiFi — lets the
//     gate tell a metered WiFi hotspot apart from ordinary metered cellular
func OnNetworkState(ssid string, hasWifi bool, hasMobile bool, metered bool, roaming bool, activeWifi bool) {
	g.mu.Lock()
	if g.currentSSID == ssid && g.hasWifi == hasWifi && g.hasMobile == hasMobile &&
		g.metered == metered && g.roaming == roaming && g.activeWifi == activeWifi {
		g.mu.Unlock()
		return
	}
	g.currentSSID = ssid
	g.hasWifi = hasWifi
	g.hasMobile = hasMobile
	g.metered = metered
	g.roaming = roaming
	g.activeWifi = activeWifi
	g.mu.Unlock()
	g.requestReconcile()
}

// OnChargingState reports whether the device is plugged in. When charging and
// KeepSyncingWhileCharging is set, the gate runs ST continuously (battery is
// no concern) — still subject to the network gate.
func OnChargingState(charging bool) {
	g.mu.Lock()
	if g.charging == charging {
		g.mu.Unlock()
		return
	}
	g.charging = charging
	g.mu.Unlock()
	g.requestReconcile()
}

// OnBatteryLow reports whether the battery is low — the level at which
// Android shows its low-battery warning (ACTION_BATTERY_LOW / OKAY). This is
// NOT battery-saver mode. Only respected when PauseWhenBatteryLow is true.
func OnBatteryLow(low bool) {
	g.mu.Lock()
	if g.batteryLow == low {
		g.mu.Unlock()
		return
	}
	g.batteryLow = low
	g.mu.Unlock()
	g.requestReconcile()
}

// OnTriggerAlarm is called by Android's AlarmManager on every scheduled wake.
// Power conditions are checked first — no point doing any work if ST can't run.
// Then trigger-specific logic decides whether to open a sync session:
//   - periodic/scheduled: always open one.
//   - on_change: backstop tick — skip if nothing local is pending and all folders
//     are send-only (nothing to receive either).
//   - on_change_poll: primary trigger — compare directory mtimes; open a session
//     only when a structural change was detected or a folder can receive. Leaving
//     the snapshot stale while conditions aren't met means changes accumulate and
//     are caught the moment conditions clear.
func OnTriggerAlarm() {
	g.mu.Lock()
	trigger := g.settings.SyncTrigger
	dirty := g.dirty
	snap := g.snapshotLocked()
	g.mu.Unlock()

	// Global power gate. Network is absolute; charging overrides battery but not
	// network — mirrors desiredRunning's precedence exactly. No trigger mode
	// should scan or open a session when ST would immediately be gated off.
	chargingOverride := snap.charging && snap.settings.KeepSyncingWhileCharging
	if !snap.networkAllowed() || (!snap.batteryAllowed() && !chargingOverride) {
		g.emitEvent("tick", "conditions not met; alarm ignored")
		return
	}

	switch trigger {
	case "on_change":
		// Backstop tick behind the live file watcher. Skip if nothing is pending
		// locally and all folders are send-only — nothing to send or receive.
		if !dirty {
			if folders, err := stmanager.Folders(); err == nil && !anyFolderReceives(folders) {
				g.emitEvent("tick", "backstop — nothing pending, all send-only; staying asleep")
				return
			}
		}
	case "on_change_poll":
		changed := pollCheckChanged()
		if !changed {
			if folders, err := stmanager.Folders(); err == nil && !anyFolderReceives(folders) {
				g.emitEvent("tick", "poll — no changes, all send-only; staying asleep")
				return
			}
			g.emitEvent("tick", "poll — no local changes; opening session to receive from peers")
		} else {
			g.emitEvent("tick", "poll — changes detected")
		}
	}
	OpenSyncSession()
}

// anyFolderReceives reports whether any folder can pull from peers (is not
// sendonly). A device whose folders are all sendonly has nothing to receive, so
// a backstop tick only matters when it has unsent local changes. Empty type is
// ST's default (sendreceive) ⇒ counts as receiving, the safe side.
func anyFolderReceives(folders []stmanager.STFolder) bool {
	for _, f := range folders {
		if f.Type != "sendonly" {
			return true
		}
	}
	return false
}

// markDirty records that the file watcher saw a local change — we (probably)
// now have unsynced local state. It's a best-effort proxy set while ST sleeps;
// the reconcile loop overwrites it with ST's authoritative completion once ST
// is awake and idle. Kept set until then so the backstop tick keeps retrying
// (e.g. a change made while the only peer was offline).
func markDirty() {
	g.mu.Lock()
	g.dirty = true
	g.dirtyGen++ // lets reconcileDirty detect a change that lands during its probe
	g.mu.Unlock()
}

// OpenSyncSession opens a sync session: ST is allowed to run (subject to the
// network/battery gates) until it reports the sync complete. Called by the
// on_change file watcher on a settled change, by OnTriggerAlarm (periodic /
// scheduled / on_change backstop), on cold-start catch-up, and by the manual
// "Sync now" button. Replaces the old OnTriggerTick / SyncNow pair.
func OpenSyncSession() {
	g.emitEvent("trigger", "session opened — starting sync")
	g.mu.Lock()
	now := time.Now()
	// Only mark a fresh start when no session is currently open. Re-triggering an
	// in-flight session (backstop tick, manual "Sync now", the watcher settling
	// again) must NOT reset sessionStartedAt — it anchors the connect-grace window
	// — nor the stall guard, which is tracking the live transfer.
	if !now.Before(g.sessionEndsAt) {
		g.sessionStartedAt = now
		g.stallPolls = 0
		g.lastTransferBytes = 0
	}
	g.sessionEndsAt = now.Add(connectGrace)
	g.mu.Unlock()
	g.requestReconcile()
}

// ShouldStayResident reports whether the gate currently needs the bundled
// Syncthing running for a background reason — an open sync session, on_change's
// resident watch, or "keep syncing while charging" — all subject to the same
// network/battery gates ST itself obeys. The Android service polls this to
// decide whether to self-stop after the user leaves.
//
// This is the SINGLE source of truth for "must the process stay alive?": it is
// literally the gate's own desiredRunning decision, so the service can never
// disagree with the gate about whether ST should be up. The previous split —
// isSyncSessionActive OR shouldKeepServiceAlive — left charging out entirely,
// so a backgrounded periodic/scheduled sync while plugged in had the gate
// wanting ST up but the service tearing the process down after the grace
// anyway, silently defeating "keep syncing while charging".
func ShouldStayResident() bool {
	g.mu.Lock()
	snap := g.snapshotLocked()
	g.mu.Unlock()
	// This is asked only on the shutdown path, i.e. when the UI is gone, so the
	// foreground reason for keeping ST up doesn't apply — the activity handles
	// that by cancelling the scheduled shutdown on resume. Strip it so a stuck
	// or late foreground flag can't pin the service after a task-swipe (some
	// OEM skins skip onPause, leaving appForeground believed-true), which would
	// defeat onTaskRemoved's self-stop backstop.
	snap.appForeground = false
	snap.foregroundUntil = time.Time{}
	// on_change keeps us alive to host the file watcher even when ST itself is
	// asleep: the watcher must stay resident to notice changes (and to re-open a
	// session when the network returns). ST sleeps; only this lightweight process
	// lingers. Without this the service would self-stop between syncs and the
	// watcher would die with it.
	if snap.settings.SyncTrigger == "on_change" {
		return true
	}
	return snap.desiredRunning(time.Now())
}

// SetPowerHost registers the platform wrapper's callback so the gate can
// push run-state changes (hold/release radio + CPU). Pass nil to clear.
func SetPowerHost(h PowerHost) {
	g.mu.Lock()
	g.host = h
	lastSync := g.lastSyncActive
	lastWatcher := g.lastWatcherActive
	g.mu.Unlock()
	// Re-assert current state so a freshly-registered host isn't left
	// out of sync with a session or watcher that's already running.
	if h != nil {
		func() {
			defer func() { _ = recover() }()
			h.OnSyncActive(lastSync)
		}()
		func() {
			defer func() { _ = recover() }()
			h.OnWatcherActive(lastWatcher)
		}()
	}
}

// RefreshPowerSettings re-reads PowerSettings from the database and re-
// evaluates the gate. Called by the Android side after the user changes a
// setting through the WS UI (PUT /api/power persists, then triggers this).
func RefreshPowerSettings() {
	if err := refreshSettingsFromDB(); err != nil {
		log.Printf("events: refresh settings: %v", err)
		return
	}
	g.requestReconcile()
}

// WakePlanJSON tells the platform wrapper exactly what to schedule. The gate
// owns the interpretation of the trigger mode; Android is a dumb executor that
// arms whatever this returns:
//
//	{ "mode": "periodic|scheduled|on_change",
//	  "periodicMinutes": 120,
//	  "scheduledTimes": ["07:00","19:00"] }
//
// on_change uses periodicMinutes too — as the periodic check-in (Android arms
// the same periodic alarm). The live file watcher is the low-latency path for
// SENDING local changes; the check-in is mainly how we RECEIVE changes from
// peers — and it also retries an offline peer's backup and catches anything the
// watcher missed. OnTriggerAlarm gates whether it actually opens a session
// (skipped when all folders are send-only and nothing local is pending).
func WakePlanJSON() string {
	g.mu.Lock()
	s := g.settings
	g.mu.Unlock()

	type plan struct {
		Mode            string   `json:"mode"`
		PeriodicMinutes int      `json:"periodicMinutes"`
		ScheduledTimes  []string `json:"scheduledTimes"`
	}
	b, _ := json.Marshal(plan{
		Mode:            s.SyncTrigger,
		PeriodicMinutes: s.PeriodicMinutes,
		ScheduledTimes:  s.ScheduledTimes,
	})
	return string(b)
}

// GateStatusJSON returns a JSON snapshot of everything the power gate
// observes. The frontend's StatusPanel polls this so users can see exactly
// why sync is or isn't happening right now.
func GateStatusJSON() string {
	g.mu.Lock()
	snap := g.snapshotLocked()
	hasMobile := g.hasMobile
	g.mu.Unlock()

	now := time.Now()
	windowOpen := snap.sessionOpen(now)
	endsIn := int64(0)
	if windowOpen && !snap.sessionEndsAt.IsZero() {
		endsIn = int64(snap.sessionEndsAt.Sub(now).Seconds())
		if endsIn < 0 {
			endsIn = 0
		}
	}
	type status struct {
		STRunning        bool   `json:"stRunning"`
		AppForeground    bool   `json:"appForeground"`
		HasWifi          bool   `json:"hasWifi"`
		HasMobile        bool   `json:"hasMobile"`
		CurrentSSID      string `json:"currentSSID"`
		BatteryLow       bool   `json:"batteryLow"`
		Charging         bool   `json:"charging"`
		Metered          bool   `json:"metered"`
		Roaming          bool   `json:"roaming"`
		ActiveWifi       bool   `json:"activeWifi"`
		NetworkAllowed   bool   `json:"networkAllowed"`
		WindowOpen       bool   `json:"triggerWindowOpen"`
		WindowEndsInSecs int64  `json:"windowEndsInSecs"`
	}
	s := status{
		STRunning:        stmanager.IsRunning(),
		AppForeground:    snap.appForeground,
		HasWifi:          snap.hasWifi,
		HasMobile:        hasMobile,
		CurrentSSID:      snap.currentSSID,
		BatteryLow:       snap.batteryLow,
		Charging:         snap.charging,
		Metered:          snap.metered,
		Roaming:          snap.roaming,
		ActiveWifi:       snap.activeWifi,
		NetworkAllowed:   snap.networkAllowed(),
		WindowOpen:       windowOpen,
		WindowEndsInSecs: endsIn,
	}
	b, _ := json.Marshal(s)
	return string(b)
}

// LogPowerEvent appends a row to the persistent activity log. The Android
// wrapper uses this to record service lifecycle moments (wake, shutdown)
// that aren't visible to the Go gate but matter for users verifying
// autonomous behaviour. Best-effort.
func LogPowerEvent(kind, message string) {
	g.emitEvent(kind, message)
}

// RecentPowerEventsJSON returns up to `limit` most recent power_events rows
// as a JSON array, newest-first. The Android setup screen polls this so
// users can see the gate's autonomous activity without the WebView.
func RecentPowerEventsJSON(limit int) string {
	g.mu.Lock()
	s := g.store
	g.mu.Unlock()
	if s == nil {
		return "[]"
	}
	events, err := s.ListPowerEvents(limit)
	if err != nil {
		return "[]"
	}
	b, _ := json.Marshal(events)
	return string(b)
}
