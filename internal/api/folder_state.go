package api

import (
	"log"
	"sync"
	"time"
	"wesync/internal/node"
	"wesync/internal/syncthing"
)

// peerProgress remembers, per (folder, device), the last needBytes we observed
// and when that count last dropped — the inputs RefreshFolderCompletion uses to
// decide a transfer has "stalled" (B needs data but nothing is flowing).
type peerProgress struct {
	needBytes    int64
	lastProgress time.Time
}

// stallAfter is how long a connected peer can need bytes from us without
// needBytes dropping before we call the transfer stalled. Long enough to ride
// out a slow block / brief lull; short enough that a genuinely stuck transfer
// surfaces instead of masquerading as "syncing".
const stallAfter = 60 * time.Second

// ── Pending folder cache (from Syncthing BEP) ─────────────────────────────────

type folderPendingCache struct {
	mu      sync.RWMutex
	folders []syncthing.PendingFolder
}

func (c *folderPendingCache) set(folders []syncthing.PendingFolder) {
	c.mu.Lock()
	c.folders = folders
	c.mu.Unlock()
}

func (c *folderPendingCache) list() []syncthing.PendingFolder {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]syncthing.PendingFolder, len(c.folders))
	copy(out, c.folders)
	return out
}

// RefreshPendingFolders fetches pending folders from Syncthing and caches them.
// Filters out folders we already have configured — ST BEP causes peers to re-offer
// folders we accepted, which would show as phantom invites on our side.
func (h *Handlers) RefreshPendingFolders() {
	pending, err := h.st.PendingFolders()
	if err != nil {
		log.Printf("folder: RefreshPending error: %v", err)
		return
	}
	existing, err := h.st.ListFolders()
	if err != nil {
		log.Printf("folder: RefreshPending listFolders error: %v", err)
		h.pendingFolders.set(pending)
		return
	}
	// Build set of current participants per folder.
	participants := make(map[string]map[string]bool, len(existing))
	for _, f := range existing {
		set := make(map[string]bool, len(f.Devices))
		for _, d := range f.Devices {
			set[d.DeviceID] = true
		}
		participants[f.ID] = set
	}

	var filtered []syncthing.PendingFolder
	for _, pf := range pending {
		if p, haveFolder := participants[pf.FolderID]; haveFolder && p[pf.DeviceID] {
			// We have the folder AND the offerer is already a participant — phantom BEP re-offer.
			dbg("folder: pending %s (%.8s) from %s filtered — offerer already in folder", pf.Label, pf.FolderID, shortID(pf.DeviceID))
		} else {
			// Either we don't have the folder, or the offerer is not a participant (re-invite after removal).
			dbg("folder: pending invite — %s (%.8s) from %s", pf.Label, pf.FolderID, shortID(pf.DeviceID))
			filtered = append(filtered, pf)
		}
	}
	h.pendingFolders.set(filtered)
}

// RefreshFolderCompletion updates the per-folder per-device BEP acceptance cache.
// Called on every ST event. Uses CompareAndSwap to prevent concurrent runs —
// if a refresh is already in progress, the incoming call is silently skipped.
func (h *Handlers) RefreshFolderCompletion() {
	if !h.completionRefreshing.CompareAndSwap(false, true) {
		return // already running — skip rather than pile up N×M ST calls
	}
	defer h.completionRefreshing.Store(false)

	stFolders, err := h.st.ListFolders()
	if err != nil {
		return
	}

	now := time.Now()
	connected, _ := h.st.ConnectedDeviceIDs()

	h.folderPeerProgressMu.Lock()
	prevProgress := h.folderPeerProgress
	h.folderPeerProgressMu.Unlock()

	accepted := make(map[string]map[string]bool, len(stFolders))
	peerInfo := make(map[string]map[string]node.FolderPeerInfo, len(stFolders))
	nextProgress := make(map[string]map[string]peerProgress, len(stFolders))

	for _, f := range stFolders {
		// Acceptance: use folder status' RemoteSequence map — a device's presence
		// as a key is ST's authoritative "this device accepted the folder" signal.
		// It's persistent (survives device offline + folder pause) and not confused
		// by "notSharing" / "idle" connection states. Accepted-empty → value 0 but
		// key present; never accepted → key absent.
		status, statusErr := h.st.GetFolderStatus(f.ID)
		perDeviceAcc := make(map[string]bool, len(f.Devices))
		perDeviceInfo := make(map[string]node.FolderPeerInfo, len(f.Devices))
		perDeviceProg := make(map[string]peerProgress, len(f.Devices))
		for _, d := range f.Devices {
			if d.DeviceID == h.selfID {
				continue
			}
			if statusErr != nil {
				perDeviceAcc[d.DeviceID] = false
			} else {
				_, ok := status.RemoteSequence[d.DeviceID]
				perDeviceAcc[d.DeviceID] = ok
			}

			// Per-peer completion: how much B still needs FROM US. This is the
			// device-level "has our data actually reached B?" signal the honest
			// folder-relation states (sending / stalled / synced / behind-offline)
			// are built on. On error we leave this peer's info absent (the derive
			// then falls back to acceptance + connection only).
			comp, compErr := h.st.DeviceCompletion(f.ID, d.DeviceID)
			if compErr != nil {
				continue
			}
			need := comp.NeedItems > 0 || comp.NeedDeletes > 0 || comp.NeedBytes > 0

			// Stall tracking: needBytes must actually drop to count as progress.
			// Carry forward the last-progress time; bump it to now when bytes fell.
			// A brand-new behind peer starts fresh (lastProgress = now) so it gets
			// the full stallAfter window before we call it stuck.
			lastProg := now
			if pm := prevProgress[f.ID]; pm != nil {
				if pp, ok := pm[d.DeviceID]; ok {
					if comp.NeedBytes < pp.needBytes {
						lastProg = now
					} else {
						lastProg = pp.lastProgress
					}
				}
			}
			perDeviceProg[d.DeviceID] = peerProgress{needBytes: comp.NeedBytes, lastProgress: lastProg}
			stalled := need && comp.NeedBytes > 0 && connected[d.DeviceID] && now.Sub(lastProg) >= stallAfter
			perDeviceInfo[d.DeviceID] = node.FolderPeerInfo{
				Need:        need,
				NeedBytes:   comp.NeedBytes,
				NeedItems:   comp.NeedItems,
				Completion:  comp.Completion,
				RemoteState: comp.RemoteState,
				Stalled:     stalled,
			}
		}
		accepted[f.ID] = perDeviceAcc
		peerInfo[f.ID] = perDeviceInfo
		nextProgress[f.ID] = perDeviceProg
	}

	h.folderPeerProgressMu.Lock()
	h.folderPeerProgress = nextProgress
	h.folderPeerProgressMu.Unlock()

	h.state.ReplaceFolderAccepted(accepted)
	h.state.ReplaceFolderPeerInfo(peerInfo)

	if ls, err := h.st.DeviceLastSeen(); err == nil {
		h.state.ReplaceDeviceLastSeen(ls)
	}
}

// removeDeviceFromAllFolders removes deviceID from every shared folder in ST.
func (h *Handlers) removeDeviceFromAllFolders(deviceID string) {
	for _, f := range h.listFolders() {
		for _, did := range f.DeviceIDs {
			if did == deviceID {
				h.removeDeviceFromFolderInST(f.ID, deviceID)
				log.Printf("unpair: removed %s from folder %s", shortID(deviceID), f.ID)
				break
			}
		}
	}
}

// resetAccepted clears both wire and BEP acceptance for a device in a folder.
// Called when a device is newly (re-)added so stale state doesn't carry over.
func (h *Handlers) resetAccepted(folderID, deviceID string) {
	h.state.ResetAccepted(folderID, deviceID)
}

// addDeviceToFolderInST adds a device to an existing folder's config in Syncthing.
// Idempotent — safe to call if device is already present.
func (h *Handlers) addDeviceToFolderInST(folderID, deviceID string) {
	stFolders, err := h.st.ListFolders()
	if err != nil {
		log.Printf("addDeviceToFolderInST: list: %v", err)
		return
	}
	for _, f := range stFolders {
		if f.ID != folderID {
			continue
		}
		for _, d := range f.Devices {
			if d.DeviceID == deviceID {
				return // already present
			}
		}
		f.Devices = append(f.Devices, syncthing.FolderDevice{DeviceID: deviceID})
		if err := h.st.UpdateFolder(f); err != nil {
			log.Printf("addDeviceToFolderInST: update %s: %v", shortID(folderID), err)
			return
		}
		h.resetAccepted(folderID, deviceID)
		return
	}
}

// removeDeviceFromFolderInST removes a device from a folder's config in Syncthing.
func (h *Handlers) removeDeviceFromFolderInST(folderID, deviceID string) {
	stFolders, err := h.st.ListFolders()
	if err != nil {
		log.Printf("removeDeviceFromFolderInST: list: %v", err)
		return
	}
	for _, f := range stFolders {
		if f.ID != folderID {
			continue
		}
		// Build a fresh slice rather than reusing f.Devices' backing array in
		// place — f is a copy of the struct, but its Devices slice still aliases
		// the array ListFolders() returned, and overwriting it would corrupt that.
		kept := make([]syncthing.FolderDevice, 0, len(f.Devices))
		for _, d := range f.Devices {
			if d.DeviceID != deviceID {
				kept = append(kept, d)
			}
		}
		f.Devices = kept
		if err := h.st.UpdateFolder(f); err != nil {
			log.Printf("removeDeviceFromFolderInST: update %s: %v", shortID(folderID), err)
		}
		return
	}
}
