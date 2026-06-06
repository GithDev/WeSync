package peerwire

import (
	"net"
	"testing"
)

// hostNet builds an *net.IPNet shaped like the ones net.Interface.Addrs returns:
// IP = the interface's host address, Mask = the subnet mask (NOT the masked
// network address that net.ParseCIDR puts in IPNet.IP).
func hostNet(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", cidr, err)
	}
	return &net.IPNet{IP: ip, Mask: n.Mask}
}

func TestSameSubnetSourceIPs(t *testing.T) {
	wifi := hostNet(t, "192.168.0.5/24")
	lan := hostNet(t, "192.168.0.9/24") // SAME subnet as wifi — the multi-homed case
	other := hostNet(t, "10.0.0.2/24")
	v6 := hostNet(t, "fe80::1/64")
	nets := []*net.IPNet{wifi, lan, other, v6}

	// Peer on 192.168.0.0/24 → both wifi and lan are candidates, order preserved.
	got := sameSubnetSourceIPs(net.ParseIP("192.168.0.50"), nets)
	if len(got) != 2 {
		t.Fatalf("want 2 candidates for same-subnet peer, got %d (%v)", len(got), got)
	}
	if !got[0].Equal(net.ParseIP("192.168.0.5")) || !got[1].Equal(net.ParseIP("192.168.0.9")) {
		t.Errorf("unexpected candidates / order: %v", got)
	}

	// Peer reached only via the 10.x NIC → exactly one candidate.
	if got := sameSubnetSourceIPs(net.ParseIP("10.0.0.7"), nets); len(got) != 1 || !got[0].Equal(net.ParseIP("10.0.0.2")) {
		t.Errorf("want [10.0.0.2] for 10.0.0.7, got %v", got)
	}

	// Peer on a subnet no local NIC is on → no candidates (caller falls back to unbound).
	if got := sameSubnetSourceIPs(net.ParseIP("172.16.0.1"), nets); got != nil {
		t.Errorf("want nil for unrelated subnet, got %v", got)
	}

	// IPv6 destination → nil (we only bind IPv4 source).
	if got := sameSubnetSourceIPs(net.ParseIP("fe80::2"), nets); got != nil {
		t.Errorf("want nil for IPv6 dst, got %v", got)
	}

	// nil / unparseable destination → nil.
	if got := sameSubnetSourceIPs(nil, nets); got != nil {
		t.Errorf("want nil for nil dst, got %v", got)
	}
}

// TestDialSourceIPs_AlwaysHasUnboundFallback is the safety invariant: whatever
// the destination, the candidate list must always end with nil (an unbound
// dial), so the multi-homed binding can only ADD reachable paths and never makes
// behavior worse than the previous unbound-only dial.
func TestDialSourceIPs_AlwaysHasUnboundFallback(t *testing.T) {
	for _, dst := range []string{"192.168.0.50", "10.1.2.3", "not-an-ip", "fe80::1", ""} {
		got := dialSourceIPs(dst)
		if len(got) == 0 || got[len(got)-1] != nil {
			t.Errorf("dialSourceIPs(%q): expected trailing nil unbound fallback, got %v", dst, got)
		}
	}
}
