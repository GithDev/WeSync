package discovery

import (
	"context"
	"os"
	"testing"
	"time"
)

// requireNetTests skips tests that bind real UDP multicast sockets unless
// WESYNC_NET_TESTS=1 is set. Binding 0.0.0.0 + JoinGroup makes Windows pop a
// firewall "allow access" prompt for the test binary on every run, which is
// disruptive on a dev machine. These are integration tests — opt in explicitly
// (CI, or `WESYNC_NET_TESTS=1 go test ./internal/discovery/`) when you want them.
func requireNetTests(t *testing.T) {
	t.Helper()
	if os.Getenv("WESYNC_NET_TESTS") == "" {
		t.Skip("skipping multicast network test; set WESYNC_NET_TESTS=1 to run (binds UDP, triggers a Windows Firewall prompt)")
	}
}

// receivesSID drains s.Peers until it sees wantSID or times out.
func receivesSID(s *Service, wantSID string, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case p := <-s.Peers:
			if p.SID == wantSID {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// enableDisc flips a service fully on/off the way the old SetEnabled did — both
// the discoverability preference and the foreground gate — so these background↔
// foreground cycle tests keep exercising the same transition under the split-input
// model (announce = wantAnnounce AND foreground).
func enableDisc(s *Service, on bool) {
	s.SetWantAnnounce(on)
	s.SetForeground(on)
}

// TestDiscovery_SurvivesEnableCycle reproduces the user-reported blackout: after
// a node goes background (SetEnabled(false)) and comes back (SetEnabled(true)),
// both announcing AND listening must resume — peers must rediscover it and it
// must rediscover them. If either atomic stays stuck, this fails.
func TestDiscovery_SurvivesEnableCycle(t *testing.T) {
	requireNetTests(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, err := NewService(40001)
	if err != nil {
		t.Fatalf("NewService A: %v", err)
	}
	b, err := NewService(40002)
	if err != nil {
		t.Fatalf("NewService B: %v", err)
	}

	enableDisc(a, true)
	enableDisc(b, true)
	go a.Run(ctx) //nolint:errcheck
	go b.Run(ctx) //nolint:errcheck

	if !receivesSID(a, b.SID(), 8*time.Second) {
		t.Fatal("A did not discover B initially")
	}

	// B goes background, then foreground again.
	enableDisc(b, false)
	time.Sleep(500 * time.Millisecond)
	enableDisc(b, true)

	if !receivesSID(a, b.SID(), 8*time.Second) {
		t.Fatal("A did not RE-discover B after B's background→foreground cycle (announce stuck off?)")
	}
	if !receivesSID(b, a.SID(), 8*time.Second) {
		t.Fatal("B did not receive A after re-enabling (forwarding stuck off?)")
	}
}

// TestDiscovery_AnnouncesImmediatelyOnEnable locks in the reopen-latency fix:
// re-enabling discovery must emit an announce at once, not wait out the periodic
// ticker. We assert A re-hears B in well under announceInterval — only possible
// if SetEnabled(true) kicked an immediate announce.
func TestDiscovery_AnnouncesImmediatelyOnEnable(t *testing.T) {
	requireNetTests(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, err := NewService(40003)
	if err != nil {
		t.Fatalf("NewService A: %v", err)
	}
	b, err := NewService(40004)
	if err != nil {
		t.Fatalf("NewService B: %v", err)
	}

	enableDisc(a, true)                // A listens throughout
	go a.Run(ctx)                      //nolint:errcheck
	go b.Run(ctx)                      //nolint:errcheck
	time.Sleep(500 * time.Millisecond) // let both join the multicast group

	// B flips on now. A must hear B fast — faster than a full announceInterval
	// would allow if we waited for the next periodic tick. The immediate announce
	// on enable is the only thing that makes this window achievable.
	enableDisc(b, true)
	if !receivesSID(a, b.SID(), announceInterval-2*time.Second) {
		t.Fatalf("A did not hear B within %s of enable — immediate announce not firing", announceInterval-2*time.Second)
	}
}
