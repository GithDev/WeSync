package api

import (
	"log"
	"net/http"
	"time"
	"wesync/internal/discovery"
	"wesync/internal/peerwire"
)

// deviceDisplayName returns the peer's name from memory, or its short ID as fallback.
func (h *Handlers) deviceDisplayName(deviceID string) string {
	p, _ := h.state.Peer(deviceID)
	if p.Name != "" {
		return p.Name
	}
	return shortID(deviceID)
}

// onAccepted is called when a peer accepts our trust (sends Accepted via wire).
// With ST-direct pairing, this can happen when the other side calls trustDevice.
// We just ensure we've also trusted them (may already be done).
func (h *Handlers) onAccepted(fromDeviceID string) {
	if h.isTrusted(fromDeviceID) {
		h.SchedulePush()
		return
	}
	// They trusted us — reciprocate.
	name := h.deviceDisplayName(fromDeviceID)
	h.trustDevice(fromDeviceID, name)
	log.Printf("peer %s accepted — mutual trust established", shortID(fromDeviceID))
	go h.MaintainConnections()
	h.SchedulePush()
}

// onCancelled is called by the peerwire hub when a peer withdraws their pair
// request or removes us.
func (h *Handlers) onCancelled(fromDeviceID string) {
	name := h.deviceDisplayName(fromDeviceID)
	if h.isTrusted(fromDeviceID) {
		// Mutual trust: cascade removal. Sets explicitlyRemoved so ST doesn't re-add.
		dbg("← Cancelled from %s — was trusted, cascading removal", shortID(fromDeviceID))
		h.untrustDevice(fromDeviceID)
	} else {
		// One-sided: they sent us a request that we never accepted, now they cancelled.
		// Do NOT call untrustDevice — that would set explicitlyRemoved and block future requests.
		dbg("← Cancelled from %s — not trusted, clearing state only", shortID(fromDeviceID))
		h.state.SetTheyTrustUs(fromDeviceID, false)
	}
	h.st.DismissPendingDevice(fromDeviceID) //nolint:errcheck
	h.removeDeviceFromAllFolders(fromDeviceID)

	// Re-add to live peers so they appear as discoverable immediately.
	if addr, port, ok := h.wire.PeerAddr(fromDeviceID); ok {
		h.state.AddPeerIfAbsent(discovery.Peer{
			DeviceID: fromDeviceID,
			Name:     name,
			Addr:     addr,
			Port:     port,
		})
	}

	log.Printf("peer %s unpaired", shortID(fromDeviceID))

	// Read folders once — after removeDeviceFromAllFolders so state is final.
	folders := h.listFolders()

	// Notify remaining participants that fromDeviceID was removed.
	for _, f := range folders {
		for _, pid := range f.DeviceIDs {
			h.notify(pid, peerwire.Message{
				Type:           peerwire.FolderRemove,
				DeviceID:       h.selfID,
				FolderID:       f.ID,
				TargetDeviceID: fromDeviceID,
			})
		}
	}

	h.SchedulePush()
}

func (h *Handlers) Pair(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req pairRequest
	if err := decodeJSON(r, &req); err != nil || req.DeviceID == "" {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	// ST-direct pairing: trust immediately, let ST propagate via BEP.
	// Target sees us in their st.PendingDevices() and can accept or dismiss.
	if h.isTrusted(req.DeviceID) {
		// Already trusted — idempotent.
		w.WriteHeader(http.StatusNoContent)
		h.SchedulePush()
		return
	}
	h.trustDevice(req.DeviceID, req.Name)
	log.Printf("trust: → sending request to %s (%s)", req.Name, shortID(req.DeviceID))
	// Notify target via wire so their UI refreshes quickly (sees us in pending).
	if err := h.wire.SendHelloTo(req.DeviceID, time.Second); err != nil {
		log.Printf("trust: → wire not yet up for %s, will retry on connect (%v)", shortID(req.DeviceID), err)
	}
	w.WriteHeader(http.StatusNoContent)
	h.SchedulePush()
}

func (h *Handlers) Incoming(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, h.buildIncoming(h.state.Peers()))

	case http.MethodDelete:
		// Dismiss a pending trust request without accepting.
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		h.st.DismissPendingDevice(id) //nolint:errcheck
		h.state.SetTheyTrustUs(id, false)
		h.SchedulePush()
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) Devices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		livePeers := h.state.Peers()
		trustedIDs := h.state.Trusted()
		stConnected, _ := h.st.ConnectedDeviceIDs()
		all := h.buildDeviceList(trustedIDs, livePeers, stConnected) // uses theyTrustUs for Accepted
		// REST /api/devices returns only explicitly paired (stPaired) devices.
		// Non-trusted wire-connected peers are for the Discovery UI via WS push.
		paired := make([]DeviceWithStatus, 0, len(all))
		for _, d := range all {
			if d.STPaired {
				paired = append(paired, d)
			}
		}
		writeJSON(w, paired)

	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		h.untrustDevice(id)
		h.removeDeviceFromAllFolders(id)

		// Notify removed device — their onCancelled will propagate the removal
		// to remaining mesh participants via FolderRemove messages.
		if err := h.wire.SendSync(id, peerwire.Message{Type: peerwire.Cancelled, DeviceID: h.selfID}, 3*time.Second); err != nil {
			log.Printf("notifyCancelled: %s: %v", shortID(id), err)
		}

		h.wire.BroadcastHello()
		w.WriteHeader(http.StatusNoContent)
		h.SchedulePush()
		// Folder state is already in ST — no additional sync needed.

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

