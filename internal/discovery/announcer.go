package discovery

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"

	"golang.org/x/net/ipv4"
)

type Announcer struct {
	sid  string // ephemeral session ID, random per process start
	port int
	addr *net.UDPAddr
}

func NewAnnouncer(port int) (*Announcer, error) {
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", MulticastAddr, MulticastPort))
	if err != nil {
		return nil, err
	}
	sid, err := generateSID()
	if err != nil {
		return nil, err
	}
	return &Announcer{sid: sid, port: port, addr: addr}, nil
}

func (a *Announcer) SID() string { return a.sid }

// send broadcasts one announce packet out EVERY usable interface — not just the
// OS default route. This mirrors the listener, which joins the multicast group
// on every interface. It matters on machines with more than one active NIC
// (e.g. WiFi plus a plugged-in LAN cable, or a Hyper-V/WSL vEthernet): a single
// DialUDP would egress on just one interface, so peers on the others would
// never receive the announce — and since the receiver derives a peer's address
// from the packet's source IP, an announce that left the "wrong" interface also
// hands out an unreachable address. Sending per-interface gives peers on each
// network a packet with a source IP they can actually dial back.
func (a *Announcer) send() {
	data, err := json.Marshal(Packet{
		Version: protocolVersion,
		App:     appID,
		Type:    PacketTypeAnnounce,
		Port:    a.port,
		SID:     a.sid,
	})
	if err != nil {
		return
	}

	conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		return
	}
	defer conn.Close()
	pc := ipv4.NewPacketConn(conn)

	ifaces, err := net.Interfaces()
	if err != nil {
		conn.WriteTo(data, a.addr) //nolint:errcheck — fallback to OS default route
		return
	}

	sent := false
	for i := range ifaces {
		iface := ifaces[i]
		// Skip loopback and down interfaces. Do NOT filter on FlagMulticast —
		// some platforms (notably Android) under-report it even on interfaces
		// that do support multicast (see the listener for the same caveat).
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		if !hasIPv4(&iface) {
			continue // not on any IPv4 network — nothing to announce from here
		}
		if err := pc.SetMulticastInterface(&iface); err != nil {
			continue
		}
		if _, err := pc.WriteTo(data, nil, a.addr); err == nil {
			sent = true
		}
	}
	if !sent {
		conn.WriteTo(data, a.addr) //nolint:errcheck — fallback to OS default route
	}
}

// hasIPv4 reports whether the interface has at least one usable (non-loopback,
// non-link-local) IPv4 address — i.e. it's actually on a network we can send on.
func hasIPv4(iface *net.Interface) bool {
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil || ipnet.IP.IsLinkLocalUnicast() || ipnet.IP.IsLoopback() {
			continue
		}
		return true
	}
	return false
}

func generateSID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
