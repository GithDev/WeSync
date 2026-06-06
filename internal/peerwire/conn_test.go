package peerwire

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
	"wesync/internal/sysinfo"
)

// testPairTrust wires A→B like testPair, but lets the caller control A's
// isTrusted/isRemoved so we can observe the Trusted flag A sends in its Hello.
func testPairTrust(t *testing.T, isTrustedA, isRemovedA func(string) bool, cbB Callbacks) (a, b *Hub) {
	t.Helper()
	a = NewHub(peerA, "nodeA", 0, 0, sysinfo.DeviceInfo{}, nil, Callbacks{}, noOutgoing, isTrustedA, isRemovedA, nil)
	b = NewHub(peerB, "nodeB", 0, 0, sysinfo.DeviceInfo{}, nil, cbB, noOutgoing, nil, nil, nil)

	srv := httptest.NewServer(http.HandlerFunc(b.ServeWS))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	a.Connect(peerB, u.Hostname(), port)
	return a, b
}

// TestConnectHello_TrustFlag locks in the trust-reset fix: the Hello sent on
// connect carries trusted:true only when we trust the peer, trusted:false ONLY
// when we explicitly removed it, and is OMITTED (nil → OnTrusted never fires)
// otherwise. A bare trusted:false from an un-adopted/churning conn used to
// cascade-remove an established trust ("both devices reset to new").
func TestConnectHello_TrustFlag(t *testing.T) {
	yes := func(string) bool { return true }

	cases := []struct {
		name             string
		isTrusted        func(string) bool
		isRemoved        func(string) bool
		wantTrustedFired bool
		wantTrustedVal   bool
	}{
		{"trusted", yes, nil, true, true},
		{"explicitly removed", nil, yes, true, false},
		{"unknown peer", nil, nil, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			helloCh := make(chan struct{}, 1)
			trustedCh := make(chan bool, 1)
			cbB := Callbacks{
				OnHello: func(_, _ string, _, _ int) {
					select {
					case helloCh <- struct{}{}:
					default:
					}
				},
				OnTrusted: func(_ string, trusted bool) { trustedCh <- trusted },
			}
			testPairTrust(t, tc.isTrusted, tc.isRemoved, cbB)

			waitCh(t, helloCh, "OnHello")

			select {
			case got := <-trustedCh:
				if !tc.wantTrustedFired {
					t.Fatalf("OnTrusted fired (val=%v) but trusted should have been omitted (nil)", got)
				}
				if got != tc.wantTrustedVal {
					t.Errorf("OnTrusted = %v, want %v", got, tc.wantTrustedVal)
				}
			case <-time.After(500 * time.Millisecond):
				if tc.wantTrustedFired {
					t.Fatalf("OnTrusted never fired, want trusted=%v", tc.wantTrustedVal)
				}
			}
		})
	}
}

// TestDisconnectAll verifies the primitive SetActive uses to go quiet in the
// background: every outbound connection is closed.
func TestDisconnectAll(t *testing.T) {
	a, _ := testPair(t, Callbacks{}, Callbacks{OnHello: func(_, _ string, _, _ int) {}}, noOutgoing, noOutgoing)

	// Wait for A's outbound connection to come up.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, _, ok := a.PeerAddr(peerB); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("outbound connection to B never established")
		}
		time.Sleep(20 * time.Millisecond)
	}

	a.DisconnectAll()

	if _, _, ok := a.PeerAddr(peerB); ok {
		t.Fatal("expected no connection to B after DisconnectAll")
	}
}

// TestConnectBySID_NudgesBackoff is the regression guard for "peer reappears but
// takes ages to show": a connection sitting in reconnect backoff must be woken
// (nudged) by a fresh announce, not left to wait out the exponential timer.
func TestConnectBySID_NudgesBackoff(t *testing.T) {
	h := NewHub(peerA, "nodeA", 0, 0, sysinfo.DeviceInfo{}, nil, Callbacks{}, noOutgoing, nil, nil, nil)

	// A connection to 127.0.0.1:59999 that exists but isn't connected — i.e. it's
	// sitting in reconnect backoff. (We don't start run(); we just need the entry.)
	c := newPeerConn("oldsid", "127.0.0.1", 59999, h)
	h.conns["oldsid"] = c

	if started := h.ConnectBySID("newsid", "127.0.0.1", 59999); started {
		t.Fatal("ConnectBySID opened a second conn to the same endpoint; expected it to nudge the existing one")
	}
	select {
	case <-c.wake:
		// nudged — good
	default:
		t.Fatal("ConnectBySID did not nudge the backed-off conn; it would wait out the full backoff")
	}
}

// TestServeWS_RejectsWhenNotAccepting verifies the inbound gate: when accepting
// is off (app hidden), inbound peer connections are refused before the upgrade.
func TestServeWS_RejectsWhenNotAccepting(t *testing.T) {
	h := NewHub(peerB, "nodeB", 0, 0, sysinfo.DeviceInfo{}, nil, Callbacks{}, noOutgoing, nil, nil, nil)
	srv := httptest.NewServer(http.HandlerFunc(h.ServeWS))
	t.Cleanup(srv.Close)

	// Accepting (default): a plain GET gets past the gate and fails the WS
	// upgrade instead (400), i.e. NOT a 503 refusal.
	if resp, err := http.Get(srv.URL); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusServiceUnavailable {
			t.Fatalf("default hub refused inbound (503) but should accept")
		}
	}

	h.SetAccepting(false)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("not-accepting hub: got %d, want 503", resp.StatusCode)
	}
}

// TestSetAccepting_ClosesExistingInbound verifies that turning accepting off
// drops live inbound connections immediately, so the peer's outbound link breaks
// at once (no waiting for a grace period).
func TestSetAccepting_ClosesExistingInbound(t *testing.T) {
	a, b := testPair(t, Callbacks{}, Callbacks{OnHello: func(_, _ string, _, _ int) {}}, noOutgoing, noOutgoing)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, _, ok := a.PeerAddr(peerB); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("A→B never connected")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// B stops accepting → B closes the inbound socket → A's outbound read errors.
	b.SetAccepting(false)

	deadline = time.Now().Add(2 * time.Second)
	for {
		if _, _, ok := a.PeerAddr(peerB); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("A's wire to B did not drop after B stopped accepting")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
