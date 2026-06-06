package api

import (
	"encoding/json"
	"time"
	"wesync/internal/discovery"
	"wesync/internal/sysinfo"
)

func (h *Handlers) pushWorker() {
	for range h.pushCh {
		h.Push()
	}
}

// SchedulePush queues a push-only UI broadcast of the CURRENT in-memory snapshot
// — it does NOT re-read ST or rebuild the trust/folder caches. Use it only when
// the change is already reflected in memory; if the change lives in ST, use
// SchedulePipeline instead (see its doc for the full contract). Debounced.
func (h *Handlers) SchedulePush() {
	select {
	case h.pushCh <- struct{}{}:
	default:
	}
}

func (h *Handlers) Push() {
	snap, err := h.snapshot()
	if err != nil {
		return
	}
	h.hub.Broadcast(snap)
}

func (h *Handlers) snapshot() ([]byte, error) {
	livePeers := h.state.Peers()
	trustedIDs := h.state.Trusted()
	selfName := h.state.Name()

	// Connection status comes from ST — the real sync link — not from wire.
	// Wire is just the ephemeral setup channel; basing "connected" on it made
	// devices flip offline whenever the management channel dropped even though
	// ST was syncing fine. nil on error → everything shows offline, which is
	// the truth (ST down ⇒ nothing syncing).
	stConnected, _ := h.st.ConnectedDeviceIDs()

	return json.Marshal(wsState{
		MyID:     h.selfID,
		Name:     selfName,
		Devices:  h.buildDeviceList(trustedIDs, livePeers, stConnected),
		Incoming: h.buildIncoming(livePeers),
		Outgoing: map[string]string{}, // removed — pairing is immediate via ST
		// The discoverability PREFERENCE, not the computed announce state. A
		// connected WS client forces foreground=true (hub.OnActiveChange), so for
		// any client that can observe this, preference == actual. Reporting the
		// computed state instead would flip this false on background and fire a
		// spurious "discovery off" toast on hide→reopen.
		Visible:        h.disc.WantAnnounce(),
		Listening:      h.disc.IsListening(), // receiving others' announcements
		PendingFolders: h.pendingFolders.list(),
		Folders:        h.listFolders(),
	})
}

// buildIncoming returns non-trusted devices from ST's pending device list,
// filtered to those with an active WeSync presence (wire-connected or recently
// seen via UDP). This suppresses stale BEP entries where the remote ST once
// connected but the other side's WeSync is no longer active or has cleaned up.
// When the remote WeSync comes back online it will re-appear naturally.
func (h *Handlers) buildIncoming(livePeers map[string]discovery.Peer) []IncomingRequest {
	// Incoming = wire-signalled trust only.
	// A peer sends trusted:true in Hello when they have us in their ST config.
	// We never use ST pending here — that reflects BEP state which can be stale.
	type candidate struct{ deviceID, name string }
	seen := make(map[string]bool)
	var candidates []candidate

	theyTrustUsSnap := h.state.TheyTrustUs()

	for deviceID := range theyTrustUsSnap {
		if deviceID == h.selfID || h.isTrusted(deviceID) || seen[deviceID] {
			continue
		}
		name := shortID(deviceID)
		if p, ok := livePeers[deviceID]; ok && p.Name != "" {
			name = p.Name
		}
		seen[deviceID] = true
		candidates = append(candidates, candidate{deviceID, name})
	}

	out := make([]IncomingRequest, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, IncomingRequest{DeviceID: c.deviceID, Name: c.name})
	}
	return out
}

// buildDeviceList builds the combined device list: trusted paired devices plus
// live UDP-discovered peers that are wire-connected but not yet paired.
//
// Paired devices report ST's connection state (the real sync link). Unpaired
// discovered peers report wire/UDP presence — ST doesn't know them yet, and
// there "connected" means "reachable to pair right now".
func (h *Handlers) buildDeviceList(trustedIDs map[string]bool, livePeers map[string]discovery.Peer, stConnected map[string]bool) []DeviceWithStatus {
	theyTrustUs := h.state.TheyTrustUs()

	inList := make(map[string]bool, len(trustedIDs))
	devices := make([]DeviceWithStatus, 0, len(trustedIDs)+len(livePeers))

	for id := range trustedIDs {
		name := shortID(id)
		var info *sysinfo.DeviceInfo
		if p, ok := livePeers[id]; ok {
			info = p.Info
			if p.Name != "" {
				name = p.Name
			}
		}
		lastSeen := ""
		if t, ok := h.state.DeviceLastSeen(id); ok {
			lastSeen = t.Format(time.RFC3339)
		}
		devices = append(devices, DeviceWithStatus{
			DeviceID:  id,
			Name:      name,
			Connected: stConnected[id], // ST connection = the real sync link
			STPaired:  true,
			Accepted:  theyTrustUs[id], // they sent us trusted=true AND we trust them
			LastSeen:  lastSeen,
			Info:      info,
		})
		inList[id] = true
	}

	// Live UDP-discovered peers not yet paired — shown only while we BOTH have a
	// wire connection AND are currently hearing their UDP announce. The announce
	// check is what makes "discovery off" actually hide a device: when it stops
	// announcing it drops out here after the grace period, even though the wire may
	// still be up or get re-dialed in the background. (Keyed by the peer's source
	// IP, the stable bridge between the SID-keyed announce and this deviceID list.)
	for id, p := range livePeers {
		if inList[id] || id == h.selfID {
			continue
		}
		_, _, wireConnected := h.wire.PeerAddr(id)
		if !wireConnected {
			continue
		}
		if !h.disc.PresentAddr(p.Addr) {
			continue // not currently announcing → not discoverable, whatever the wire is doing
		}
		devices = append(devices, DeviceWithStatus{
			DeviceID:  id,
			Name:      p.Name,
			Connected: true,
			STPaired:  false,
			Info:      p.Info,
		})
	}

	return devices
}
