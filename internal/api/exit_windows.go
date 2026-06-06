//go:build windows

package api

import "net/http"

// Exit is a no-op handover hook on Windows. The NSIS installer stops the old
// processes before upgrading and a named mutex enforces single-instance, so
// runtime self-termination for handover isn't needed — and Go can't deliver
// SIGTERM to its own process on Windows anyway.
func (h *Handlers) Exit(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "handover not supported on windows", http.StatusNotImplemented)
}
