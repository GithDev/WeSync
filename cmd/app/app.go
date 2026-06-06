package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Suppress unused import warning — runtime used for GOOS check in loadIcon.
var _ = runtime.GOOS

type App struct {
	ctx      context.Context
	cancel   context.CancelFunc
	quitting atomic.Bool // set when the user chose Quit, so beforeClose allows the close instead of hiding
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	_, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	loadIcon()

	log.Printf("app: checking backend on :%d", backendPort)

	if isBackendReady() {
		log.Printf("app: backend already running — loading UI from backend")
		loadUIFromBackend(ctx)
		return
	}

	exe, _ := os.Executable()
	svcExe := filepath.Join(filepath.Dir(exe), svcBinaryName())
	log.Printf("app: backend not ready, starting %s", svcExe)

	if _, err := os.Stat(svcExe); err != nil {
		log.Printf("app: ERROR — %s not found: %v", svcExe, err)
		return
	}

	cmd := exec.Command(svcExe)
	if err := cmd.Start(); err != nil {
		log.Printf("app: ERROR starting backend: %v", err)
		return
	}
	log.Printf("app: backend started (pid %d)", cmd.Process.Pid)
	go func() { cmd.Wait() }() //nolint:errcheck

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if isBackendReady() {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !isBackendReady() {
		log.Printf("app: ERROR — backend did not respond within 30s")
		wailsRuntime.LogWarningf(ctx, "backend did not start in time — check wesync.log")
		return
	}

	log.Printf("app: backend ready — loading UI from backend")
	loadUIFromBackend(ctx)
}

// loadUIFromBackend points the webview at the backend's HTTP server — Linux ONLY.
// On Linux the backend serves the web UI and the GUI is a thin webview aimed at
// it. On Windows/macOS the GUI embeds the web assets directly (Wails) and the
// frontend connects to the backend's API/WS on its own; navigating away from that
// embedded context there drops the Wails JS bridge and loads the wrong page —
// which is what made discovery + the visibility button disappear on Windows. So
// outside Linux we leave the embedded UI in place and do nothing here.
func loadUIFromBackend(ctx context.Context) {
	if runtime.GOOS != "linux" {
		return
	}
	wailsRuntime.WindowExecJS(ctx, `window.location.replace("http://localhost:47820")`)
}

func (a *App) shutdown(_ context.Context) {
	if a.cancel != nil {
		a.cancel()
	}
}

// beforeClose runs when the user clicks the window's close button OR when we
// call runtime.Quit. For the close button we hide to tray (the app keeps running
// for background sync) and tell the backend to go silent immediately — kill wire
// + UDP, no grace — returning true to prevent the real close. For an actual Quit
// (tray menu, which sets quitting first) we return false to let it proceed.
func (a *App) beforeClose(ctx context.Context) bool {
	if a.quitting.Load() {
		return false // real quit — allow it
	}
	go a.notifyActive(false)
	wailsRuntime.WindowHide(ctx)
	return true
}

// notifyActive tells the (separate) backend process that the UI just became
// visible (true) or hidden (false). Best-effort: a failed call just leaves the
// backend in its current state, and the UI-WebSocket backstop still applies.
func (a *App) notifyActive(active bool) {
	body, _ := json.Marshal(map[string]bool{"active": active})
	req, err := http.NewRequest(http.MethodPut, backendURL+"/api/active", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("app: notifyActive(%v): %v", active, err)
		return
	}
	resp.Body.Close()
}

// PickFolder opens a native directory picker dialog and returns the selected path.
// Called from the frontend via the Wails bridge (window.go.main.App.PickFolder).
// Returns an empty string if cancelled.
func (a *App) PickFolder() string {
	if a.ctx == nil {
		return ""
	}
	dir, _ := wailsRuntime.OpenDirectoryDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Select folder to share via WeSync",
	})
	return dir
}

func (a *App) showWindow() {
	if a.ctx != nil {
		wailsRuntime.WindowShow(a.ctx)
		go a.notifyActive(true)
	}
}

func (a *App) quit() {
	a.quitting.Store(true) // so beforeClose lets the close through instead of hiding
	if a.cancel != nil {
		a.cancel()
	}
	if a.ctx != nil {
		wailsRuntime.Quit(a.ctx)
	}
}

func isBackendReady() bool {
	conn, err := net.DialTimeout("tcp", backendAddr, time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func loadIcon() {
	// iconBytes is already populated via //go:embed build/windows/icon.ico on Windows.
	// Nothing to load at runtime.
}

func svcBinaryName() string {
	if runtime.GOOS == "windows" {
		return "wesync.exe"
	}
	return "wesync"
}
