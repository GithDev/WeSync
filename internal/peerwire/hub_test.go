package peerwire

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"
	"wesync/internal/sysinfo"
)

const (
	peerA = "AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA"
	peerB = "BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB"
	peerC = "CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC"
)

// testPair wires up two hubs A?B: A's outbound WS connects to B's inbound server.
// Returns A's hub, B's hub, and a cleanup function.
func testPair(t *testing.T, cbA, cbB Callbacks, outgoingA, outgoingB func() []string) (a, b *Hub) {
	t.Helper()
	a = NewHub(peerA, "nodeA", 0, 0, sysinfo.DeviceInfo{}, nil, cbA, outgoingA, nil, nil, nil)
	b = NewHub(peerB, "nodeB", 0, 0, sysinfo.DeviceInfo{}, nil, cbB, outgoingB, nil, nil, nil)

	srv := httptest.NewServer(http.HandlerFunc(b.ServeWS))
	t.Cleanup(srv.Close)

	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	a.Connect(peerB, u.Hostname(), port)
	return a, b
}

func noOutgoing() []string { return nil }

func waitCh(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
	}
}

// TestHello_FiresOnConnect verifies that when A connects to B, B's OnHello fires with A's device ID.
func TestHello_FiresOnConnect(t *testing.T) {
	ch := make(chan struct{}, 1)
	var mu sync.Mutex
	var gotFrom string

	cbB := Callbacks{
		OnHello: func(fromDeviceID, _ string, _, _ int) {
			mu.Lock()
			gotFrom = fromDeviceID
			mu.Unlock()
			ch <- struct{}{}
		},
	}
	testPair(t, Callbacks{}, cbB, noOutgoing, noOutgoing)

	waitCh(t, ch, "OnHello")

	mu.Lock()
	from := gotFrom
	mu.Unlock()

	if from != peerA {
		t.Errorf("OnHello: expected from %s, got %s", peerA[:7], from[:7])
	}
}

// TestSendSync_OnAccepted verifies that SendSync(Accepted) triggers B's OnAccepted.
func TestSendSync_OnAccepted(t *testing.T) {
	ch := make(chan struct{}, 1)
	var gotFrom string
	cbB := Callbacks{
		OnHello: func(_, _ string, _, _ int) {},
		OnAccepted: func(fromDeviceID string) {
			gotFrom = fromDeviceID
			ch <- struct{}{}
		},
	}
	a, _ := testPair(t, Callbacks{}, cbB, noOutgoing, noOutgoing)

	// Wait a moment for A's outbound connection to establish.
	time.Sleep(50 * time.Millisecond)

	if err := a.SendSync(peerB, Message{Type: Accepted, DeviceID: peerA}, 3*time.Second); err != nil {
		t.Fatalf("SendSync: %v", err)
	}

	waitCh(t, ch, "OnAccepted")

	if gotFrom != peerA {
		t.Errorf("OnAccepted: expected from %s, got %s", peerA[:7], gotFrom[:7])
	}
}

// TestSendSync_OnCancelled verifies that SendSync(Cancelled) triggers B's OnCancelled.
func TestSendSync_OnCancelled(t *testing.T) {
	ch := make(chan struct{}, 1)
	var gotFrom string
	cbB := Callbacks{
		OnHello: func(_, _ string, _, _ int) {},
		OnCancelled: func(fromDeviceID string) {
			gotFrom = fromDeviceID
			ch <- struct{}{}
		},
	}
	a, _ := testPair(t, Callbacks{}, cbB, noOutgoing, noOutgoing)

	time.Sleep(50 * time.Millisecond)

	if err := a.SendSync(peerB, Message{Type: Cancelled, DeviceID: peerA}, 3*time.Second); err != nil {
		t.Fatalf("SendSync: %v", err)
	}

	waitCh(t, ch, "OnCancelled")

	if gotFrom != peerA {
		t.Errorf("OnCancelled: expected from %s, got %s", peerA[:7], gotFrom[:7])
	}
}

// TestBroadcastHello verifies that BroadcastHello triggers OnHello on connected peers.
func TestBroadcastHello(t *testing.T) {
	hellos := make(chan struct{}, 4)
	cbB := Callbacks{
		OnHello: func(_, _ string, _, _ int) { hellos <- struct{}{} },
	}
	a, _ := testPair(t, Callbacks{}, cbB, noOutgoing, noOutgoing)

	// Initial hello on connect.
	waitCh(t, hellos, "initial OnHello")

	// BroadcastHello must trigger a second OnHello on B.
	a.BroadcastHello()
	waitCh(t, hellos, "OnHello after BroadcastHello")
}

// TestSendSync_NoConnection verifies SendSync errors when no connection exists.
func TestSendSync_NoConnection(t *testing.T) {
	h := NewHub(peerA, "nodeA", 0, 0, sysinfo.DeviceInfo{}, nil, Callbacks{}, noOutgoing, nil, nil, nil)
	err := h.SendSync(peerB, Message{Type: Accepted}, 100*time.Millisecond)
	if err == nil {
		t.Error("expected error for unknown peer, got nil")
	}
}
