package mobile

import (
	"strings"
	"time"

	"wesync/internal/store"
)

// This file holds the gate's *pure* decision logic: the immutable snapshot of
// its inputs and the functions that turn that snapshot into "should ST be
// running?" Nothing here touches a process, the DB, or the network — it's all
// clock-injected and unit-testable (see gate_test.go). The process control
// that ACTS on these decisions lives in gate_reconcile.go.

// snapshot is an immutable copy of everything desiredRunning() needs.
// Taken under the lock, evaluated without it — so the blocking ST
// start/stop never happens while we hold g.mu.
type snapshot struct {
	stExePath       string
	appForeground   bool
	currentSSID     string
	hasWifi         bool
	batteryLow      bool
	charging        bool
	metered         bool
	roaming         bool
	activeWifi      bool
	settings        store.PowerSettings
	sessionEndsAt   time.Time
	foregroundUntil time.Time
}

// snapshotLocked copies the decision inputs. Caller must hold g.mu.
func (g *gate) snapshotLocked() snapshot {
	return snapshot{
		stExePath:       g.stExePath,
		appForeground:   g.appForeground,
		currentSSID:     g.currentSSID,
		hasWifi:         g.hasWifi,
		batteryLow:      g.batteryLow,
		charging:        g.charging,
		metered:         g.metered,
		roaming:         g.roaming,
		activeWifi:      g.activeWifi,
		settings:        g.settings,
		sessionEndsAt:   g.sessionEndsAt,
		foregroundUntil: g.foregroundUntil,
	}
}

// desiredRunning is THE computed property. Pure function of the snapshot;
// runs every check in order, every time.
func (s snapshot) desiredRunning(now time.Time) bool {
	// Foreground — or within the short grace after losing it — forces ST on so
	// the UI's API calls work and a transient background (picker, dialog) can't
	// tear ST down out from under the next request.
	if s.appForeground || now.Before(s.foregroundUntil) {
		return true
	}
	// Network gate (privacy + metered/roaming cost) applies to ALL background
	// running — even charging can't buy past it.
	if !s.networkAllowed() {
		return false
	}
	// Charging is a modifier, not a mode: plugged in → battery is no concern →
	// run continuously regardless of the trigger or low-battery gate. This is
	// what makes "fast backup, never worry about battery" work.
	if s.charging && s.settings.KeepSyncingWhileCharging {
		return true
	}
	// On battery: respect the low-battery gate.
	if !s.batteryAllowed() {
		return false
	}
	// Every trigger mode gates ST on an open session — ST is the heavy part
	// (scan/hash/network), so it sleeps between sessions. periodic/scheduled and
	// on_change_poll all open a session on their alarm; ST wakes, syncs, and sleeps.
	return s.sessionOpen(now)
}

func (s snapshot) batteryAllowed() bool {
	return !s.settings.PauseWhenBatteryLow || !s.batteryLow
}

func (s snapshot) networkAllowed() bool {
	// Cost protection, applied in EVERY network mode (on by default). Android
	// marks ALL cellular as metered, so `metered` alone can't mean "capped" —
	// ordinary mobile data at home is the user's normal plan and should sync.
	// We refuse only the connections that genuinely cost extra: a roaming
	// cellular link, or a metered WiFi (a phone hotspot, tethering, or a
	// network the user marked metered). Metered cellular that isn't roaming
	// passes through.
	if s.settings.BlockMeteredRoaming && (s.roaming || (s.metered && s.activeWifi)) {
		return false
	}
	switch s.settings.NetworkMode {
	case "any":
		return true
	case "any_wifi":
		return s.hasWifi
	case "trusted_wifi":
		if !s.hasWifi || s.currentSSID == "" {
			return false
		}
		for _, ssid := range s.settings.TrustedSSIDs {
			if strings.EqualFold(ssid, s.currentSSID) {
				return true
			}
		}
		return false
	}
	return true
}

func (s snapshot) sessionOpen(now time.Time) bool {
	return now.Before(s.sessionEndsAt)
}

// reasonString assembles a short "why" string for the event log.
func (s snapshot) reasonString() string {
	parts := []string{}
	if s.appForeground {
		parts = append(parts, "WS foreground")
	} else {
		parts = append(parts, "WS background")
	}
	parts = append(parts, "network="+s.settings.NetworkMode)
	if s.currentSSID != "" {
		parts = append(parts, "ssid="+s.currentSSID)
	}
	parts = append(parts, "trigger="+s.settings.SyncTrigger)
	if s.batteryLow && s.settings.PauseWhenBatteryLow {
		parts = append(parts, "battery-low")
	}
	return strings.Join(parts, ", ")
}

// nextSessionEnd computes the new session deadline after a poll, given ST's
// reported activity. There is no hard session cap — a large transfer may outlast
// any fixed ceiling, so `busy` keeps extending indefinitely; the caller's stall
// guard (see stallTick) is what stops a no-progress session. Pure + clock-
// injected so it's unit-testable without a live Syncthing.
func nextSessionEnd(now, startedAt, current time.Time, busy, connected bool) time.Time {
	if busy {
		return now.Add(activeSyncExtend)
	}
	if !connected && now.Before(startedAt.Add(connectGrace)) {
		// Still trying to find/connect peers — keep the window open.
		return startedAt.Add(connectGrace)
	}
	// Idle and (connected or past the connect grace): don't extend, don't
	// shrink — let the existing deadline lapse so a brief idle between
	// files doesn't tear us down mid-sync.
	return current
}

// stallTick advances the stall-guard counter for the keepalive. progressed=true
// (transferred bytes moved past the floor since the last poll) resets it;
// otherwise it increments. Returns the new count and whether we've hit the stall
// limit — at which point a peer-pull keepalive should no longer extend the
// session. Pure so it's unit-testable.
func stallTick(stallPolls int, progressed bool) (next int, stalled bool) {
	if progressed {
		return 0, false
	}
	next = stallPolls + 1
	return next, next >= stallPollLimit
}
