package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"wesync/internal/syncthing"
)

// ── Ignore patterns ────────────────────────────────────────────────────────────

// FolderIgnoresHandler routes GET/POST for /api/folder/ignores.
func (h *Handlers) FolderIgnoresHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.FolderIgnoresGet(w, r)
	case http.MethodPost:
		h.FolderIgnoresSet(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// FolderIgnoresGet returns the current ignore patterns for a folder.
// GET /api/folder/ignores?id=X
func (h *Handlers) FolderIgnoresGet(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	folderID := r.URL.Query().Get("id")
	if folderID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	patterns, err := h.st.GetFolderIgnores(folderID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string][]string{"patterns": patterns})
}

// FolderIgnoresSet replaces the ignore patterns for a folder.
// POST /api/folder/ignores?id=X   body: {patterns: [...]}
func (h *Handlers) FolderIgnoresSet(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	folderID := r.URL.Query().Get("id")
	if folderID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	var req struct {
		Patterns []string `json:"patterns"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := h.st.SetFolderIgnores(folderID, req.Patterns); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Conflict files ─────────────────────────────────────────────────────────────

// FolderConflictsList scans the folder path for Syncthing conflict copies.
// GET /api/folder/conflicts?id=X
func (h *Handlers) FolderConflictsList(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	folderID := r.URL.Query().Get("id")
	if folderID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	f := folderByID(h.listFolders(), folderID)
	if f == nil {
		http.Error(w, "folder not found", http.StatusNotFound)
		return
	}
	conflicts, err := scanConflicts(f.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, conflicts)
}

// FolderConflictDelete deletes a specific conflict file.
// DELETE /api/folder/conflict?id=X&path=relative/path
func (h *Handlers) FolderConflictDelete(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	folderID := r.URL.Query().Get("id")
	conflictPath := r.URL.Query().Get("path")
	if folderID == "" || conflictPath == "" {
		http.Error(w, "missing id or path", http.StatusBadRequest)
		return
	}
	f := folderByID(h.listFolders(), folderID)
	if f == nil {
		http.Error(w, "folder not found", http.StatusNotFound)
		return
	}
	// Security: the resolved path must stay INSIDE the folder. A bare
	// HasPrefix(abs, base) is wrong — it lets a sibling dir whose name shares the
	// prefix through (e.g. base "/data/folder" would accept "/data/folderEVIL").
	// Require an exact match or a base+separator prefix, which also rejects any
	// "../" traversal in the attacker-supplied conflictPath.
	base := filepath.Clean(f.Path)
	abs := filepath.Clean(filepath.Join(base, conflictPath))
	if abs != base && !strings.HasPrefix(abs, base+string(os.PathSeparator)) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if !strings.Contains(filepath.Base(abs), ".sync-conflict-") {
		http.Error(w, "not a conflict file", http.StatusBadRequest)
		return
	}
	if err := os.Remove(abs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func scanConflicts(folderPath string) ([]syncthing.ConflictFile, error) {
	var results []syncthing.ConflictFile
	err := filepath.Walk(folderPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		name := filepath.Base(path)
		if !strings.Contains(name, ".sync-conflict-") {
			return nil
		}
		rel, _ := filepath.Rel(folderPath, path)
		results = append(results, syncthing.ConflictFile{
			Path:         filepath.ToSlash(rel),
			OriginalPath: filepath.ToSlash(filepath.Join(filepath.Dir(rel), conflictOriginal(name))),
		})
		return nil
	})
	if results == nil {
		results = []syncthing.ConflictFile{}
	}
	return results, err
}

// conflictOriginal strips .sync-conflict-DATE-TIME-DEVICEID[.ext] from a filename.
// Format: BASENAME.sync-conflict-DATE-TIME-DEVICEID.EXT or BASENAME.sync-conflict-DATE-TIME-DEVICEID
func conflictOriginal(name string) string {
	idx := strings.Index(name, ".sync-conflict-")
	if idx < 0 {
		return name
	}
	base := name[:idx]
	// The part after the conflict marker is DATE-TIME-DEVICEID, optionally followed by .ext
	after := name[idx+len(".sync-conflict-"):]
	if dotIdx := strings.LastIndex(after, "."); dotIdx >= 0 {
		return base + after[dotIdx:]
	}
	return base
}
