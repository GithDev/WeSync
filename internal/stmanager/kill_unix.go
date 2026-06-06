//go:build !windows

package stmanager

import (
	"log"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// killByPath terminates any process whose cmdline references the given
// executable path. Used to clear a stale Syncthing occupying our port.
func killByPath(exePath string) {
	killMatchingProcs("exe "+exePath, func(cmdline string) bool {
		return strings.Contains(cmdline, exePath)
	})
}

// killSyncthingByHome terminates any Syncthing started with OUR home dir
// (`--home=<home>` in its cmdline) — including one orphaned when Android
// reclaimed the app process and that we no longer hold a cmd handle for (the
// "adopted" case). Without this, Stop() could only kill the process WE started,
// so an adopted/orphaned ST ran on forever while the gate believed it had
// stopped — and the peer kept seeing us connected. Returns how many it killed.
func killSyncthingByHome(home string) int {
	marker := "--home=" + home
	return killMatchingProcs("home "+home, func(cmdline string) bool {
		return strings.Contains(cmdline, marker)
	})
}

// killMatchingProcs scans /proc for processes whose cmdline satisfies match and
// terminates them: SIGTERM first so Syncthing closes its peer connections
// cleanly (the peer sees us drop promptly), then SIGKILL for any that linger.
// Linux/Android only; a sandboxed app's /proc exposes only its own UID's
// processes, so this can't touch anything but our own Syncthing. No pkill
// dependency — that's unreliable/absent for a sandboxed Android app.
func killMatchingProcs(label string, match func(cmdline string) bool) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // not a /proc/<pid> dir
		}
		data, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil {
			continue // process gone / unreadable
		}
		// cmdline args are NUL-separated.
		cmdline := strings.ReplaceAll(string(data), "\x00", " ")
		if match(cmdline) {
			pids = append(pids, pid)
		}
	}
	if len(pids) == 0 {
		return 0
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	time.Sleep(750 * time.Millisecond)
	for _, pid := range pids {
		if syscall.Kill(pid, 0) == nil { // signal 0 = liveness probe
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	log.Printf("stmanager: terminated %d Syncthing process(es) matching %s", len(pids), label)
	return len(pids)
}
