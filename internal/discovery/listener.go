package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"

	"golang.org/x/net/ipv4"
)

type Listener struct {
	selfSID  string // our own SID — used to filter self-announcements
}

func NewListener(selfSID string) *Listener {
	return &Listener{selfSID: selfSID}
}

func (l *Listener) Run(ctx context.Context, peers chan<- Peer) error {
	lc := net.ListenConfig{Control: reuseAddrControl}
	conn, err := lc.ListenPacket(ctx, "udp4", fmt.Sprintf("0.0.0.0:%d", MulticastPort))
	if err != nil {
		return fmt.Errorf("discovery listen: %w", err)
	}

	pc := ipv4.NewPacketConn(conn)
	group := net.ParseIP(MulticastAddr)

	// Joining multicast groups on Android is finicky:
	//   - net.Interfaces() returns wlan0, but Flags often DOESN'T include
	//     FlagMulticast even though wlan0 supports it — netlink reports
	//     different bits than glibc does. So we no longer filter on it.
	//   - JoinGroup with nil iface lets the kernel pick the default route,
	//     which is what we usually want on a mobile device with one wifi
	//     interface anyway.
	// Strategy: try nil first; if that fails, walk every interface and
	// log which ones rejected us — silent "0 interfaces joined" was the
	// worst failure mode the first time this broke.
	joined := 0
	if err := pc.JoinGroup(nil, &net.UDPAddr{IP: group}); err == nil {
		joined++
	} else {
		log.Printf("discovery: JoinGroup(nil) failed: %v", err)
	}

	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if err := pc.JoinGroup(&iface, &net.UDPAddr{IP: group}); err == nil {
			joined++
		} else {
			log.Printf("discovery: JoinGroup(%s) failed: %v", iface.Name, err)
		}
	}
	if joined == 0 {
		conn.Close()
		return fmt.Errorf("discovery: could not join multicast group on any interface")
	}
	log.Printf("discovery: joined multicast group on %d interface(s)", joined)
	pc.SetMulticastLoopback(true)

	// Close the socket when the context is cancelled OR when this function
	// returns (e.g. on a read error). The done channel stops the watcher from
	// outliving Run — important because Service.Run restarts the listener on
	// transient errors, and a leaked watcher per restart would accumulate.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		conn.Close()
	}()

	buf := make([]byte, 2048)
	for {
		n, _, src, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		var pkt Packet
		if err := json.Unmarshal(buf[:n], &pkt); err != nil {
			continue
		}
		if pkt.Version != protocolVersion || pkt.App != appID || pkt.Type != PacketTypeAnnounce {
			continue
		}
		if pkt.SID == "" || pkt.SID == l.selfSID {
			continue // skip packets without SID or our own
		}

		srcIP := ""
		if udpAddr, ok := src.(*net.UDPAddr); ok {
			srcIP = udpAddr.IP.String()
		}

		select {
		case peers <- Peer{SID: pkt.SID, Addr: srcIP, Port: pkt.Port}:
		case <-ctx.Done():
			return nil
		}
	}
}
