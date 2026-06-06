package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	"wesync/internal/api"
	"wesync/internal/backend"
	"wesync/internal/stmanager"
)

func main() {
	// --syncthing-service: run only the embedded Syncthing subprocess as a
	// Windows Service (SYSTEM account). WeSync API is NOT started.
	for _, arg := range os.Args[1:] {
		if arg == "--syncthing-service" {
			runSyncthingService()
			return
		}
	}

	if isWindowsService() {
		runAsService(run)
		return
	}
	run()
}

// runSyncthingService starts Syncthing and blocks until stopped.
// Called when wesync.exe is registered as the Syncthing Windows Service.
func runSyncthingService() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	stExe, err := stmanager.FindSyncthing()
	if err != nil {
		stExe, err = stmanager.ExtractEmbedded(syncthingBin)
		if err != nil {
			log.Fatalf("syncthing unavailable: %v", err)
		}
	}
	if err := stmanager.EnsureReady(stExe); err != nil {
		log.Fatalf("syncthing setup: %v", err)
	}
	if err := stmanager.Start(stExe); err != nil {
		log.Fatalf("syncthing start: %v", err)
	}
	<-ctx.Done()
	stmanager.Stop() //nolint:errcheck
}

func run() {
	// Log to both file and stdout so terminal windows show output.
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	if logFile, err := openLogFile(); err == nil {
		log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	}

	// Flags take precedence over env vars, env vars take precedence over defaults.
	//
	// Env vars:
	//   WESYNC_PORT           — HTTP API port, localhost only (default 47820)
	//   WESYNC_PEER_PORT      — WSS peer port, LAN-accessible  (default 47821)
	//   WESYNC_DB             — SQLite database path
	//   WESYNC_DEBUG          — debug mode (true/false)
	//   WESYNC_SYNCTHING_URL  — external Syncthing API URL
	//   WESYNC_SYNCTHING_KEY  — external Syncthing API key
	stURL    := flag.String("syncthing-url",  env("WESYNC_SYNCTHING_URL",  ""), "Syncthing API URL")
	stKey    := flag.String("syncthing-key",  env("WESYNC_SYNCTHING_KEY",  ""), "Syncthing API key")
	stHome   := flag.String("syncthing-home", env("WESYNC_SYNCTHING_HOME", ""), "Syncthing home dir (for TLS cert)")
	dbPath   := flag.String("db", env("WESYNC_DB", ""), "SQLite database path")
	apiPort  := flag.Int("port", envInt("WESYNC_PORT", 47820), "WeSync HTTP API port (localhost only)")
	peerPort := flag.Int("peer-port", envInt("WESYNC_PEER_PORT", 47821), "WeSync WSS peer port (LAN)")
	debug    := flag.Bool("debug", envBool("WESYNC_DEBUG"), "Enable debug mode")
	flag.Parse()

	// Reconcile with any backend already holding our API port. If it's the SAME
	// build, defer to it (a healthy instance is already running). If it's a
	// DIFFERENT build — e.g. we were just updated and the old one is still running
	// (its files were swapped under it; common after a flatpak update) — ask it to
	// exit and take over. This is what makes an update actually take effect without
	// a reboot/relogin. (On Windows the installer + mutex handle this instead.)
	if !reconcileExistingBackend(*apiPort) {
		return
	}

	// Prevent multiple instances racing to bind the same port.
	// Mutex is port-scoped so dev setups with multiple instances on different
	// ports work correctly.
	acquired, release := acquireSingleInstanceLock(*apiPort)
	if !acquired {
		return
	}
	defer release()

	if *dbPath == "" {
		p, err := stmanager.DBPath()
		if err != nil {
			log.Fatalf("resolve db path: %v", err)
		}
		dbPath = &p
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *stKey == "" {
		stExe, err := stmanager.FindSyncthing()
		if err != nil {
			stExe, err = stmanager.ExtractEmbedded(syncthingBin)
			if err != nil {
				log.Fatalf("syncthing unavailable: %v", err)
			}
		}
		if err := stmanager.EnsureReady(stExe); err != nil {
			log.Fatalf("prepare syncthing: %v", err)
		}
		if err := stmanager.Start(stExe); err != nil {
			log.Fatalf("start syncthing: %v", err)
		}
		defer stmanager.Stop() //nolint:errcheck — best-effort on exit
		if *stHome == "" {
			*stHome = filepath.Join(stmanager.DataDir(), "syncthing")
		}
	}

	dist, err := fs.Sub(staticFiles, "web/dist")
	if err != nil {
		log.Fatalf("static files: %v", err)
	}

	if err := backend.Run(ctx, backend.Options{
		APIPort:  *apiPort,
		PeerPort: *peerPort,
		DBPath:   *dbPath,
		Debug:    *debug,
		STKey:    *stKey,
		STURL:    *stURL,
		STHome:   *stHome,
		StaticFS: dist,
	}); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// openLogFile opens (or creates) wesync.log in the data directory.
// Called by run() so logs are always written to disk, even with -H windowsgui.
func openLogFile() (*os.File, error) {
	dir := stmanager.DataDir()
	os.MkdirAll(dir, 0700) //nolint:errcheck
	return os.OpenFile(filepath.Join(dir, "wesync.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
}

// reconcileExistingBackend decides whether to start, given any WeSync backend
// already on the API port. Returns true to proceed (port is free, or we just
// took over from a stale build), false to defer (a same-build instance is
// already healthy, or a stale one wouldn't step aside in time).
func reconcileExistingBackend(port int) bool {
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	running, err := fetchBuildTime(base)
	if err != nil {
		return true // nothing reachable on the port — go ahead
	}
	if running == api.BuildTime {
		log.Printf("WeSync already running (build %s) — deferring to it", running)
		return false
	}
	log.Printf("replacing stale backend on :%d (running %q, ours %q)", port, running, api.BuildTime)
	req, _ := http.NewRequest(http.MethodPost, base+"/api/exit", nil)
	if resp, e := (&http.Client{Timeout: 3 * time.Second}).Do(req); e == nil {
		resp.Body.Close()
	}
	// Wait for it to release the port (graceful shutdown stops Syncthing too).
	for i := 0; i < 100; i++ {
		c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if e != nil {
			return true // port free — take over
		}
		c.Close() //nolint:errcheck
		time.Sleep(100 * time.Millisecond)
	}
	log.Printf("stale backend did not exit in time — not starting a second instance")
	return false
}

// fetchBuildTime reads the running backend's build stamp from GET /api/status.
func fetchBuildTime(base string) (string, error) {
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(base + "/api/status")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var s struct {
		BuildTime string `json:"buildTime"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return "", err
	}
	return s.BuildTime, nil
}

// ── env helpers ───────────────────────────────────────────────────────────────

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "1" || v == "true" || v == "yes"
}
