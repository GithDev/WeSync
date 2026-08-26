//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestIsSupervisedChild(t *testing.T) {
	t.Setenv(respawnEnvVar, "")
	if isSupervisedChild() {
		t.Error("a process without the marker env var is the supervisor, not the child")
	}

	t.Setenv(respawnEnvVar, "1")
	if !isSupervisedChild() {
		t.Error("a process with the marker env var must run the app, not supervise another child")
	}
}

// Distinguishes a user quit (0) from a WebView2 crash (-1), so it must report
// the real code.
func TestExitCodeReportsProcessExitStatus(t *testing.T) {
	err := exec.Command("cmd", "/c", "exit 3").Run()
	if err == nil {
		t.Fatal("expected the command to fail")
	}
	if got := exitCode(err); got != "3" {
		t.Errorf("exitCode() = %q, want %q", got, "3")
	}
}

func TestExitCodeFallsBackToErrorText(t *testing.T) {
	err := errors.New("some non-exit failure")
	if got := exitCode(err); got != "some non-exit failure" {
		t.Errorf("exitCode() = %q, want the error text for a non-ExitError", got)
	}
}

// The installer kills "wesync-app" by name, matching supervisor and child.
// Without these guards the supervisor reads that as a crash and relaunches,
// locking the .exe being replaced.

func TestShutdownEventStopsRestarts(t *testing.T) {
	name, err := windows.UTF16PtrFromString(`Local\WeSyncAppShutdownTest`)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateEvent(nil, 1, 0, name) // manual reset, non-signalled
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h) //nolint:errcheck

	if shutdownRequested(h) {
		t.Error("a non-signalled event must not look like a shutdown request")
	}

	if err := windows.SetEvent(h); err != nil {
		t.Fatal(err)
	}
	if !shutdownRequested(h) {
		t.Error("a signalled event must stop the supervisor from restarting")
	}

	if !shutdownRequested(h) {
		t.Error("shutdown request must persist across checks (manual-reset event)")
	}
}

func TestShutdownRequestedHandlesNoEvent(t *testing.T) {
	if shutdownRequested(0) {
		t.Error("a zero handle (event could not be created) must not block restarts")
	}
}

func TestExeChangedSinceDetectsUpgrade(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "wesync-app.exe")
	if err := os.WriteFile(exe, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	baseline, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}

	if changed, _ := exeChangedSince(exe, baseline); changed {
		t.Error("an untouched binary must not look like an upgrade")
	}

	if err := os.WriteFile(exe, []byte("a different build"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(exe, time.Now().Add(time.Minute), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if changed, _ := exeChangedSince(exe, baseline); !changed {
		t.Error("a replaced binary must stop the supervisor from restarting")
	}

	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	if changed, _ := exeChangedSince(exe, baseline); !changed {
		t.Error("a removed binary must stop the supervisor from restarting")
	}
}

// Restarting is the safer default; refusing would disable crash recovery.
func TestExeChangedSinceIgnoresUnknownBaseline(t *testing.T) {
	if changed, _ := exeChangedSince("anything", nil); changed {
		t.Error("an unknown baseline must not disable crash recovery")
	}
}

// Keeping --hidden on a restart would come back as a tray icon with no window,
// which is the bug the supervisor exists to fix.
func TestRestartDropsHiddenFlag(t *testing.T) {
	got := withoutHiddenFlag([]string{"--hidden"})
	if len(got) != 0 {
		t.Errorf("withoutHiddenFlag(--hidden) = %v, want the flag removed", got)
	}

	got = withoutHiddenFlag([]string{"--debug", "--hidden", "--port", "1234"})
	want := []string{"--debug", "--port", "1234"}
	if len(got) != len(want) {
		t.Fatalf("withoutHiddenFlag() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("withoutHiddenFlag() = %v, want %v (other args must survive)", got, want)
		}
	}
}

// Guards both ends: a budget that never resets disables restarts over time, no
// budget at all spins forever on a deterministic failure.
func TestRestartPolicyIsBounded(t *testing.T) {
	if maxRestarts <= 0 {
		t.Error("maxRestarts must allow at least one restart")
	}
	if restartBackoff <= 0 {
		t.Error("a zero backoff would busy-loop on a deterministic crash")
	}
	if healthyRunTime <= restartBackoff {
		t.Error("healthyRunTime must exceed the backoff, or a crash-looping child could be counted as healthy")
	}
}
