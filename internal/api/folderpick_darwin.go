//go:build darwin

package api

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func pickFolder() (string, error) {
	out, err := exec.Command("osascript", "-e",
		`POSIX path of (choose folder with prompt "Select folder to share via WeSync")`).Output()
	if err != nil {
		// osascript exits non-zero when the user cancels and writes
		// "User canceled." to stderr. Translate that into empty-path no-error
		// so the frontend doesn't fall back to the manual path input modal.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && strings.Contains(string(exitErr.Stderr), "canceled") {
			return "", nil
		}
		return "", fmt.Errorf("folder picker: %w", err)
	}
	path := strings.TrimRight(strings.TrimSpace(string(out)), "/")
	return path, nil
}
