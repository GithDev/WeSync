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
	wasAllowed := g.snapshotLocked().networkAllowed()
	g.currentSSID = ssid
	g.hasWifi = hasWifi
	g.hasMobile = hasMobile
	g.metered = metered
	g.roaming = roaming
	g.activeWifi = activeWifi
	nowAllowed := g.snapshotLocked().networkAllowed()
	g.mu.Unlock()

	if hasWifi {
		if ssid != "" {
			g.emitEvent("net", "wifi — ssid="+ssid)
		} else {
			g.emitEvent("net", "wifi — ssid unknown (no location permission)")
		}
	} else if hasMobile {
		g.emitEvent("net", "mobile only")
	} else {
		g.emitEvent("net", "no network")
	}

	g.requestReconcile()

	// Network just became reachable — open a session immediately rather than
	// waiting for the next scheduled alarm. Covers the "came home, want sync
	// now" case for all trigger modes. Guard against rapid reconnects (e.g.
	// mesh AP handoffs) that would otherwise reset sessionEndsAt on every
	// transition and keep ST running indefinitely without a real sync trigger.
	if !wasAllowed && nowAllowed {
		g.mu.Lock()
		sessionActive := time.Now().Before(g.sessionEndsAt)
		g.mu.Unlock()
		if !sessionActive {
			OpenSyncSession()
		}
	}
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
//   - on_change_poll: safety-net backstop — compare directory mtimes for the log,
//     then open a session unconditionally. OnTriggerPollAlarm is the primary
//     change-detection path; this alarm is the periodic guarantee that peer
//     changes sync even when no local structural change is detected.
func OnTriggerAlarm() {
	g.mu.Lock()
	trigger := g.settings.SyncTrigger
	snap := g.snapshotLocked()
	g.mu.Unlock()

	if !snap.networkAllowed() || !snap.batteryAllowed() {
		g.emitEvent("tick", "conditions not met; alarm ignored")
		return
	}

	if trigger == triggerOnChangePoll {
		if pollCheckChanged() {
			g.emitEvent("tick", "poll — structural changes detected")
		} else {
			g.emitEvent("tick", "poll — no structural changes; syncing anyway")
		}
	}
	OpenSyncSession()
}

// OnTriggerPollAlarm is the fast change-detection counterpart to OnTriggerAlarm.
// Called by the short-interval poll alarm for on_change_poll mode.
// Runs the mtime snapshot check and opens a session ONLY if structural changes
// are detected — no session is opened on a clean poll (saves battery). The slow
// safety-net alarm (OnTriggerAlarm) still fires unconditionally every periodicMinutes.
func OnTriggerPollAlarm() {
	g.mu.Lock()
	snap := g.snapshotLocked()
	g.mu.Unlock()

	if !snap.networkAllowed() || !snap.batteryAllowed() {
		g.emitEvent("tick", "poll: conditions not met; skipped")
		return
	}
	if pollCheckChanged() {
		g.emitEvent("tick", "poll: structural changes detected — opening session")
		OpenSyncSession()
	} else {
		g.emitEvent("tick", "poll: no structural changes")
	}
}

// OpenSyncSession opens a sync session: ST is allowed to run (subject to the
// network/battery gates) until it reports the sync complete. Called by
// OnTriggerAlarm (periodic / scheduled / on_change_poll) and by the manual
// "Sync now" button.
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
// Syncthing running for a background reason — an open sync session or "keep
// syncing while charging" — subject to the same network/battery gates ST
// itself obeys. The Android service polls this to decide whether to self-stop
// after the user leaves.
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
	return snap.desiredRunning(time.Now())
}

// SetPowerHost registers the platform wrapper's callback so the gate can
// push run-state changes (hold/release radio + CPU). Pass nil to clear.
func SetPowerHost(h PowerHost) {
	g.mu.Lock()
	g.host = h
	lastSync := g.lastSyncActive
	g.mu.Unlock()
	// Re-assert current state so a freshly-registered host isn't left
	// out of sync with a session that's already running.
	if h != nil {
		func() {
			defer func() { _ = recover() }()
			h.OnSyncActive(lastSync)
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
//	{ "mode": "periodic|scheduled|on_change_poll",
//	  "periodicMinutes": 120,        // safety-net tick for periodic / on_change_poll
//	  "onChangePollMinutes": 5,      // fast change-detection poll (on_change_poll only)
//	  "scheduledTimes": ["07:00","19:00"] }
//
// on_change_poll arms TWO alarms: a fast poll (onChangePollMinutes) that fires
// OnTriggerPollAlarm and only opens a session when changes are detected, and a
// slow safety-net (periodicMinutes) that fires OnTriggerAlarm and always syncs.
func WakePlanJSON() string {
	g.mu.Lock()
	s := g.settings
	g.mu.Unlock()

	type plan struct {
		Mode                string   `json:"mode"`
		PeriodicMinutes     int      `json:"periodicMinutes"`
		OnChangePollMinutes int      `json:"onChangePollMinutes"`
		ScheduledTimes      []string `json:"scheduledTimes"`
	}
	b, _ := json.Marshal(plan{
		Mode:                s.SyncTrigger,
		PeriodicMinutes:     s.PeriodicMinutes,
		OnChangePollMinutes: s.OnChangePollMinutes,
		ScheduledTimes:      s.ScheduledTimes,
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
		NetworkGatePassed bool  `json:"networkGatePassed"`
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
		NetworkGatePassed: snap.networkAllowed(),
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
