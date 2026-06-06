package peerwire

import "net"

// dialSourceIPs returns the local source IPs to try binding an outbound dial to,
// most-specific first and always ending with nil (an unbound dial that lets the
// OS pick the route). The same-subnet candidates force egress out the interface
// that is actually on the peer's network — the fix for multi-homed hosts where
// the OS default route would pick a NIC that can't reach a directly-connected
// peer. nil is appended last so behavior is never worse than an unbound dial.
func dialSourceIPs(dstAddr string) []net.IP {
	dst := net.ParseIP(dstAddr)
	out := sameSubnetSourceIPs(dst, localIPNets())
	return append(out, nil)
}

// localIPNets returns the IPNets of every up, non-loopback interface.
func localIPNets() []*net.IPNet {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var nets []*net.IPNet
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				nets = append(nets, ipnet)
			}
		}
	}
	return nets
}

// sameSubnetSourceIPs returns the IPv4 addresses among nets whose subnet
// contains dst. Pure (no syscalls) so it is unit-testable. Returns nil when dst
// is not a usable IPv4 address — callers then fall back to an unbound dial.
func sameSubnetSourceIPs(dst net.IP, nets []*net.IPNet) []net.IP {
	if dst == nil || dst.To4() == nil {
		return nil
	}
	var out []net.IP
	for _, n := range nets {
		if n == nil || n.IP.To4() == nil {
			continue
		}
		if n.Contains(dst) {
			out = append(out, n.IP)
		}
	}
	return out
}
