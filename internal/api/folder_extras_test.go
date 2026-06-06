package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"wesync/internal/syncthing"
)

// ── conflictOriginal ──────────────────────────────────────────────────────────

func TestConflictOriginal_StripsConflictSuffix(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"photo.sync-conflict-20240101-120000-ABCDEFG.jpg", "photo.jpg"},
		{"document.sync-conflict-20240101-120000-ABCDEFG.pdf", "document.pdf"},
		{"file.sync-conflict-20240101-120000-ABCDEFG.txt", "file.txt"},
		{"noextension.sync-conflict-20240101-120000-ABCDEFG", "noextension"},
		{"normal.txt", "normal.txt"}, // not a conflict file — unchanged
		// Dotted basename: ST inserts the marker before the final extension, so
		// the original is reconstructed without a doubled extension.
		{"archive.tar.sync-conflict-20240101-120000-ABCDEFG.gz", "archive.tar.gz"},
	}
	for _, c := range cases {
		got := conflictOriginal(c.input)
		if got != c.want {
			t.Errorf("conflictOriginal(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestConflictOriginal_NestedName(t *testing.T) {
	// Only the basename is passed to conflictOriginal, not the full path.
	got := conflictOriginal("report.sync-conflict-20240101-120000-ABCDEFG.docx")
	if got != "report.docx" {
		t.Errorf("got %q, want %q", got, "report.docx")
	}
}

// ── scanConflicts ─────────────────────────────────────────────────────────────

func TestScanConflicts_FindsConflictFiles(t *testing.T) {
	dir := t.TempDir()

	// Create a conflict file and a normal file
	conflict := filepath.Join(dir, "photo.sync-conflict-20240101-120000-ABCDEFG.jpg")
	normal := filepath.Join(dir, "photo.jpg")
	os.WriteFile(conflict, []byte("data"), 0644)
	os.WriteFile(normal, []byte("data"), 0644)

	results, err := scanConflicts(dir)
	if err != nil {
		t.Fatalf("scanConflicts: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(results))
	}
	if results[0].Path != "photo.sync-conflict-20240101-120000-ABCDEFG.jpg" {
		t.Errorf("unexpected path: %q", results[0].Path)
	}
	if results[0].OriginalPath != "photo.jpg" {
		t.Errorf("unexpected originalPath: %q", results[0].OriginalPath)
	}
}

func TestScanConflicts_ReturnsEmptySlice_WhenNone(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "normal.txt"), []byte("data"), 0644)

	results, err := scanConflicts(dir)
	if err != nil {
		t.Fatalf("scanConflicts: %v", err)
	}
	if results == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(results) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(results))
	}
}

func TestScanConflicts_FindsNestedConflicts(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	os.MkdirAll(sub, 0755)
	os.WriteFile(filepath.Join(sub, "doc.sync-conflict-20240101-120000-ABCDEFG.txt"), []byte("x"), 0644)

	results, err := scanConflicts(dir)
	if err != nil {
		t.Fatalf("scanConflicts: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(results))
	}
	if results[0].Path != "subdir/doc.sync-conflict-20240101-120000-ABCDEFG.txt" {
		t.Errorf("unexpected path: %q", results[0].Path)
	}
	if results[0].OriginalPath != "subdir/doc.txt" {
		t.Errorf("unexpected originalPath: %q", results[0].OriginalPath)
	}
}

// ── HTTP: FolderIgnoresGet / FolderIgnoresSet ─────────────────────────────────

func TestFolderIgnores_GetReturnsPatterns(t *testing.T) {
	a, _ := setup(t)
	a.st.AddFolder(syncthing.Folder{ID: "f1", Label: "Test", Path: "/test", Type: "sendreceive", Devices: []syncthing.FolderDevice{{DeviceID: a.handlers.selfID}}}) //nolint:errcheck

	a.st.setIgnorePatterns("f1", []string{"*.tmp", ".DS_Store"})

	req, _ := http.NewRequest(http.MethodGet, a.srv.URL+"/api/folder/ignores?id=f1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET ignores: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body struct {
		Patterns []string `json:"patterns"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Patterns) != 2 {
		t.Errorf("expected 2 patterns, got %v", body.Patterns)
	}
}

func TestFolderIgnores_SetAndGet(t *testing.T) {
	a, _ := setup(t)
	a.st.AddFolder(syncthing.Folder{ID: "f1", Label: "Test", Path: "/test", Type: "sendreceive", Devices: []syncthing.FolderDevice{{DeviceID: a.handlers.selfID}}}) //nolint:errcheck

	payload, _ := json.Marshal(map[string]any{"patterns": []string{"*.log", "tmp/"}})
	resp, err := http.Post(a.srv.URL+"/api/folder/ignores?id=f1", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST ignores: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	got, err := a.st.GetFolderIgnores("f1")
	if err != nil {
		t.Fatalf("GetFolderIgnores: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 patterns, got %v", got)
	}
}

func TestFolderIgnores_MissingID_Returns400(t *testing.T) {
	a, _ := setup(t)

	req, _ := http.NewRequest(http.MethodGet, a.srv.URL+"/api/folder/ignores", nil)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

// ── HTTP: FolderConflictDelete ────────────────────────────────────────────────

func TestFolderConflictDelete_RemovesFile(t *testing.T) {
	a, _ := setup(t)
	dir := t.TempDir()
	a.st.AddFolder(syncthing.Folder{ID: "f1", Label: "Test", Path: dir, Type: "sendreceive", Devices: []syncthing.FolderDevice{{DeviceID: a.handlers.selfID}}}) //nolint:errcheck

	conflictFile := filepath.Join(dir, "photo.sync-conflict-20240101-120000-ABCDEFG.jpg")
	os.WriteFile(conflictFile, []byte("data"), 0644)

	req, _ := http.NewRequest(http.MethodDelete,
		a.srv.URL+"/api/folder/conflict?id=f1&path=photo.sync-conflict-20240101-120000-ABCDEFG.jpg", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE conflict: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if _, err := os.Stat(conflictFile); !os.IsNotExist(err) {
		t.Error("expected conflict file to be deleted")
	}
}

func TestFolderConflictDelete_RejectsPathTraversal(t *testing.T) {
	a, _ := setup(t)
	dir := t.TempDir()
	a.st.AddFolder(syncthing.Folder{ID: "f1", Label: "Test", Path: dir, Type: "sendreceive", Devices: []syncthing.FolderDevice{{DeviceID: a.handlers.selfID}}}) //nolint:errcheck

	req, _ := http.NewRequest(http.MethodDelete,
		a.srv.URL+"/api/folder/conflict?id=f1&path=../../etc/passwd", nil)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for path traversal, got %d", resp.StatusCode)
	}
}

func TestFolderConflictDelete_RejectsNonConflictFile(t *testing.T) {
	a, _ := setup(t)
	dir := t.TempDir()
	a.st.AddFolder(syncthing.Folder{ID: "f1", Label: "Test", Path: dir, Type: "sendreceive", Devices: []syncthing.FolderDevice{{DeviceID: a.handlers.selfID}}}) //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "normal.txt"), []byte("data"), 0644)

	req, _ := http.NewRequest(http.MethodDelete,
		a.srv.URL+"/api/folder/conflict?id=f1&path=normal.txt", nil)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for non-conflict file, got %d", resp.StatusCode)
	}
}
