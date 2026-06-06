//go:build !windows

package stmanager

import (
	"os"
	"path/filepath"
)

// DataDir returns the WeSync data directory on Linux/macOS.
// Follows XDG Base Directory spec.
func DataDir() string {
	if DataDirOverride != "" {
		return DataDirOverride
	}
	if db := os.Getenv("WESYNC_DB"); db != "" {
		return filepath.Dir(db)
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return xdg + "/wesync"
	}
	home, _ := os.UserHomeDir()
	return home + "/.local/share/wesync"
}
