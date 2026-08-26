//go:build windows

package main

import (
	"errors"
	"log"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// Wails handles a WebView2 browser-process crash (typically across sleep/resume)
// with a modal dialog and a hardcoded os.Exit(-1) — see frontend.go
// ProcessFailedCallback. os.Exit skips cleanup, so the tray icon is never
// removed: the app looks alive in the tray but has no window or message loop, so
// clicking the icon does nothing and the taskbar menu never opens.
//
// Fix: the top-level process supervises a child that runs the real app and
// restarts it on a crash. The tray stays in the child with the window, since the
// menu drives it directly.

const createNoWindowFlag = 0x08000000

const respawnEnvVar = "WESYNC_APP_CHILD"

// Signalled by the installer before it kills "wesync-app" — a name that now
// matches both supervisor and child. Kill order is not guaranteed, so without
// this the supervisor could read the kill as a crash and relaunch, locking the
// .exe being replaced.
const shutdownEventName = `Local\WeSyncAppShutdown`

func isSupervisedChild() bool { return os.Getenv(respawnEnvVar) == "1" }

// Bounded so a deterministic crash cannot become an endless respawn loop.
// The budget resets after a healthy run, so crashes spread over weeks do not
// permanently exhaust it.
const (
	maxRestarts    = 5
	restartBackoff = 2 * time.Second
	healthyRunTime = 60 * time.Second
)

// --hidden means "silent autostart at login", not "stay invisible" — a restart
// that kept it would come back as a tray icon with no window.
func withoutHiddenFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--hidden" {
			continue
		}
		out = append(out, a)
	}
	return out
}

func exeStat() (os.FileInfo, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return os.Stat(exe)
}

// Detects an install/uninstall running underneath us. An unreadable baseline
// disables the check — restarting beats refusing on a stat error.
func exeChangedSince(exe string, baseline os.FileInfo) (bool, string) {
	if baseline == nil {
		return false, ""
	}
	now, err := os.Stat(exe)
	if err != nil {
		return true, "our binary is gone (install or uninstall in progress)"
	}
	if now.Size() != baseline.Size() || !now.ModTime().Equal(baseline.ModTime()) {
		return true, "our binary was replaced (upgrade in progress)"
	}
	return false, ""
}

func shutdownRequested(h windows.Handle) bool {
	if h == 0 {
		return false
	}
	s, err := windows.WaitForSingleObject(h, 0) // poll, never block
	return err == nil && s == windows.WAIT_OBJECT_0
}

// superviseChild blocks until the child exits cleanly or the restart budget runs
// out.
func superviseChild() {
	// Created, not opened, so the event exists for the installer to signal.
	// Manual-reset: once shutdown is requested it stays requested.
	name, err := windows.UTF16PtrFromString(shutdownEventName)
	var shutdownEvt windows.Handle
	if err == nil {
		if h, e := windows.CreateEvent(nil, 1, 0, name); e == nil {
			shutdownEvt = h
			defer windows.CloseHandle(shutdownEvt) //nolint:errcheck
		}
	}

	ourExeInfo, _ := exeStat()

	restarts := 0
	for {
		exe, err := os.Executable()
		if err != nil {
			log.Printf("supervisor: cannot resolve own path: %v", err)
			return
		}

		args := os.Args[1:]
		if restarts > 0 {
			args = withoutHiddenFlag(args)
		}

		cmd := exec.Command(exe, args...)
		cmd.Env = append(os.Environ(), respawnEnvVar+"=1")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow:    true,
			CreationFlags: createNoWindowFlag,
		}

		started := time.Now()
		if err := cmd.Start(); err != nil {
			log.Printf("supervisor: cannot start GUI process: %v", err)
			return
		}
		log.Printf("supervisor: GUI process started (pid %d)", cmd.Process.Pid)

		err = cmd.Wait()
		ran := time.Since(started)

		if err == nil {
			log.Printf("supervisor: GUI exited normally — shutting down")
			return
		}

		if shutdownRequested(shutdownEvt) {
			log.Printf("supervisor: shutdown requested — not restarting")
			return
		}

		// Backstop for installers predating the event above: if our binary is gone
		// or replaced, stand down and let the installer start the new build.
		if changed, why := exeChangedSince(exe, ourExeInfo); changed {
			log.Printf("supervisor: %s — not restarting", why)
			return
		}

		code := exitCode(err)
		if ran >= healthyRunTime {
			restarts = 0
		}
		restarts++
		if restarts > maxRestarts {
			log.Printf("supervisor: GUI crashed (exit %s) after %s — restart budget exhausted, giving up",
				code, ran.Round(time.Millisecond))
			return
		}

		log.Printf("supervisor: GUI crashed (exit %s) after %s — restarting (%d/%d)",
			code, ran.Round(time.Millisecond), restarts, maxRestarts)
		time.Sleep(restartBackoff)
	}
}

func exitCode(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return strconv.Itoa(ee.ExitCode())
	}
	return err.Error()
}
