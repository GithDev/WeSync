// Package backend contains the WeSync server startup logic, shared between
// the service binary (main.go) and the Wails desktop app (cmd/app).
package backend

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"
	"wesync/internal/api"
	"wesync/internal/certid"
	"wesync/internal/discovery"
	"wesync/internal/stmanager"
	"wesync/internal/store"
	"wesync/internal/syncthing"
	"wesync/internal/sysinfo"
)

// Options configures the WeSync backend.
type Options struct {
	// APIPort is the local HTTP port for the web UI and REST API (127.0.0.1 only, no TLS).
	APIPort int
	// PeerPort is the HTTPS/WSS port for incoming peerwire connections (0.0.0.0, TLS).
	PeerPort int
	DBPath   string
	Debug    bool
	STKey    string // empty = use managed Syncthing via stmanager
	STURL    string // empty = use managed Syncthing via stmanager
	STHome   string // Syncthing home directory; used to load ST's TLS cert for peerwire
	StaticFS fs.FS  // embedded web/dist
	// HostnameOverride replaces os.Hostname() for device naming and sysinfo.
	// Used on platforms where the kernel hostname is unhelpful (Android
	// reports "localhost"; the wrapper passes a real device name instead).
	HostnameOverride string
}

// runTicker runs fn on every interval until ctx is cancelled.
func runTicker(ctx context.Context, interval time.Duration, fn func()) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				fn()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Run starts the WeSync API server and blocks until ctx is cancelled.
// Syncthing must already be running before calling Run (stmanager.Start).
func Run(ctx context.Context, opts Options) error {
	stURL := opts.STURL
	if stURL == "" {
		stURL = stmanager.APIURL
	}
	stKey := opts.STKey
	if stKey == "" {
		key, err := stmanager.APIKey()
		if err != nil {
			return err
		}
		stKey = key
	}

	st := syncthing.NewClient(stURL, stKey)

	var (
		status syncthing.SystemStatus
		err    error
	)
	for attempt := 1; ; attempt++ {
		status, err = st.SystemStatus()
		if err == nil {
			break
		}
		if attempt >= 30 {
			return err
		}
		log.Printf("backend: waiting for Syncthing (attempt %d/30)…", attempt)
		time.Sleep(2 * time.Second)
	}
	log.Printf("backend: Syncthing OK — %s", status.MyID)

	if err := st.LockToLocalNetwork(); err != nil {
		return err
	}
	if opts.Debug {
		api.DebugWire = true
		if err := st.SetGUIEnabled(true); err != nil {
			log.Printf("backend: enable ST GUI: %v", err)
		}
	}

	db, err := store.Open(opts.DBPath)
	if err != nil {
		return err
	}

	// Propagate the host name override (if any) to sysinfo so DeviceInfo
	// reports the right hostname too.
	if opts.HostnameOverride != "" {
		sysinfo.HostnameOverride = opts.HostnameOverride
	}

	hostName := opts.HostnameOverride
	if hostName == "" {
		hostName, _ = os.Hostname() //nolint:errcheck — empty string is an acceptable fallback
	}

	name, err := db.GetName()
	// Re-derive the device name when:
	//   - it was never set (first launch), or
	//   - the saved value is the useless Android default "localhost" and
	//     we now have a real override to replace it with.
	if err != nil || name == "" || (name == "localhost" && opts.HostnameOverride != "") {
		name = hostName
		if err := db.SetName(name); err != nil {
			log.Printf("backend: set name: %v", err)
		}
		if err := st.UpdateDevice(status.MyID, name); err != nil {
			log.Printf("backend: set ST device name: %v", err)
		}
	}

	disc, err := discovery.NewService(opts.PeerPort)
	if err != nil {
		return err
	}

	// Peerwire MUST use Syncthing's own TLS cert so the wire identity (certFP →
	// device ID) IS the ST device ID. We never generate a substitute: a node
	// presenting a different identity on the wire than in ST is a security risk
	// and silently breaks peer matching. A missing ST cert is therefore fatal.
	if opts.STHome == "" {
		return fmt.Errorf("backend: --syncthing-home is required — peerwire needs Syncthing's cert")
	}
	stCertPath := filepath.Join(opts.STHome, "cert.pem")
	stKeyPath := filepath.Join(opts.STHome, "key.pem")
	tlsCert, err := certid.Load(stCertPath, stKeyPath)
	if err != nil {
		return fmt.Errorf("backend: cannot load Syncthing cert from %s: %w — refusing to start without ST identity", opts.STHome, err)
	}
	log.Printf("backend: using ST cert for peerwire (certFP tied to ST device ID)")

	stListenPort := st.GetListenPort()
	devInfo := sysinfo.Collect()

	hub := api.NewHub()
	handlers := api.NewHandlers(st, db, status.MyID, name, opts.PeerPort, stListenPort, devInfo, tlsCert, disc, hub)

	hub.OnActiveChange(func(hasClients bool) {
		// Foreground signal from the UI WebSocket count: any UI connect → foreground,
		// last disconnect → background. This fires on EVERY connect (see hub.go), so a
		// reopen / webview reload / post-timeout WS reconnect always brings the node
		// back — the robust recovery path. The desktop app ALSO drives the finer
		// hide-to-tray transition explicitly via PUT /api/active (a hidden webview
		// keeps its WS open and never trips this), which is additive, not a gate.
		if hasClients {
			log.Printf("backend: UI connected — discovery + wire on")
		} else {
			log.Printf("backend: UI disconnected — discovery + wire off")
		}
		handlers.SetForeground(hasClients)
	})

	// Apply saved connectivity level (defaults to 1 = LAN only).
	if err := st.SetConnectivityLevel(db.GetConnectivityLevel()); err != nil {
		log.Printf("backend: connectivity level: %v", err)
	}
	// Seed the persisted discoverability preference. Actual UDP announce stays
	// gated on foreground, so this only takes effect once the UI opens (the hub's
	// OnActiveChange → SetForeground(true)); background stays silent regardless.
	disc.SetWantAnnounce(db.GetVisible())
	handlers.SchedulePipeline()
	// No startup MaintainConnections: wire stays quiet until the UI opens
	// (SetActive(true) via the hub). Headless/background = no wire by design.

	apiSrv, peerSrv := api.NewServer(opts.APIPort, opts.PeerPort, handlers, hub, opts.StaticFS, tlsCert)

	go func() {
		if err := disc.Run(ctx); err != nil {
			log.Printf("backend: discovery: %v", err)
		}
	}()
	go st.WatchEvents(ctx, func() {
		handlers.SchedulePipeline()
	})
	// Bind these to ctx: disc.Run never closes Peers/PeerGone, so a bare
	// `for range` would block forever after shutdown and leak the goroutine on
	// every backend.Run (matters on the mobile lifecycle, which re-runs it).
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case peer := <-disc.Peers:
				handlers.TrackPeer(peer)
			}
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case sid := <-disc.PeerGone:
				handlers.DropPeer(sid)
			}
		}
	}()
	runTicker(ctx, 30*time.Second, handlers.SchedulePipeline)
	runTicker(ctx, 30*time.Second, handlers.MaintainConnections)

	log.Printf("backend: ready — http://localhost:%d  (peer WSS on :%d)", opts.APIPort, opts.PeerPort)

	// Run both servers concurrently; return when either exits.
	errCh := make(chan error, 2)
	go func() { errCh <- apiSrv.Run(ctx) }()
	go func() { errCh <- peerSrv.Run(ctx) }()
	return <-errCh
}
