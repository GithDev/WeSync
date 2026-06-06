package node

import (
	"fmt"
	"sync"
	"testing"
)

// TestState_NoConcurrentMapAccess hammers the read API (which returns copies)
// against the mutators, all on one State. Go's runtime detects concurrent map
// read+write as a hard fatal error even without -race, so if any read path ever
// aliased an internal map instead of copying, this crashes the test binary.
// With the copy-on-read API it completes cleanly — the race class is gone by
// construction, which is the whole point of this package.
func TestState_NoConcurrentMapAccess(t *testing.T) {
	s := New()
	const workers = 4
	const iters = 3000
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				id := fmt.Sprintf("DEV%d-%02d", w, i%23)
				s.Trust(id)
				s.SetTheyTrustUs(id, true)
				s.MergePeer(id, "peer", "192.168.0.5", 1234, nil)
				s.SetWireAccepted("folder1", id, true)
				s.ReconcileTrust([]DeviceName{{ID: id, Name: "peer"}}, nil, "self")
				if i%50 == 0 {
					s.ReplaceFolderAccepted(map[string]map[string]bool{"folder1": {id: true}})
				}
			}
		}(w)
	}
	for r := 0; r < workers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_ = s.Peers()
				_ = s.Trusted()
				_ = s.TheyTrustUs()
				_ = s.FolderAccepted("folder1")
				_ = s.WireAccepted("folder1")
				_ = s.IsTrusted("DEV0-00")
				_, _ = s.Peer("DEV0-00")
			}
		}()
	}
	wg.Wait()
}

// TestState_ReadsReturnIndependentCopies guards the core invariant: a map
// returned by a read must NOT be backed by the internal map, so mutating the
// returned copy can't corrupt state and reading it later can't race a writer.
func TestState_ReadsReturnIndependentCopies(t *testing.T) {
	s := New()
	s.Trust("a")

	got := s.Trusted()
	got["b"] = true  // mutate the copy
	delete(got, "a") // mutate the copy

	if !s.IsTrusted("a") {
		t.Error("mutating a returned copy corrupted internal state (a was removed)")
	}
	if s.IsTrusted("b") {
		t.Error("mutating a returned copy leaked into internal state (b was added)")
	}
}

func TestState_TrustLifecycle(t *testing.T) {
	s := New()

	s.Trust("a")
	if !s.IsTrusted("a") || s.IsExplicitlyRemoved("a") {
		t.Fatal("Trust should mark trusted and clear removed")
	}
	s.SetTheyTrustUs("a", true)
	if !s.IsMutuallyTrusted("a") {
		t.Fatal("trusted + theyTrustUs should be mutually trusted")
	}

	s.Untrust("a")
	if s.IsTrusted("a") || s.IsMutuallyTrusted("a") || !s.IsExplicitlyRemoved("a") {
		t.Fatal("Untrust should drop trust + theyTrustUs and mark explicitly removed")
	}

	// Re-trusting clears the removal mark.
	s.Trust("a")
	if s.IsExplicitlyRemoved("a") {
		t.Fatal("re-Trust should clear the explicit-removal mark")
	}
}

func TestState_OnPeerTrustSignal(t *testing.T) {
	s := New()

	// Unknown peer asserts trust → just records it, no side effects.
	if out := s.OnPeerTrustSignal("a", true); out.ReassertRemoval || out.DismissPending || out.CascadeUntrust {
		t.Fatalf("fresh trusted signal should have no side effects, got %+v", out)
	}
	if !s.TheyTrustUs()["a"] {
		t.Fatal("trusted signal should record theyTrustUs")
	}

	// We trust them and they withdraw → cascade untrust.
	s.Trust("a")
	s.OnPeerTrustSignal("a", true)
	out := s.OnPeerTrustSignal("a", false)
	if !out.DismissPending || !out.CascadeUntrust {
		t.Fatalf("withdrawal while mutually trusted should dismiss + cascade, got %+v", out)
	}

	// Explicitly-removed peer re-asserts trust → reassert removal.
	s.Untrust("b")
	out = s.OnPeerTrustSignal("b", true)
	if !out.ReassertRemoval {
		t.Fatalf("removed peer re-asserting trust should reassert removal, got %+v", out)
	}
}

func TestState_ReconcileTrust(t *testing.T) {
	s := New()
	s.Untrust("removed") // ST still lists it → should come back as toRemove

	devices := []DeviceName{
		{ID: "self", Name: "me"},
		{ID: "plain", Name: "Plain"},
		{ID: "intro", Name: "Intro"},
		{ID: "removed", Name: "Removed"},
	}
	toRemove := s.ReconcileTrust(devices, map[string]bool{"intro": true}, "self")

	if !s.IsTrusted("plain") || s.IsMutuallyTrusted("plain") {
		t.Error("plain device should be trusted but not mutually (no wire confirm yet)")
	}
	if !s.IsMutuallyTrusted("intro") {
		t.Error("introduced device should be mutually trusted immediately")
	}
	if s.IsTrusted("removed") {
		t.Error("explicitly-removed device must not be re-trusted")
	}
	if len(toRemove) != 1 || toRemove[0] != "removed" {
		t.Errorf("toRemove = %v, want [removed]", toRemove)
	}
	if s.IsTrusted("self") {
		t.Error("self must never be added to trusted set")
	}
}
