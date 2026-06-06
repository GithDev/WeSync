package main

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// newProxy returns an http.Handler that backs the Wails asset server at
// wails.localhost. It only matters during startup: it shows a loading page
// until the backend is up, after which app.startup navigates the WebView
// straight to http://localhost:47820 (the backend) — so REST and the WebSocket
// are same-origin with the backend, exactly like a browser. That sidesteps two
// WebKitGTK limits the wails.localhost path hits: the production asset server
// does not tunnel WebSockets, and a cross-origin ws:// from the wails.localhost
// secure context is blocked. proxyWebSocket below is kept only for completeness.
func newProxy(app *App, targetURL string) http.Handler {
	target, _ := url.Parse(targetURL)
	httpProxy := httputil.NewSingleHostReverseProxy(target)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Show loading page while backend is initialising.
		if !isBackendReady() {
			serveLoadingPage(w, r)
			return
		}
		// Intercept folder picker — use native OS dialog, no PowerShell window.
		if r.URL.Path == "/api/folder/pick" && r.Method == http.MethodGet {
			ctx := app.ctx
			if ctx == nil {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
			dir, err := wailsRuntime.OpenDirectoryDialog(ctx, wailsRuntime.OpenDialogOptions{
				Title: "Select folder to share via WeSync",
			})
			if err != nil || dir == "" {
				http.Error(w, "cancelled", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"path": dir}) //nolint:errcheck
			return
		}

		// WebSocket upgrade — TCP tunnel to backend.
		if isWebSocketUpgrade(r) {
			proxyWebSocket(w, r, target.Host)
			return
		}

		httpProxy.ServeHTTP(w, r)
	})
}

func serveLoadingPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!DOCTYPE html><html><head><meta charset="UTF-8"><title>WeSync</title>` + //nolint:errcheck
		`<style>body{margin:0;display:flex;align-items:center;justify-content:center;height:100vh;` +
		`background:#f8fafc;font-family:system-ui,sans-serif;}` +
		`.d{animation:p 1.2s ease-in-out infinite;}@keyframes p{0%,100%{opacity:.3}50%{opacity:1}}</style></head>` +
		`<body><p style="color:#94a3b8;font-size:14px"><span class="d">●</span>&nbsp;Starting WeSync…</p></body></html>`))
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func proxyWebSocket(w http.ResponseWriter, r *http.Request, backendHost string) {
	backend, err := net.Dial("tcp", backendHost)
	if err != nil {
		http.Error(w, "backend unavailable", http.StatusBadGateway)
		return
	}
	defer backend.Close()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		log.Printf("ws proxy: hijack: %v", err)
		return
	}
	defer client.Close()

	if err := r.Write(backend); err != nil {
		log.Printf("ws proxy: write request: %v", err)
		return
	}

	done := make(chan struct{}, 2)
	go func() { io.Copy(backend, client); done <- struct{}{} }() //nolint:errcheck
	go func() { io.Copy(client, backend); done <- struct{}{} }() //nolint:errcheck
	<-done
}
