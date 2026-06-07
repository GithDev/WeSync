package mobile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"wesync/internal/store"
)

// helpers ---------------------------------------------------------------

func mkdirs(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("mkdirs: %v", err)
		}
	}
}

func snapKeys(snap map[string]time.Time) map[string]bool {
	out := make(map[string]bool, len(snap))
	for k := range snap {
		out[k] = true
	}
	return out
}

// scanDirMtimes tests ----------------------------------------------------

func TestScanDirMtimes_IncludesNormalDirs(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "photos", "photos/2024", "documents")
	snap := make(map[string]time.Time)
	scanDirMtimes(root, snap)

	want := []string{root, filepath.Join(root, "photos"), filepath.Join(root, "photos", "2024"), filepath.Join(root, "documents")}
	keys := snapKeys(snap)
	for _, p := range want {
		if !keys[p] {
			t.Errorf("expected %q in snapshot, not found", p)
		}
	}
}

func TestScanDirMtimes_SkipsHiddenDirs(t *testing.T) {
	root := t.TempDir()
	// Hidden dirs that must be skipped.
	mkdirs(t, root, ".stversions", ".stfolder", ".git", "visible", "visible/.hidden-inside")
	snap := make(map[string]time.Time)
	scanDirMtimes(root, snap)

	keys := snapKeys(snap)
	for _, hidden := range []string{
		filepath.Join(root, ".stversions"),
		filepath.Join(root, ".stfolder"),
		filepath.Join(root, ".git"),
		filepath.Join(root, "visible", ".hidden-inside"),
	} {
		if keys[hidden] {
			t.Errorf("hidden dir %q must be skipped, but found in snapshot", hidden)
		}
	}
	// The visible dir itself must be present.
	if !keys[filepath.Join(root, "visible")] {
		t.Errorf("visible dir must be in snapshot")
	}
}

func TestScanDirMtimes_RootWithDotPrefix(t *testing.T) {
	// If the sync folder itself starts with a dot (unusual but valid), it must
	// NOT be skipped — the exemption applies to non-root dirs only.
	parent := t.TempDir()
	root := filepath.Join(parent, ".myfolder")
	mkdirs(t, root, "sub")
	snap := make(map[string]time.Time)
	scanDirMtimes(root, snap)

	if !snapKeys(snap)[root] {
		t.Errorf("root dir starting with '.' must appear in snapshot")
	}
	if !snapKeys(snap)[filepath.Join(root, "sub")] {
		t.Errorf("non-hidden child of dot-prefixed root must appear in snapshot")
	}
}

func TestDirSnapChanged_DetectsAddedDir(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "a")
	before := make(map[string]time.Time)
	scanDirMtimes(root, before)

	mkdirs(t, root, "b")
	after := make(map[string]time.Time)
	scanDirMtimes(root, after)

	if !dirSnapChanged(before, after) {
		t.Error("adding a directory must be detected as changed")
	}
}

func TestDirSnapChanged_NothingChanged(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "a", "b")
	s1 := make(map[string]time.Time)
	scanDirMtimes(root, s1)
	s2 := make(map[string]time.Time)
	for k, v := range s1 {
		s2[k] = v
	}
	if dirSnapChanged(s1, s2) {
		t.Error("identical snapshots must not be detected as changed")
	}
}

// OnTriggerAlarm power gate tests ---------------------------------------

// setGateForAlarmTest initialises just enough of the global gate so
// OnTriggerAlarm can read settings + snapshot without panicking, and registers
// cleanup so tests don't leak state into each other.
func setGateForAlarmTest(t *testing.T, mutate func()) {
	t.Helper()
	g.mu.Lock()
	g.appForeground = false
	g.charging = false
	g.batteryLow = false
	g.hasWifi = true
	g.metered = false
	g.roaming = false
	g.activeWifi = false
	g.currentSSID = ""
	g.settings = store.PowerSettings{
		SyncTrigger:              "on_change_poll",
		NetworkMode:              "any",
		PauseWhenBatteryLow:      true,
		KeepSyncingWhileCharging: false,
		BlockMeteredRoaming:      true,
	}
	mutate()
	g.mu.Unlock()
	// Clean snapshot so we can observe whether pollCheckChanged ran.
	resetPollSnapshot()
	t.Cleanup(func() {
		g.markStopped()
		resetPollSnapshot()
	})
}

// pollDirs returns poll.dirs under the lock (test helper).
func pollDirs() map[string]time.Time {
	poll.mu.Lock()
	defer poll.mu.Unlock()
	return poll.dirs
}

func TestOnTriggerAlarm_PowerGate_NoNetwork(t *testing.T) {
	// When the network gate fails, OnTriggerAlarm must bail before pollCheckChanged.
	// Observable: poll.dirs stays nil because pollCheckChanged was never called.
	setGateForAlarmTest(t, func() {
		g.settings.NetworkMode = "any_wifi"
		g.hasWifi = false
	})
	OnTriggerAlarm()
	if pollDirs() != nil {
		t.Error("network gate must prevent pollCheckChanged; poll.dirs must stay nil")
	}
}

func TestOnTriggerAlarm_PowerGate_BatteryLow(t *testing.T) {
	// Battery low + PauseWhenBatteryLow must also bail before the scan.
	setGateForAlarmTest(t, func() {
		g.batteryLow = true
		g.settings.PauseWhenBatteryLow = true
		g.charging = false
	})
	OnTriggerAlarm()
	if pollDirs() != nil {
		t.Error("battery gate must prevent pollCheckChanged; poll.dirs must stay nil")
	}
}

func TestOnTriggerAlarm_PowerGate_NetworkFails_EvenWhenCharging(t *testing.T) {
	// Charging overrides the battery gate but NOT the network gate.
	// Even with KeepSyncingWhileCharging=true, no-wifi must still block the scan.
	setGateForAlarmTest(t, func() {
		g.settings.NetworkMode = "any_wifi"
		g.hasWifi = false
		g.charging = true
		g.settings.KeepSyncingWhileCharging = true
	})
	OnTriggerAlarm()
	if pollDirs() != nil {
		t.Error("network gate must block scan even when charging; poll.dirs must stay nil")
	}
}
