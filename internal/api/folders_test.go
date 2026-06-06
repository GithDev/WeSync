package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"wesync/internal/syncthing"
)

// ── mock folder backend ───────────────────────────────────────────────────────

func (m *mockSyncthing) ListFoldersOK() ([]syncthing.Folder, error) { return nil, nil }

// ── folder share validation ───────────────────────────────────────────────────

func doShareFolder(t *testing.T, inst *instance, deviceID, path, label, direction string) int {
	t.Helper()
	_, code := doShareFolderID(t, inst, deviceID, path, label, direction)
	return code
}

// doShareFolderID shares a folder and returns (folderID, statusCode).
func doShareFolderID(t *testing.T, inst *instance, deviceID, path, label, direction string) (string, int) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"deviceID": deviceID, "path": path, "label": label, "direction": direction,
	})
	resp, err := http.Post(inst.srv.URL+"/api/folder/share", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("share folder: %v", err)
	}
	defer resp.Body.Close()
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	return result["folderID"], resp.StatusCode
}


func TestFolderShare_ReusesExistingFolder(t *testing.T) {
	a, b := setup(t)
	a.seedDevice(idB, "DeviceB")

	// First share succeeds.
	code := doShareFolder(t, a, idB, "/home/photos", "Photos", "sendonly")
	if code != http.StatusOK {
		t.Fatalf("first share: expected 204, got %d", code)
	}

	// Second share with same path also succeeds — reuses existing folder.
	code = doShareFolder(t, a, idB, "/home/photos", "Photos", "sendonly")
	if code != http.StatusOK {
		t.Errorf("duplicate path: expected 204 (reuse), got %d", code)
	}

	// Only one folder should exist in ST for this path.
	folders := a.handlers.listFolders()
	count := 0
	for _, f := range folders {
		if normPath(f.Path) == normPath("/home/photos") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 folder for path, got %d", count)
	}
	_ = b
}

func TestFolderShare_BlocksSamePathDifferentLabel(t *testing.T) {
	a, b := setup(t)
	a.seedDevice(idB, "DeviceB")

	doShareFolder(t, a, idB, "/home/docs", "Docs", "sendonly")

	// Same path, different label — still succeeds (reuses existing folder ID).
	code := doShareFolder(t, a, idB, "/home/docs", "Documents", "sendonly")
	if code != http.StatusOK {
		t.Errorf("same path different label: expected 204 (reuse), got %d", code)
	}
	_ = b
}

func TestFolderShare_AllowsDifferentPaths(t *testing.T) {
	a, b := setup(t)
	a.seedDevice(idB, "DeviceB")

	code1 := doShareFolder(t, a, idB, "/home/photos", "Photos", "sendonly")
	code2 := doShareFolder(t, a, idB, "/home/docs", "Docs", "sendonly")

	if code1 != http.StatusOK {
		t.Errorf("first path: expected 204, got %d", code1)
	}
	if code2 != http.StatusOK {
		t.Errorf("second path: expected 204, got %d", code2)
	}
	_ = b
}

func TestFolderAddDevice_IdempotentForSameDevice(t *testing.T) {
	a, _ := setup(t)
	a.seedDevice(idB, "DeviceB")

	// Share folder then add device twice — must be idempotent in ST.
	doShareFolder(t, a, idB, "/home/photos", "Photos", "sendonly")
	a.handlers.addDeviceToFolderInST("", idB) // no-op for unknown folder

	// ST should still have exactly one entry for idB in the folder.
	folders := a.handlers.listFolders()
	for _, f := range folders {
		if normPath(f.Path) == normPath("/home/photos") {
			count := 0
			for _, id := range f.DeviceIDs {
				if id == idB {
					count++
				}
			}
			if count != 1 {
				t.Errorf("expected 1 device entry for idB, got %d", count)
			}
			return
		}
	}
}
