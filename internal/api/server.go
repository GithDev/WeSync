package api

import (
	"context"
	"crypto/tls"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"wesync/internal/ratelimit"
)

const maxRequestBody = 1 * 1024 * 1024 // 1 MB

// APIServer serves the web UI and REST API over plain HTTP on localhost only.
// No TLS — no cert warnings in browsers or Wails WebView.
type APIServer struct {
	port    int
	handler http.Handler
}

// PeerServer serves the /peer/ws endpoint over HTTPS/WSS on all interfaces.
// Uses TLS + cert-pinning so LAN peers can verify each other's identity.
type PeerServer struct {
	port    int
	tlsCert *tls.Certificate
	handler http.Handler
}

// NewServer returns both the API server and the peer server.
// Call Run on both in separate goroutines.
func NewServer(
	apiPort, peerPort int,
	h *Handlers,
	hub *Hub,
	static fs.FS,
	tlsCert *tls.Certificate,
) (*APIServer, *PeerServer) {
	// ── API mux (no /peer/ws) ─────────────────────────────────────────────────
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/api/status", h.Status)
	apiMux.HandleFunc("/api/exit", h.Exit)
	apiMux.HandleFunc("/api/mode", h.Mode)
	apiMux.HandleFunc("/api/active", h.Active)
	apiMux.HandleFunc("/api/peers", h.Peers)
	apiMux.HandleFunc("/api/devices", h.Devices)
	apiMux.HandleFunc("/api/pair", h.Pair)
	apiMux.HandleFunc("/api/incoming", h.Incoming)
	apiMux.HandleFunc("/api/sync", h.Sync)
	apiMux.HandleFunc("/api/name", h.Name)
	apiMux.HandleFunc("/api/connectivity", h.Connectivity)
	apiMux.HandleFunc("/api/connectivity-status", h.ConnectivityStatus)
	apiMux.HandleFunc("/api/power", h.Power)
	apiMux.HandleFunc("/api/power/events", h.PowerEvents)
	apiMux.HandleFunc("/api/power/sync-now", h.PowerSyncNow)
	apiMux.HandleFunc("/api/power/status", h.PowerStatus)
	apiMux.HandleFunc("/api/folder/pick", h.FolderPick)
	apiMux.HandleFunc("/api/folder/share", h.FolderShare)
	apiMux.HandleFunc("/api/folder/accept", h.FolderAccept)
	apiMux.HandleFunc("/api/folder/decline", h.FolderDecline)
	apiMux.HandleFunc("/api/folder/device", h.FolderRemoveDevice)
	apiMux.HandleFunc("/api/folder/direction", h.FolderUpdateDirection)
	apiMux.HandleFunc("/api/folder/label", h.FolderUpdateLabel)
	apiMux.HandleFunc("/api/folder/check", h.FolderCheckPath)
	apiMux.HandleFunc("/api/folder/fix-marker", h.FolderFixMarker)
	apiMux.HandleFunc("/api/folder/revert", h.FolderRevert)
	apiMux.HandleFunc("/api/folder/pause", h.FolderPause)
	apiMux.HandleFunc("/api/folder", h.FolderRemove)
	apiMux.HandleFunc("/api/folders", h.FolderList)
	apiMux.HandleFunc("/api/folders/pending", h.FolderPendingList)
	apiMux.HandleFunc("/api/folder/status", h.FolderStatus)
	apiMux.HandleFunc("/api/folder/ignores", h.FolderIgnoresHandler)
	apiMux.HandleFunc("/api/folder/conflicts", h.FolderConflictsList)
	apiMux.HandleFunc("/api/folder/conflict", h.FolderConflictDelete)
	apiMux.HandleFunc("/api/ws", hub.ServeWS(h))
	apiMux.Handle("/", spaHandler(static))

	pairLimiter := ratelimit.New(20, time.Minute)
	apiHandler := corsMiddleware(bodySizeMiddleware(rateLimitMiddleware(pairLimiter, "/api/pair", apiMux)))

	// ── Peer mux (only /peer/ws) ──────────────────────────────────────────────
	peerMux := http.NewServeMux()
	peerMux.HandleFunc("/peer/ws", h.wire.ServeWS)

	return &APIServer{port: apiPort, handler: apiHandler},
		&PeerServer{port: peerPort, tlsCert: tlsCert, handler: peerMux}
}

// Run starts the API server on 127.0.0.1 (HTTP, no TLS).
func (s *APIServer) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", s.port),
		Handler: s.handler,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx) //nolint:errcheck
	}()
	log.Printf("API listening on http://localhost:%d", s.port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Run starts the peer server on 0.0.0.0 (HTTPS/WSS with TLS, LAN-reachable).
func (s *PeerServer) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%d", s.port),
		Handler: s.handler,
	}
	if s.tlsCert != nil {
		srv.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{*s.tlsCert},
			// RequireAnyClientCert, not RequestClientCert: the wire's whole premise
			// is "identity IS the cert", so a peer MUST present one. Requesting
			// (but not requiring) let a certless client complete the handshake and
			// fall through to the Hello-claimed identity — a spoofable bypass. We
			// don't verify against a CA here (peers are self-signed; the cert is
			// pinned/derived in readInbound), we just require that one exists.
			ClientAuth: tls.RequireAnyClientCert,
			MinVersion: tls.VersionTLS12,
		}
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx) //nolint:errcheck
	}()
	log.Printf("Peer server listening on 0.0.0.0:%d (WSS)", s.port)
	if s.tlsCert != nil {
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			return err
		}
	} else {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
	}
	return nil
}

const noCache = "no-cache, no-store, must-revalidate"

func spaHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" && path != "index.html" {
			if _, err := fs.Stat(dist, path); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		w.Header().Set("Cache-Control", noCache)
		fileServer.ServeHTTP(w, r)
	})
}

func bodySizeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && isLocalOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func rateLimitMiddleware(limiter *ratelimit.Limiter, prefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, prefix) {
			ip := clientIP(r)
			if !limiter.Allow(ip) {
				log.Printf("rate limit: %s blocked on %s", ip, r.URL.Path)
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	// SplitHostPort handles IPv6 ("[::1]:54321") correctly; strings.Cut on ":"
	// would mis-key every IPv6 client to the same bucket.
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func isLocalOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	h := u.Hostname()
	// Accept localhost, loopback IPs, and *.localhost (covers wails://wails.localhost
	// used by the Wails WebView2 runtime on Windows).
	return h == "localhost" || h == "127.0.0.1" || h == "::1" ||
		strings.HasSuffix(h, ".localhost")
}
