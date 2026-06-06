//go:build !windows

package api

import (
	"log"
	"net/http"
	"os"
	"syscall"
)

// Exit gracefully shuts this backend down so a newer build can take over the API
// port (the update/handover path — see reconcileExistingBackend in main.go). It
// raises SIGTERM on its own process, which main's signal.NotifyContext turns into
// a normal context cancel — Syncthing is stopped and the wire closed exactly as
// on Ctrl+C. POST-only; the API only ever binds 127.0.0.1, so this isn't
// reachable off-host.
func (h *Handlers) Exit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	log.Printf("api: /api/exit — shutting down to hand over to a newer build")
	w.WriteHeader(http.StatusNoContent)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	// Signal is queued; the handler returns, the response is delivered, then the
	// signal handler cancels the root context and graceful shutdown proceeds.
	_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
}
