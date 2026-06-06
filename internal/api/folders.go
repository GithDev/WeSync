package api

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"wesync/internal/peerwire"
	"wesync/internal/syncthing"
)

func (h *Handlers) FolderPick(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	path, err := pickFolder()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"path": path})
}

// FolderShare creates or updates a folder in Syncthing with the given device.
func (h *Handlers) FolderShare(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req struct {
		DeviceID  string `json:"deviceID"`
		Path      string `json:"path"`
		Label     string `json:"label"`
		Direction string `json:"direction"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Path == "" {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Label == "" {
		req.Label = lastPathComponent(req.Path)
	}
	if req.Direction == "" {
		req.Direction = "sendonly"
	}
	if req.DeviceID != "" && !h.isTrusted(req.DeviceID) {
		http.Error(w, "device not trusted", http.StatusForbidden)
		return
	}

	// Find existing folder by path or create new.
	stFolders, err := h.st.ListFolders()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var folderID string
	var existing *syncthing.Folder
	norm := normPath(req.Path)
	for i := range stFolders {
		if normPath(stFolders[i].Path) == norm {
			folderID = stFolders[i].ID
			existing = &stFolders[i]
			break
		}
	}

	if existing != nil {
		existing.Label = req.Label
		existing.Type = req.Direction
		newDevice := false
		if req.DeviceID != "" {
			found := false
			for _, d := range existing.Devices {
				if d.DeviceID == req.DeviceID {
					found = true
					break
				}
			}
			if !found {
				existing.Devices = append(existing.Devices, syncthing.FolderDevice{DeviceID: req.DeviceID})
				newDevice = true
				h.resetAccepted(folderID, req.DeviceID) // clear stale BEP/wire state for fresh invite
				dbg("folder: share %.8s (%s) → added device %s", folderID, req.Label, shortID(req.DeviceID))
			} else {
				dbg("folder: share %.8s (%s) → device %s already in folder", folderID, req.Label, shortID(req.DeviceID))
			}
		}
		if err := h.st.UpdateFolder(*existing); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if newDevice {
			// A new device was added to an existing folder. Pause/resume forces
			// a fresh BEP ClusterConfig exchange: with the new device so it learns
			// of the folder being offered (ST won't proactively re-send
			// ClusterConfig on a live BEP connection just because config changed),
			// and with the other participants so the Introducer mechanism
			// propagates the new member to everyone already in the folder.
			refresh := []string{req.DeviceID}
			for _, d := range existing.Devices {
				if d.DeviceID != h.selfID && d.DeviceID != req.DeviceID {
					refresh = append(refresh, d.DeviceID)
				}
			}
			go func() {
				for _, pid := range refresh {
					h.st.PauseDevice(pid)  //nolint:errcheck
					h.st.ResumeDevice(pid) //nolint:errcheck
				}
			}()
		}
	} else {
		folderID = newFolderID()
		sf := syncthing.Folder{
			ID: folderID, Label: req.Label, Path: req.Path, Type: req.Direction,
			Devices: []syncthing.FolderDevice{{DeviceID: h.selfID}},
		}
		if req.DeviceID != "" {
			sf.Devices = append(sf.Devices, syncthing.FolderDevice{DeviceID: req.DeviceID})
		}
		dbg("folder: create %.8s (%s) at %s, device=%s", folderID, req.Label, req.Path, shortID(req.DeviceID))
		if err := h.st.AddFolder(sf); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		// A brand-new folder starts at ST's default fsWatcherDelayS; let the
		// power gate re-apply the on-change debounce so it doesn't stay at the
		// default until the next app restart.
		foldersChanged()
		// Same reason as the update branch: force a fresh ClusterConfig to the
		// recipient so they see the freshly-created folder as a pending invite.
		if req.DeviceID != "" {
			did := req.DeviceID
			go func() {
				h.st.PauseDevice(did)  //nolint:errcheck
				h.st.ResumeDevice(did) //nolint:errcheck
			}()
		}
	}

	h.SchedulePush()
	writeJSON(w, map[string]string{"folderID": folderID})
}

// FolderAccept configures a pending Syncthing folder locally.
func (h *Handlers) FolderAccept(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req struct {
		FolderID  string `json:"folderID"`
		DeviceID  string `json:"deviceID"`
		Path      string `json:"path"`
		Direction string `json:"direction"`
	}
	if err := decodeJSON(r, &req); err != nil || req.FolderID == "" {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	dbg("folder: accept %.8s from %s at path=%s", req.FolderID, shortID(req.DeviceID), req.Path)
	// Collect all devices offering this folder (direct + co-offerers from ST pending).
	allOfferers := []string{req.DeviceID}
	for _, pf := range h.pendingFolders.list() {
		if pf.FolderID == req.FolderID && pf.DeviceID != req.DeviceID {
			allOfferers = append(allOfferers, pf.DeviceID)
		}
	}

	// Check if we already have this folder in ST.
	stFolders, err := h.st.ListFolders()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	alreadyHave := false
	for _, f := range stFolders {
		if f.ID == req.FolderID {
			alreadyHave = true
			break
		}
	}

	if alreadyHave {
		for _, did := range allOfferers {
			h.addDeviceToFolderInST(req.FolderID, did)
		}
	} else {
		if req.Path == "" {
			http.Error(w, "path required for new folder", http.StatusBadRequest)
			return
		}
		if err := h.assertPathNotInUse(req.Path); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		label := req.FolderID
		for _, pf := range h.pendingFolders.list() {
			if pf.FolderID == req.FolderID {
				label = pf.Label
				break
			}
		}
		direction := req.Direction
		switch direction {
		case "sendonly", "receiveonly", "sendreceive":
		default:
			direction = "sendreceive"
		}
		sf := syncthing.Folder{
			ID: req.FolderID, Label: label, Path: req.Path, Type: direction,
			Devices: []syncthing.FolderDevice{{DeviceID: h.selfID}},
		}
		for _, did := range allOfferers {
			sf.Devices = append(sf.Devices, syncthing.FolderDevice{DeviceID: did})
		}
		if err := h.st.AddFolder(sf); err != nil {
			http.Error(w, fmt.Sprintf("add folder: %v", err), http.StatusBadGateway)
			return
		}
		// New folder → re-apply the on-change debounce so it doesn't sit at
		// ST's default delay until an app restart.
		foldersChanged()
	}

	// Trigger a fresh BEP ClusterConfig exchange with the offering device(s).
	// When a BEP connection is already active, ST may not immediately re-send
	// ClusterConfig after a folder is added — the pause/resume forces it.
	// This lets the Introducer mechanism propagate immediately (e.g. A appears
	// in C's folder when B is an Introducer and A is already in B's folder).
	go func() {
		for _, did := range allOfferers {
			h.st.PauseDevice(did)  //nolint:errcheck
			h.st.ResumeDevice(did) //nolint:errcheck
		}
	}()

	// Mark all offerers as accepted locally — they created/shared the folder so they already "accepted" it.
	// Also notify them via wire so their UI updates immediately.
	for _, did := range allOfferers {
		h.state.SetWireAccepted(req.FolderID, did, true)
	}
	for _, did := range allOfferers {
		dbg("folder: → sending FolderAccept %.8s to %s", req.FolderID, shortID(did))
		h.notify(did, peerwire.Message{
			Type:     peerwire.FolderAccept,
			DeviceID: h.selfID,
			FolderID: req.FolderID,
		})
	}
	h.SchedulePush()
	h.RefreshPendingFolders()
	go h.wire.BroadcastHello()
	w.WriteHeader(http.StatusNoContent)
}

// FolderDecline dismisses a pending folder invite and notifies the sender
// so they can remove us from the folder's device list.
func (h *Handlers) FolderDecline(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req struct {
		FolderID string `json:"folderID"`
		DeviceID string `json:"deviceID"` // the device that sent the invite
	}
	if err := decodeJSON(r, &req); err != nil || req.FolderID == "" || req.DeviceID == "" {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	dbg("folder: decline %.8s from %s", req.FolderID, shortID(req.DeviceID))
	if err := h.st.DismissPendingFolder(req.FolderID, req.DeviceID); err != nil {
		log.Printf("FolderDecline: %v", err)
	}
	// Notify the inviter via wire so they remove us from their folder immediately.
	h.notify(req.DeviceID, peerwire.Message{
		Type:     peerwire.FolderDecline,
		DeviceID: h.selfID,
		FolderID: req.FolderID,
	})
	h.RefreshPendingFolders()
	h.SchedulePush()
	w.WriteHeader(http.StatusNoContent)
}

// FolderRemoveDevice removes one device from a folder's sync group and
// propagates the removal to all remaining participants.
// DELETE /api/folder/device?folderID=X&deviceID=Y
func (h *Handlers) FolderRemoveDevice(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	folderID := r.URL.Query().Get("folderID")
	deviceID := r.URL.Query().Get("deviceID")
	if folderID == "" || deviceID == "" {
		http.Error(w, "missing folderID or deviceID", http.StatusBadRequest)
		return
	}

	participants := folderParticipants(h.listFolders(), folderID)
	dbg("folder: removeDevice %.8s → removing %s (notifying %d others)", folderID, shortID(deviceID), len(participants)-1)

	h.removeDeviceFromFolderInST(folderID, deviceID)
	h.SchedulePush()

	h.notify(deviceID, peerwire.Message{
		Type:     peerwire.FolderRemove,
		DeviceID: h.selfID,
		FolderID: folderID,
	})
	for _, pid := range participants {
		if pid == deviceID {
			continue
		}
		h.notify(pid, peerwire.Message{
			Type:           peerwire.FolderRemove,
			DeviceID:       h.selfID,
			FolderID:       folderID,
			TargetDeviceID: deviceID,
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// FolderRemove removes this device from a folder (leaves the sync group).
// Folders are never fully deleted — only device participation is removed.
func (h *Handlers) FolderRemove(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	folderID := r.URL.Query().Get("id")
	if folderID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	participants := folderParticipants(h.listFolders(), folderID)

	// Remove ourselves from all other participants' folder config.
	for _, did := range participants {
		h.notify(did, peerwire.Message{
			Type:           peerwire.FolderRemove,
			DeviceID:       h.selfID,
			FolderID:       folderID,
			TargetDeviceID: h.selfID,
		})
	}
	// Remove the folder from our own ST — we are leaving, not deleting.
	if err := h.st.RemoveFolder(folderID); err != nil {
		http.Error(w, fmt.Sprintf("leave folder: %v", err), http.StatusInternalServerError)
		return
	}
	h.SchedulePush()
	w.WriteHeader(http.StatusNoContent)
}

// FolderPause pauses or resumes syncing for a folder.
// PATCH /api/folder/pause   body: {folderID, paused}
func (h *Handlers) FolderPause(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPatch) {
		return
	}
	var req struct {
		FolderID string `json:"folderID"`
		Paused   bool   `json:"paused"`
	}
	if err := decodeJSON(r, &req); err != nil || req.FolderID == "" {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := h.st.SetFolderPaused(req.FolderID, req.Paused); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	h.SchedulePush()
	w.WriteHeader(http.StatusNoContent)
}

// FolderPendingList returns the currently cached pending folder offers from Syncthing.
func (h *Handlers) FolderPendingList(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	h.RefreshPendingFolders()
	writeJSON(w, h.pendingFolders.list())
}

// FolderUpdateLabel renames a folder label on this device.
// PATCH /api/folder/label
func (h *Handlers) FolderUpdateLabel(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPatch) {
		return
	}
	var req struct {
		FolderID string `json:"folderID"`
		Label    string `json:"label"`
	}
	if err := decodeJSON(r, &req); err != nil || req.FolderID == "" || req.Label == "" {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	stFolders, err := h.st.ListFolders()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for i, f := range stFolders {
		if f.ID != req.FolderID {
			continue
		}
		stFolders[i].Label = req.Label
		if err := h.st.UpdateFolder(stFolders[i]); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		h.SchedulePush()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Error(w, "folder not found", http.StatusNotFound)
}

// FolderUpdateDirection changes the sync direction for a folder on this device only.
// PATCH /api/folder/direction
func (h *Handlers) FolderUpdateDirection(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPatch) {
		return
	}
	var req struct {
		FolderID  string `json:"folderID"`
		Direction string `json:"direction"`
	}
	if err := decodeJSON(r, &req); err != nil || req.FolderID == "" || req.Direction == "" {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	switch req.Direction {
	case "sendonly", "receiveonly", "sendreceive":
	default:
		http.Error(w, "invalid direction", http.StatusBadRequest)
		return
	}
	stFolders, err := h.st.ListFolders()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for i, f := range stFolders {
		if f.ID != req.FolderID {
			continue
		}
		stFolders[i].Type = req.Direction
		if err := h.st.UpdateFolder(stFolders[i]); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		h.SchedulePush()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Error(w, "folder not found", http.StatusNotFound)
}

// FolderStatus returns the live Syncthing sync status for a folder,
// including paused state (which lives in config, not db/status).
// GET /api/folder/status?id=X
func (h *Handlers) FolderStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	folderID := r.URL.Query().Get("id")
	if folderID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	status, err := h.st.GetFolderStatus(folderID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if paused, err := h.st.GetFolderPaused(folderID); err == nil {
		status.Paused = paused
	}
	writeJSON(w, status)
}


// FolderFixMarker creates the .stfolder marker file that Syncthing requires.
// POST /api/folder/fix-marker?id=X
func (h *Handlers) FolderFixMarker(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
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
	markerPath := filepath.Join(f.Path, ".stfolder")
	if err := os.MkdirAll(markerPath, 0o755); err != nil {
		http.Error(w, fmt.Sprintf("could not create marker: %v", err), http.StatusInternalServerError)
		return
	}
	// Tell Syncthing to rescan immediately so it picks up the new marker.
	if err := h.st.RescanFolder(folderID); err != nil {
		log.Printf("FolderFixMarker: rescan %s: %v", folderID, err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// FolderRevert undoes all local changes in a receive-only folder via Syncthing:
// locally edited files are overwritten with the cluster version and locally
// added files are deleted. Syncthing no-ops this for non-receive-only folders.
// POST /api/folder/revert?id=X
func (h *Handlers) FolderRevert(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	folderID := r.URL.Query().Get("id")
	if folderID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if err := h.st.RevertFolder(folderID); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// FolderCheckPath checks whether a local path is empty (safe to use as sync target).
// GET /api/folder/check?path=X  →  { empty: bool, fileCount: int }
func (h *Handlers) FolderCheckPath(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		// Dir doesn't exist or unreadable — will be created by ST, treat as empty.
		writeJSON(w, map[string]any{"empty": true, "fileCount": 0})
		return
	}
	writeJSON(w, map[string]any{"empty": len(entries) == 0, "fileCount": len(entries)})
}

func (h *Handlers) FolderList(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, h.listFolders())
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (h *Handlers) assertPathNotInUse(path string) error {
	norm := normPath(path)
	for _, f := range h.listFolders() {
		if normPath(f.Path) == norm {
			return fmt.Errorf("path %q is already shared as %q", path, f.Label)
		}
	}
	return nil
}

