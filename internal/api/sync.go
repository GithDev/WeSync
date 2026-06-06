package api

import "net/http"

func (h *Handlers) Sync(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	h.SchedulePipeline()
	w.WriteHeader(http.StatusNoContent)
}
