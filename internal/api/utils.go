package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"wesync/internal/store"
	"wesync/internal/uid"
)

// requireMethod writes a 405 and returns false when the method doesn't match.
// Usage: if !requireMethod(w, r, http.MethodPost) { return }
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

// writeJSON encodes v as JSON and writes it to w.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// decodeJSON decodes the JSON body of r into v.
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// ── folder slice helpers ──────────────────────────────────────────────────────

// folderByID returns the first folder with the given ID, or nil.
func folderByID(folders []store.FolderWithDevices, id string) *store.FolderWithDevices {
	for i := range folders {
		if folders[i].ID == id {
			return &folders[i]
		}
	}
	return nil
}

// folderParticipants returns the DeviceIDs for folderID, or nil if not found.
func folderParticipants(folders []store.FolderWithDevices, folderID string) []string {
	if f := folderByID(folders, folderID); f != nil {
		return f.DeviceIDs
	}
	return nil
}

// normPath normalises a file path for deduplication comparisons.
func normPath(p string) string {
	return strings.TrimRight(strings.ToLower(p), `/\`)
}

// lastPathComponent returns the final segment of a file path.
func lastPathComponent(path string) string {
	path = strings.TrimRight(path, `/\`)
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}

// newFolderID generates a UUID v4 folder ID, suitable for use in API routes.
func newFolderID() string { return uid.New() }
