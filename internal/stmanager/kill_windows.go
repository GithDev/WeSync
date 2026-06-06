//go:build windows

package stmanager

import (
	"fmt"
	"log"
	"os/exec"
)

// killByPath kills the process running from the given executable path.
// Only processes matching this exact path are terminated — other Syncthing
// instances elsewhere on the system are unaffected.
func killByPath(exePath string) {
	script := fmt.Sprintf(
		`Get-Process syncthing -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq '%s' } | Stop-Process -Force`,
		exePath,
	)
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		log.Printf("stmanager: killByPath(%s): %v %s", exePath, err, out)
	} else {
		log.Printf("stmanager: killed stale Syncthing at %s", exePath)
	}
}

// killSyncthingByHome is a no-op on Windows: the gate's start/stop churn (and
// thus the orphaned-then-adopted Syncthing this sweeps up) is an Android-only
// power-save scenario — desktop runs Syncthing continuously and always holds its
// own process handle, so Stop()'s handle-based kill already suffices. Defined
// only so the cross-platform Stop() compiles everywhere.
func killSyncthingByHome(_ string) int { return 0 }
