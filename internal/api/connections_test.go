package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func dialUIWS(t *testing.T, inst *instance) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(inst.srv.URL, "http") + "/api/ws"
	c, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial /api/ws: %v", err)
	}
	return c
}

func waitBool(t *testing.T, ch chan bool) bool {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for OnActiveChange")
		return false
	}
}

// TestHub_ForegroundOnEveryConnect locks in the reopen fix: every UI WebSocket
// connect signals foreground (true), not just the first — so reopening the app
// (a new client while a ghost/hidden client may still be counted) reliably wakes
// the node. Background (false) fires only when the LAST client leaves.
func TestHub_ForegroundOnEveryConnect(t *testing.T) {
	inst := newInstance(t, idA, "DeviceA")
	ch := make(chan bool, 8)
	inst.handlers.hub.OnActiveChange(func(active bool) { ch <- active })

	c1 := dialUIWS(t, inst)
	if got := waitBool(t, ch); !got {
		t.Fatalf("first connect: want foreground(true), got %v", got)
	}

	// Second connect (the reopen-while-still-counted case) must ALSO signal true.
	c2 := dialUIWS(t, inst)
	if got := waitBool(t, ch); !got {
		t.Fatalf("second connect: want foreground(true) again, got %v", got)
	}

	// Closing one of two clients must NOT signal background — a client remains.
	c1.Close()
	select {
	case s := <-ch:
		t.Fatalf("disconnect with a client remaining signaled %v, want nothing", s)
	case <-time.After(500 * time.Millisecond):
	}

	// Closing the last client signals background.
	c2.Close()
	if got := waitBool(t, ch); got {
		t.Fatalf("last disconnect: want background(false), got %v", got)
	}
}

func putActive(t *testing.T, inst *instance, active bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]bool{"active": active})
	req, _ := http.NewRequest(http.MethodPut, inst.srv.URL+"/api/active", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/active: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT /api/active: expected 204, got %d", resp.StatusCode)
	}
}

// TestActiveEndpoint verifies the hide-to-tray signal: PUT /api/active flips
// both UDP discovery and the peerwire active flag together, immediately.
func TestActiveEndpoint(t *testing.T) {
	a := newInstance(t, idA, "DeviceA")

	putActive(t, a, false)
	if a.disc.IsListening() {
		t.Error("discovery should be off after active:false")
	}
	if a.handlers.active.Load() {
		t.Error("active flag should be false after active:false")
	}

	putActive(t, a, true)
	if !a.disc.IsListening() {
		t.Error("discovery should be on after active:true")
	}
	if !a.handlers.active.Load() {
		t.Error("active flag should be true after active:true")
	}
}

// TestReopenRestoresForeground guards the recovery path that the reverted
// uiExplicit latch broke: any UI WebSocket connect must bring the node back to
// foreground (discovery + wire on), even after an explicit hide-to-tray. Wired
// exactly as production (backend.go): the hub's OnActiveChange → SetForeground.
func TestReopenRestoresForeground(t *testing.T) {
	inst := newInstance(t, idA, "DeviceA")
	inst.handlers.hub.OnActiveChange(inst.handlers.SetForeground)

	// Hide to tray — explicit background.
	putActive(t, inst, false)
	if inst.disc.IsListening() || inst.handlers.active.Load() {
		t.Fatal("should be background after explicit hide")
	}

	// Reopen: a UI WebSocket connects → hub onChange(true) → foreground restored.
	c := dialUIWS(t, inst)
	defer c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if inst.disc.IsListening() && inst.handlers.active.Load() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("UI reconnect did not restore foreground — recovery path broken")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestReopenCycle_BidirectionalWireRecovers reproduces the user's exact report
// with NO GUI: two real backend instances + real wire, driving the same signals
// the desktop app sends. A and B see each other; B "hides" (PUT /api/active
// false); B's wire drops both ways; B "reopens" via a UI WebSocket reconnect —
// the precise signal the uiExplicit regression swallowed — and the wire must
// recover in BOTH directions. The hub is wired exactly as production
// (OnActiveChange → SetForeground).
func TestReopenCycle_BidirectionalWireRecovers(t *testing.T) {
	a := newInstance(t, idA, "DeviceA")
	b := newInstance(t, idB, "DeviceB")

	// Production wiring: B's UI WebSocket count drives foreground/background.
	b.handlers.hub.OnActiveChange(b.handlers.SetForeground)

	// A and B are paired (mutually trusted) — the post-pairing LAN state.
	a.seedDevice(b.id, "DeviceB")
	b.seedDevice(a.id, "DeviceA")

	// Both foreground; they discover each other (UDP simulated by trackPeer) and
	// the wire comes up both ways.
	a.handlers.SetActive(true)
	b.handlers.SetForeground(true)
	a.trackPeer(b)
	b.trackPeer(a)
	waitWire(t, a, b.id, true, "initial A→B")
	waitWire(t, b, a.id, true, "initial B→A")

	// ── B hides its UI: the desktop app's PUT /api/active{false}. ──
	putActive(t, b, false)
	if b.disc.IsListening() || b.handlers.active.Load() {
		t.Fatal("B should be fully background after hide")
	}
	waitWire(t, b, a.id, false, "after B hides: B→A drops")
	waitWire(t, a, b.id, false, "after B hides: A→B drops (B closed inbound)")

	// ── B reopens. Trigger it like a reopened webview does — a UI WebSocket
	// reconnect, NOT a PUT — because that is the signal the regression ignored. ──
	c := dialUIWS(t, b)
	defer c.Close()

	// Foreground must be restored by the WS connect alone.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if b.disc.IsListening() && b.handlers.active.Load() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("B did not return to foreground on UI reconnect — recovery path broken")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Rediscovery: each side hears the other's announce again (UDP simulated).
	a.trackPeer(b)
	b.trackPeer(a)
	waitWire(t, a, b.id, true, "after B reopens: A→B recovers")
	waitWire(t, b, a.id, true, "after B reopens: B→A recovers")
}

func waitWire(t *testing.T, inst *instance, id string, wantConnected bool, when string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, _, ok := inst.handlers.wire.PeerAddr(id)
		if ok == wantConnected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: wire connected=%v, want %v", when, ok, wantConnected)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestForegroundCycle_DropsAndRestoresWire reproduces the user's exact sequence
// at the handler level: discover a peer (foreground), hide (background → wire
// must drop), then reopen (foreground → wire must come back). This is the
// regression guard for "reopen = permanent blackout".
func TestForegroundCycle_DropsAndRestoresWire(t *testing.T) {
	a := newInstance(t, idA, "DeviceA")
	b := newInstance(t, idB, "DeviceB")

	// Foreground + discover B (UDP TrackPeer dials B's wire server).
	a.handlers.SetActive(true)
	a.trackPeer(b)
	waitWire(t, a, b.id, true, "after initial discovery")

	// Hide to tray → full silence: A's wire to B must drop.
	a.handlers.SetForeground(false)
	waitWire(t, a, b.id, false, "after background")

	// Reopen → foreground; real discovery re-announces, here we re-track.
	a.handlers.SetForeground(true)
	a.trackPeer(b)
	waitWire(t, a, b.id, true, "after reopen")
}

// TestMaintainConnections_GatedOnActive verifies the foreground gate: while the
// app is backgrounded (active=false) MaintainConnections opens no wire
// connections, and SetActive(true) brings them up. This is what keeps wire quiet
// in the background while leaving file sync (ST) untouched.
func TestMaintainConnections_GatedOnActive(t *testing.T) {
	a := newInstance(t, idA, "DeviceA")
	b := newInstance(t, idB, "DeviceB")

	// B is trusted and we have a live address for it — MaintainConnections would
	// dial it if it were allowed to run.
	a.seedDevice(b.id, "DeviceB")
	a.handlers.state.MergePeer(b.id, "", b.addr(), b.port(), nil)

	// Inactive (the default for a fresh node): no outbound wire connection.
	a.handlers.MaintainConnections()
	time.Sleep(100 * time.Millisecond)
	if _, _, ok := a.handlers.wire.PeerAddr(b.id); ok {
		t.Fatal("wire connected while backgrounded — should stay quiet")
	}

	// Foreground → SetActive(true) kicks MaintainConnections and the wire comes up.
	a.handlers.SetActive(true)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, _, ok := a.handlers.wire.PeerAddr(b.id); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("wire did not connect after SetActive(true)")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
