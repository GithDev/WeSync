// Package mobile is the gomobile-bind entry point for Android (and later iOS).
// Java/Kotlin code calls Start() with the app's private storage path, then
// loads a WebView pointed at http://127.0.0.1:<APIPort()>.
//
// Constraints (gomobile-bind):
//   - Exported function signatures must use bind-compatible types:
//     bool, int, int64, float32/64, string, []byte, and interfaces.
//     No arbitrary structs or channels in the API surface.
//   - Don't take or return Go-internal types like context.Context across
//     the bind boundary. Manage lifecycle via package-level state instead.
//
// See docs/state-model.md for the broader architecture; this file is purely
// the platform glue.
package mobile

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"

	"wesync/internal/api"
	"wesync/internal/backend"
	"wesync/internal/stmanager"
)

// Register the /api/power/sync-now hook with the API package once at
// package init. The handler is registered at server-setup time, so the
// hook must be present before the first request — package init runs
// before mobile.Start so this is safe.
func init() {
	api.SyncNowHook = OpenSyncSession
	api.GateStatusHook = GateStatusJSON
	api.FoldersChangedHook = onFoldersChanged
}

const (
	// Fixed loopback ports — the WebView and any Android-side bridge are
	// expected to talk to 127.0.0.1 only. Peer-port is what the on-device
	// Syncthing/peerwire listens on for inbound LAN connections; 47821
	// matches the desktop default so cross-device pairing stays compatible.
	defaultAPIPort  = 47820
	defaultPeerPort = 47821
)

var (
	mu      sync.Mutex
	cancel  context.CancelFunc
	lastErr error
)

// Start launches the WeSync backend in a background goroutine. Returns
// immediately.
//
//   - dataDir: writable app-private path (Context.getFilesDir() on Android).
//     Houses wesync.db, syncthing home dir, and TLS certs.
//   - stExePath: absolute path to the bundled Syncthing binary. On Android
//     it's extracted into `applicationInfo.nativeLibraryDir` from jniLibs
//     (named libsyncthing.so so Android packages it and marks it +x).
//   - deviceName: user-visible host name. Android's kernel hostname is
//     "localhost", so the wrapper passes something sensible (the user-set
//     "Enhetens namn" from Settings, falling back to Build.MODEL).
//
// Idempotent: calling Start when already running returns nil without
// restarting. To restart, call Stop first.
func Start(dataDir string, stExePath string, deviceName string) error {
	mu.Lock()
	defer mu.Unlock()

	if cancel != nil {
		return nil
	}
	if dataDir == "" {
		return errors.New("mobile.Start: dataDir is required")
	}
	if stExePath == "" {
		return errors.New("mobile.Start: stExePath is required")
	}

	// Android's HOME/XDG concept is useless — root everything under the
	// private files dir we got. stmanager.home() uses DataDir() under the
	// hood, so this propagates to the Syncthing home as well
	// (<dataDir>/syncthing/).
	stmanager.DataDirOverride = dataDir
	// Point stmanager at the extracted libsyncthing.so so FindSyncthing()
	// doesn't try to look beside the (nonexistent) wesync executable.
	stmanager.SyncthingPathOverride = stExePath

	dbPath := filepath.Join(dataDir, "wesync.db")
	stHome := filepath.Join(dataDir, "syncthing")

	ctx, c := context.WithCancel(context.Background())
	cancel = c
	lastErr = nil

	opts := backend.Options{
		APIPort:          defaultAPIPort,
		PeerPort:         defaultPeerPort,
		DBPath:           dbPath,
		Debug:            false,
		STHome:           stHome, // unifies peerwire identity with ST device ID
		StaticFS:         staticFS(),
		HostnameOverride: deviceName,
	}

	// Run in a goroutine so Start() returns to Java promptly. Errors are
	// stashed in lastErr for retrieval via LastError().
	go func() {
		// First — generate Syncthing's config + keys if missing, then launch
		// the binary. backend.Run blocks on /rest/system/status until ST's
		// API responds, so the order matters.
		if err := stmanager.EnsureReady(stExePath); err != nil {
			log.Printf("mobile.Start: stmanager.EnsureReady: %v", err)
			mu.Lock()
			lastErr = err
			mu.Unlock()
			return
		}
		if err := stmanager.Start(stExePath); err != nil {
			log.Printf("mobile.Start: stmanager.Start: %v", err)
			mu.Lock()
			lastErr = err
			mu.Unlock()
			return
		}
		// Hand the same exe path to the power gate so it can restart ST
		// after a Stop. backend.Run blocks below; the gate runs alongside
		// it, driven by event calls from the Android wrapper.
		initGate(stExePath)
		if err := backend.Run(ctx, opts); err != nil {
			log.Printf("mobile.Start: backend.Run: %v", err)
			mu.Lock()
			lastErr = err
			mu.Unlock()
		}
		mu.Lock()
		cancel = nil
		mu.Unlock()
	}()

	return nil
}

// Stop signals the backend to shut down. Safe to call multiple times.
// Blocks until the cancel is delivered (the goroutine exit is asynchronous).
// Also stops the bundled Syncthing process so it doesn't leak.
func Stop() {
	mu.Lock()
	if cancel != nil {
		cancel()
		cancel = nil
	}
	mu.Unlock()
	// Strip the gate's authority to restart ST before we kill the process,
	// so a kick racing this teardown can't bring ST back up.
	g.markStopped()
	stmanager.Stop() //nolint:errcheck
}

// IsRunning reports whether the backend goroutine is currently active.
func IsRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return cancel != nil
}

// APIPort is the local HTTP port the WebView should load. Fixed; the value
// is exposed as a function (not a const) because gomobile-bind doesn't
// project Go constants to Java.
func APIPort() int {
	return defaultAPIPort
}

// LastError returns the most recent fatal error from the backend goroutine,
// or empty string if none. Polled by the Android wrapper to surface
// startup failures (e.g. Syncthing wasn't started or storage unwritable).
func LastError() string {
	mu.Lock()
	defer mu.Unlock()
	if lastErr == nil {
		return ""
	}
	return fmt.Sprintf("%v", lastErr)
}
