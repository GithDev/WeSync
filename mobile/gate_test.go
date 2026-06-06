package mobile

import (
	"sync/atomic"
	"testing"
	"time"

	"wesync/internal/stmanager"
	"wesync/internal/store"
)

// These tests pin the gate's single decision: should ST be running right
// now? The gate no longer touches folder pause state; that's owned
// entirely by the user via the WS UI.

func newSnap(s store.PowerSettings) snapshot {
	return snapshot{stExePath: "/fake/libsyncthing.so", settings: s}
}

func TestDesiredRunning_Foreground_AlwaysTrue(t *testing.T) {
	// Foreground forces ST on regardless of network / battery / session —
	// the UI needs the API to function.
	cases := []struct {
		name             string
		hasWifi, saverOn bool
		mode             string
	}{
		{"on_change no wifi", false, false, "on_change"},
		{"on_change saver on", true, true, "on_change"},
		{"periodic no wifi", false, false, "periodic"},
		{"periodic session closed", true, false, "periodic"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSnap(store.PowerSettings{
				SyncTrigger:         c.mode,
				NetworkMode:         "any_wifi",
				PauseWhenBatteryLow: c.saverOn,
			})
			s.appForeground = true
			s.hasWifi = c.hasWifi
			s.batteryLow = c.saverOn
			if !s.desiredRunning(time.Now()) {
				t.Errorf("foreground must always run ST, got false")
			}
		})
	}
}

func TestDesiredRunning_Periodic_RequiresAllGates(t *testing.T) {
	// periodic/scheduled only run ST inside an open trigger window, and then
	// only if network + battery gates also pass.
	cases := []struct {
		name        string
		hasWifi     bool
		saverOn     bool
		sessionOpen bool
		expected    bool
	}{
		{"all good", true, false, true, true},
		{"no wifi", false, false, true, false},
		{"saver active", true, true, true, false},
		{"window closed", true, false, false, false},
		{"nothing aligned", false, true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSnap(store.PowerSettings{
				SyncTrigger:         "periodic",
				NetworkMode:         "any_wifi",
				PauseWhenBatteryLow: true,
			})
			s.appForeground = false
			s.hasWifi = c.hasWifi
			s.batteryLow = c.saverOn
			if c.sessionOpen {
				s.sessionEndsAt = time.Now().Add(5 * time.Minute)
			}
			if got := s.desiredRunning(time.Now()); got != c.expected {
				t.Errorf("got %v, want %v", got, c.expected)
			}
		})
	}
}

func TestDesiredRunning_OnChange_SessionGated(t *testing.T) {
	// on_change no longer keeps ST resident — ST is the heavy part and now sleeps
	// between sessions just like periodic. WeSync's own file watcher opens a
	// session on a change; with NO open session ST stays off, even on a fine
	// network. (This is the whole point of the redesign: ST sleeps.)
	mk := func() snapshot {
		s := newSnap(store.PowerSettings{
			SyncTrigger:         "on_change",
			NetworkMode:         "any_wifi",
			PauseWhenBatteryLow: true,
		})
		s.appForeground = false
		s.hasWifi = true
		return s
	}

	s := mk() // network ok, no open session
	if s.desiredRunning(time.Now()) {
		t.Errorf("on_change on wifi with no session: ST should sleep, not run")
	}

	s = mk() // a session is open (watcher/tick opened it) → ST runs
	s.sessionEndsAt = time.Now().Add(time.Minute)
	if !s.desiredRunning(time.Now()) {
		t.Errorf("on_change with an open session: ST should run")
	}

	s = mk() // network gate still applies even with a session open
	s.hasWifi = false
	s.sessionEndsAt = time.Now().Add(time.Minute)
	if s.desiredRunning(time.Now()) {
		t.Errorf("on_change without wifi: should not run even with a session")
	}

	s = mk() // battery gate still applies
	s.batteryLow = true
	s.sessionEndsAt = time.Now().Add(time.Minute)
	if s.desiredRunning(time.Now()) {
		t.Errorf("on_change with battery low: should not run")
	}
}

func TestDesiredRunning_TrustedWifi_MatchingSSID(t *testing.T) {
	s := newSnap(store.PowerSettings{
		SyncTrigger:  "periodic",
		NetworkMode:  "trusted_wifi",
		TrustedSSIDs: []string{"Home", "Office"},
	})
	s.appForeground = false
	s.hasWifi = true
	s.sessionEndsAt = time.Now().Add(time.Minute)

	s.currentSSID = "CoffeeShop"
	if s.desiredRunning(time.Now()) {
		t.Errorf("trusted_wifi on unlisted SSID: should not run")
	}

	s.currentSSID = "home" // case-insensitive
	if !s.desiredRunning(time.Now()) {
		t.Errorf("trusted_wifi on listed SSID (case-insensitive): should run")
	}

	s.currentSSID = ""
	if s.desiredRunning(time.Now()) {
		t.Errorf("trusted_wifi with empty SSID (no location perm): should not run")
	}
}

func TestDesiredRunning_BatteryLow_OnlyMattersIfEnabled(t *testing.T) {
	s := newSnap(store.PowerSettings{
		SyncTrigger:         "periodic",
		NetworkMode:         "any",
		PauseWhenBatteryLow: false,
	})
	s.appForeground = false
	s.batteryLow = true
	s.sessionEndsAt = time.Now().Add(time.Minute)
	if !s.desiredRunning(time.Now()) {
		t.Errorf("battery low active but PauseWhenBatteryLow=false: should run")
	}

	s.settings.PauseWhenBatteryLow = true
	if s.desiredRunning(time.Now()) {
		t.Errorf("battery low active and PauseWhenBatteryLow=true: should not run")
	}
}

func TestDesiredRunning_ForegroundGrace(t *testing.T) {
	// After the app loses foreground, ST stays up until foregroundUntil lapses,
	// regardless of the trigger/network/battery gates — a transient background
	// (folder picker, permission dialog) must not tear ST down. This is the
	// fix for "connection refused" when creating a folder unplugged: the SAF
	// picker backgrounds the app, and without the grace the gate stopped ST.
	mk := func() snapshot {
		s := newSnap(store.PowerSettings{
			SyncTrigger:         "periodic", // window-gated; closed here
			NetworkMode:         "any_wifi",
			PauseWhenBatteryLow: true,
		})
		s.appForeground = false // backgrounded
		s.hasWifi = false       // network gate would otherwise say no
		return s
	}
	now := time.Now()

	// Within the grace: runs even though the trigger window is closed and the
	// network gate fails.
	s := mk()
	s.foregroundUntil = now.Add(30 * time.Second)
	if !s.desiredRunning(now) {
		t.Errorf("within foreground grace: ST should stay running")
	}

	// Grace lapsed: falls back to the normal gates → off.
	s = mk()
	s.foregroundUntil = now.Add(-time.Second)
	if s.desiredRunning(now) {
		t.Errorf("after foreground grace lapses: ST should stop (gates fail)")
	}

	// No grace pending (zero value) behaves as before.
	s = mk()
	if s.desiredRunning(now) {
		t.Errorf("no grace + closed gates: ST should be off")
	}
}

func TestSessionOpen(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		endsAt   time.Time
		expected bool
	}{
		{"never opened", time.Time{}, false},
		{"future deadline", now.Add(time.Minute), true},
		{"past deadline", now.Add(-time.Minute), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSnap(store.PowerSettings{SyncTrigger: "periodic"})
			s.sessionEndsAt = c.endsAt
			if got := s.sessionOpen(now); got != c.expected {
				t.Errorf("got %v, want %v", got, c.expected)
			}
		})
	}
}

func TestNetworkAllowed(t *testing.T) {
	cases := []struct {
		name     string
		mode     string
		hasWifi  bool
		ssid     string
		trusted  []string
		expected bool
	}{
		{"any: no network", "any", false, "", nil, true},
		{"any: wifi", "any", true, "Home", nil, true},
		{"any_wifi: no wifi", "any_wifi", false, "", nil, false},
		{"any_wifi: wifi", "any_wifi", true, "Anything", nil, true},
		{"trusted: matching", "trusted_wifi", true, "Home", []string{"Home"}, true},
		{"trusted: matching case-fold", "trusted_wifi", true, "HOME", []string{"home"}, true},
		{"trusted: not matching", "trusted_wifi", true, "Cafe", []string{"Home"}, false},
		{"trusted: no wifi", "trusted_wifi", false, "", []string{"Home"}, false},
		{"trusted: empty ssid", "trusted_wifi", true, "", []string{"Home"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSnap(store.PowerSettings{
				NetworkMode:  c.mode,
				TrustedSSIDs: c.trusted,
			})
			s.hasWifi = c.hasWifi
			s.currentSSID = c.ssid
			if got := s.networkAllowed(); got != c.expected {
				t.Errorf("got %v, want %v", got, c.expected)
			}
		})
	}
}

func TestNextSessionEnd(t *testing.T) {
	// Fixed reference instant so the test is deterministic.
	start := time.Unix(1_700_000_000, 0)

	t.Run("busy extends to now+activeSyncExtend", func(t *testing.T) {
		now := start.Add(20 * time.Second)
		cur := start.Add(connectGrace)
		got := nextSessionEnd(now, start, cur, true, true)
		want := now.Add(activeSyncExtend)
		if !got.Equal(want) {
			t.Errorf("busy: got %v, want %v", got, want)
		}
	})

	t.Run("busy extends with no ceiling — a large transfer outlasts any cap", func(t *testing.T) {
		now := start.Add(6 * time.Hour) // far past the old 60-min cap
		got := nextSessionEnd(now, start, now, true, true)
		want := now.Add(activeSyncExtend)
		if !got.Equal(want) {
			t.Errorf("no-cap: got %v, want %v", got, want)
		}
	})

	t.Run("idle and not yet connected holds open within grace", func(t *testing.T) {
		now := start.Add(10 * time.Second) // inside connectGrace
		cur := start.Add(connectGrace)
		got := nextSessionEnd(now, start, cur, false, false)
		want := start.Add(connectGrace)
		if !got.Equal(want) {
			t.Errorf("connect grace: got %v, want %v", got, want)
		}
	})

	t.Run("idle and connected lets deadline lapse (no extend)", func(t *testing.T) {
		now := start.Add(10 * time.Second)
		cur := start.Add(connectGrace)
		got := nextSessionEnd(now, start, cur, false, true)
		if !got.Equal(cur) {
			t.Errorf("idle+connected: got %v, want unchanged %v", got, cur)
		}
	})

	t.Run("idle past connect grace does not extend", func(t *testing.T) {
		now := start.Add(connectGrace + time.Second)
		cur := start.Add(connectGrace)
		got := nextSessionEnd(now, start, cur, false, false)
		if !got.Equal(cur) {
			t.Errorf("past grace idle: got %v, want unchanged %v", got, cur)
		}
	})
}

func TestStallTick(t *testing.T) {
	// Progress always resets the counter, regardless of how high it was.
	if n, stalled := stallTick(2, true); n != 0 || stalled {
		t.Errorf("progress: got (%d,%v), want (0,false)", n, stalled)
	}
	// No progress increments; stalls exactly at the limit.
	n, stalled := 0, false
	for i := 1; i <= stallPollLimit; i++ {
		n, stalled = stallTick(n, false)
		wantStalled := i >= stallPollLimit
		if n != i || stalled != wantStalled {
			t.Errorf("poll %d: got (%d,%v), want (%d,%v)", i, n, stalled, i, wantStalled)
		}
	}
	// A single progress poll mid-stall clears it.
	if n, stalled := stallTick(stallPollLimit, true); n != 0 || stalled {
		t.Errorf("recover: got (%d,%v), want (0,false)", n, stalled)
	}
}

func TestMarkStopped_RevokesRunAuthority(t *testing.T) {
	// After teardown the gate must not be able to restart ST even if it
	// still thinks the app is foreground. reconcileOnce bails on an empty
	// stExePath, so clearing it is the local guarantee (no reliance on the
	// Android service's foreground invariant).
	g.mu.Lock()
	g.stExePath = "/fake/libsyncthing.so"
	g.appForeground = true
	g.sessionStartedAt = time.Now()
	g.sessionEndsAt = time.Now().Add(time.Hour)
	g.mu.Unlock()

	g.markStopped()

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stExePath != "" {
		t.Errorf("stExePath must be cleared after markStopped, got %q", g.stExePath)
	}
	if !g.sessionEndsAt.IsZero() {
		t.Errorf("session must be closed after markStopped")
	}
}

func TestShouldStayResident(t *testing.T) {
	// ShouldStayResident is what the Android service polls to decide whether to
	// self-stop. It must mirror the gate's own background run decision — and in
	// particular honor "keep syncing while charging", which the old split
	// (isSyncSessionActive || on_change) ignored, letting a plugged-in periodic
	// sync die after the grace.
	set := func(mutate func()) {
		g.mu.Lock()
		g.appForeground = false
		g.charging = false
		g.metered = false
		g.roaming = false
		g.hasWifi = true
		g.sessionStartedAt = time.Time{}
		g.sessionEndsAt = time.Time{}
		g.foregroundUntil = time.Time{}
		g.settings = store.PowerSettings{
			SyncTrigger:              "periodic", // window-gated; closed here
			NetworkMode:              "any",
			KeepSyncingWhileCharging: true,
			BlockMeteredRoaming:      true,
		}
		mutate()
		g.mu.Unlock()
	}
	t.Cleanup(func() { g.markStopped() })

	// Backgrounded, periodic, no open window, not charging → nothing to keep us
	// up. The service may self-stop.
	set(func() {})
	if ShouldStayResident() {
		t.Errorf("idle periodic background: should NOT stay resident")
	}

	// Same, but plugged in: the gate wants ST up, so the service must stay alive.
	// This is the bug the fix addresses.
	set(func() { g.charging = true })
	if !ShouldStayResident() {
		t.Errorf("charging + KeepSyncingWhileCharging: should stay resident")
	}

	// An open sync session keeps us alive (an in-flight sync mustn't be torn down).
	set(func() { g.sessionStartedAt = time.Now(); g.sessionEndsAt = time.Now().Add(time.Hour) })
	if !ShouldStayResident() {
		t.Errorf("open session: should stay resident")
	}

	// A believed-true foreground flag must NOT pin the service: this query runs
	// only when the UI is gone, and some OEMs skip onPause on swipe, leaving the
	// flag stuck true. Foreground is the activity's concern (cancel-shutdown),
	// not a residency reason.
	set(func() { g.appForeground = true })
	if ShouldStayResident() {
		t.Errorf("stuck foreground flag must not keep the service resident")
	}

	// on_change pins the service even with nothing else going on and ST asleep:
	// it must stay alive to host the file watcher (which notices changes and
	// re-opens sessions). Set a state where periodic would self-stop.
	set(func() { g.settings.SyncTrigger = "on_change" })
	if !ShouldStayResident() {
		t.Errorf("on_change must keep the service resident to host the watcher")
	}
}

func TestAnyFolderReceives(t *testing.T) {
	cases := []struct {
		name    string
		folders []stmanager.STFolder
		want    bool
	}{
		{"empty", nil, false},
		{"all sendonly", []stmanager.STFolder{{Type: "sendonly"}, {Type: "sendonly"}}, false},
		{"one sendreceive", []stmanager.STFolder{{Type: "sendonly"}, {Type: "sendreceive"}}, true},
		{"receiveonly counts as receiving", []stmanager.STFolder{{Type: "receiveonly"}}, true},
		{"empty type is ST default sendreceive", []stmanager.STFolder{{Type: ""}}, true},
	}
	for _, c := range cases {
		if got := anyFolderReceives(c.folders); got != c.want {
			t.Errorf("%s: anyFolderReceives = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestChangeBatcher_FixedDelayFromFirstChange(t *testing.T) {
	var fires int32
	b := newChangeBatcher(40*time.Millisecond, func() { atomic.AddInt32(&fires, 1) })

	// A flurry of changes within one window must collapse to a single fire —
	// the fixed delay runs from the FIRST change and ignores the rest.
	for i := 0; i < 10; i++ {
		b.notifyChange()
	}
	time.Sleep(90 * time.Millisecond)
	if n := atomic.LoadInt32(&fires); n != 1 {
		t.Fatalf("flurry should fire once, fired %d times", n)
	}

	// A later change, after the first window closed, fires again.
	b.notifyChange()
	time.Sleep(90 * time.Millisecond)
	if n := atomic.LoadInt32(&fires); n != 2 {
		t.Fatalf("a new change after the window should fire again, total fires = %d", n)
	}
}

func TestDesiredRunning_ChargingModifier(t *testing.T) {
	mk := func() snapshot {
		s := newSnap(store.PowerSettings{
			SyncTrigger:              "scheduled", // window-gated; closed by default
			NetworkMode:              "any",
			PauseWhenBatteryLow:      true,
			KeepSyncingWhileCharging: true,
			BlockMeteredRoaming:      true,
		})
		s.appForeground = false
		return s
	}

	// Charging + KeepSyncingWhileCharging → runs continuously even with the
	// trigger window closed and battery low on.
	s := mk()
	s.charging = true
	s.batteryLow = true
	if !s.desiredRunning(time.Now()) {
		t.Errorf("charging modifier should run ST regardless of trigger/saver")
	}

	// Charging but feature off → falls back to the trigger (window closed → off).
	s = mk()
	s.settings.KeepSyncingWhileCharging = false
	s.charging = true
	if s.desiredRunning(time.Now()) {
		t.Errorf("KeepSyncingWhileCharging=false: charging should not force-run")
	}

	// Charging must NOT bypass the network gate. Use a roaming link — a
	// genuinely cost-blocked network (plain metered cellular is allowed now).
	s = mk()
	s.charging = true
	s.roaming = true
	if s.desiredRunning(time.Now()) {
		t.Errorf("charging must not bypass the network/roaming gate")
	}
}

func TestNetworkAllowed_Metered(t *testing.T) {
	// block=true → the protective default. It refuses roaming and metered WiFi,
	// but NOT ordinary metered cellular (Android marks all cellular metered, so
	// that's just the user's normal mobile data — it should sync).
	anyMode := func(block bool) snapshot {
		return newSnap(store.PowerSettings{NetworkMode: "any", BlockMeteredRoaming: block})
	}

	if s := anyMode(true); !s.networkAllowed() {
		t.Errorf("any + unmetered: should be allowed")
	}
	// Plain metered cellular (not roaming, not WiFi) is ordinary mobile data.
	if s := anyMode(true); func() bool { s.metered = true; return s.networkAllowed() }() == false {
		t.Errorf("any + metered cellular + block: should be allowed (normal mobile data)")
	}
	// A metered WiFi (hotspot / tethering / user-marked) is still skipped.
	if s := anyMode(true); func() bool { s.metered = true; s.activeWifi = true; return s.networkAllowed() }() {
		t.Errorf("any + metered WiFi + block: should be blocked")
	}
	if s := anyMode(true); func() bool { s.roaming = true; return s.networkAllowed() }() {
		t.Errorf("any + roaming + block: should be blocked")
	}
	// With protection off, even roaming / metered WiFi pass.
	if s := anyMode(false); func() bool { s.metered = true; s.activeWifi = true; return s.networkAllowed() }() == false {
		t.Errorf("any + metered WiFi + block off: should be allowed")
	}
	if s := anyMode(false); func() bool { s.roaming = true; return s.networkAllowed() }() == false {
		t.Errorf("any + roaming + block off: should be allowed")
	}

	// The metered-WiFi gate applies in ALL modes, not just "any".
	w := newSnap(store.PowerSettings{NetworkMode: "any_wifi", BlockMeteredRoaming: true})
	w.hasWifi = true
	w.metered = true
	w.activeWifi = true
	if w.networkAllowed() {
		t.Errorf("any_wifi + metered WiFi + block: should be blocked")
	}
	w.settings.BlockMeteredRoaming = false
	if !w.networkAllowed() {
		t.Errorf("any_wifi + metered WiFi + block off: should be allowed")
	}
}
