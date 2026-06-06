//go:build windows

package stmanager

import (
	"os"
	"path/filepath"
)

// DataDir returns the WeSync data directory — always next to the exe.
//
// When installed to %LOCALAPPDATA%\WeSync\ (the default), this returns
// %LOCALAPPDATA%\WeSync\data\ which is naturally private to the user.
// No ACL manipulation needed.
//
// Override with the WESYNC_DB environment variable for server/advanced use.
func DataDir() string {
	if DataDirOverride != "" {
		return DataDirOverride
	}
	exe, err := os.Executable()
	if err != nil {
		// Fallback: %LOCALAPPDATA%\WeSync
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = os.Getenv("USERPROFILE") + `\AppData\Local`
		}
		return filepath.Join(local, "WeSync", "data")
	}
	return filepath.Join(filepath.Dir(exe), "data")
}
