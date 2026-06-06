package api

import (
	"fmt"
	"log"
	"wesync/internal/discovery"
	"wesync/internal/node"
	"wesync/internal/peerwire"
	"wesync/internal/sysinfo"
)

// TrackPeer initiates a wire connection to a UDP-discovered peer.
func (h *Handlers) TrackPeer(p discovery.Peer) {
	if p.Addr == "" || p.Port == 0 {
		return
	}
	sid := p.SID
	if sid == "" {
		sid = fmt.Sprintf("%s:%d", p.Addr, p.Port)
	}
	if h.wire.ConnectBySID(sid, p.Addr, p.Port) {
		log.Printf("Peer found: %s (SID %.8s)", p.Addr, sid)
	}
}

// DropPeer disconnects a peer's wire connection if it's not trusted.
func (h *Handlers) DropPeer(sid string) {
	h.wire.DropUntrustedSID(sid, h.isTrusted)
	// The peer stopped announcing (UDP grace expired) — refresh the UI so it drops
	// from the discoverable list now. buildDeviceList recomputes presence per push,
	// so this is what makes the disappearance timely instead of waiting for the
	// next unrelated push.
	h.SchedulePush()
}

// onHello is called when a peer sends its Hello message.
func (h *Handlers) onHello(fromDeviceID, fromAddr string, fromPort, fromSTPort int) {
	if fromDeviceID == h.selfID {
		return
	}
	h.state.MergePeer(fromDeviceID, "", fromAddr, fromPort, nil)

	if fromAddr != "" && fromPort != 0 && node.IsRoutableAddr(fromAddr) {
		h.wire.Connect(fromDeviceID, fromAddr, fromPort)
	}
	h.SchedulePush()
}

// onPeerState is called when a peer sends its name and sysinfo.
func (h *Handlers) onPeerState(fromDeviceID, name string, info *sysinfo.DeviceInfo) {
	if fromDeviceID == h.selfID {
		return
	}
	h.state.SetPeerNameInfo(fromDeviceID, name, info)

	if name != "" && h.isTrusted(fromDeviceID) {
		go h.st.UpdateDevice(fromDeviceID, name) //nolint:errcheck
	}
	h.SchedulePush()
}

func shortID(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

func (h *Handlers) onTrusted(fromDeviceID string, trusted bool) {
	if fromDeviceID == "" || fromDeviceID == h.selfID {
		return
	}
	// State decides what changed under its lock; we perform the ST/wire side
	// effects out here. Note: explicitlyRemoved is deliberately NOT cleared on a
	// trusted:false signal — only an explicit re-pair (trustDevice) clears it —
	// so updateTrust won't re-add the device from stale ST config mid-removal.
	out := h.state.OnPeerTrustSignal(fromDeviceID, trusted)
	if out.ReassertRemoval {
		// We removed them but they're (re)asserting trust — show them as incoming
		// and re-send trusted:false so they know we haven't accepted.
		dbg("← %s sent request but we removed them — re-sending trusted:false", shortID(fromDeviceID))
		h.notify(fromDeviceID, peerwire.Message{
			Type:     peerwire.Hello,
			DeviceID: h.selfID,
			Trusted:  boolPtr(false),
		})
	}
	if out.DismissPending {
		go h.st.DismissPendingDevice(fromDeviceID) //nolint:errcheck
	}
	if out.CascadeUntrust {
		dbg("cascade: removing %s — they withdrew and we had them trusted", shortID(fromDeviceID))
		h.untrustDevice(fromDeviceID)
	}
	h.SchedulePush()
}
