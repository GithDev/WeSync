package discovery

import (
	"wesync/internal/sysinfo"
)

const (
	// 239.0.82.98 is in the administratively scoped (site-local) range
	// reserved for application-specific use. It does not overlap with mDNS
	// (224.0.0.251) or any well-known multicast group.
	MulticastAddr      = "239.0.82.98"
	MulticastPort      = 21026
	protocolVersion    = 1
	PacketTypeAnnounce = "announce"
	appID              = "wesync" // filters out unrelated multicast traffic
)

type Packet struct {
	Version int    `json:"v"`
	App     string `json:"app"` // always "wesync"
	Type    string `json:"type"`
	Port    int    `json:"port"`
	SID     string `json:"sid"` // ephemeral session ID (random per process start)
	// DeviceID intentionally omitted — identity established by TLS cert on wire.
}

type Peer struct {
	SID  string `json:"sid"`  // ephemeral session ID from UDP announcement
	Addr string `json:"addr"`
	Port int    `json:"port"`
	// DeviceID and Name filled after wire connects (from TLS cert and Hello).
	DeviceID string              `json:"deviceID,omitempty"`
	Name     string              `json:"name,omitempty"`
	Info     *sysinfo.DeviceInfo `json:"info,omitempty"`
}

// Clone returns a deep copy of the Peer, including its Info pointer and the
// slices inside it, so a holder can't mutate state shared with the source.
func (p Peer) Clone() Peer {
	p.Info = p.Info.Clone() // nil-safe
	return p
}
