package api

import (
	"testing"
	"wesync/internal/discovery"
)

// Device IDs are long strings matching Syncthing's format.
const (
	idA = "AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA"
	idB = "BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB"
	idC = "CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC"
)

// setup returns two fully wired instances that know each other via UDP discovery.
func setup(t *testing.T) (a, b *instance) {
	t.Helper()
	a = newInstance(t, idA, "DeviceA")
	b = newInstance(t, idB, "DeviceB")
	a.trackPeer(b)
	b.trackPeer(a)
	return a, b
}

// setup3 returns three instances with full peerwire mesh and seeded device knowledge.
func setup3(t *testing.T) (a, b, c *instance) {
	t.Helper()
	a = newInstance(t, idA, "DeviceA")
	b = newInstance(t, idB, "DeviceB")
	c = newInstance(t, idC, "DeviceC")
	a.trackPeer(b); a.trackPeer(c)
	b.trackPeer(a); b.trackPeer(c)
	c.trackPeer(a); c.trackPeer(b)
	a.seedDevice(idB, "DeviceB"); a.seedDevice(idC, "DeviceC")
	b.seedDevice(idA, "DeviceA"); b.seedDevice(idC, "DeviceC")
	c.seedDevice(idA, "DeviceA"); c.seedDevice(idB, "DeviceB")
	return a, b, c
}

// ── Pair (ST-direct) ──────────────────────────────────────────────────────────

func TestPair_TrustsBImmediately(t *testing.T) {
	// pair(A, B) immediately trusts B — no waiting for acceptance.
	a, _ := setup(t)
	doPair(t, a, idB, "DeviceB")
	if !a.handlers.isTrusted(idB) {
		t.Error("expected B trusted immediately after pair")
	}
	if !a.st.hasDevice(idB) {
		t.Error("expected B in A's Syncthing config after pair")
	}
}

func TestPair_IdempotentWhenAlreadyTrusted(t *testing.T) {
	a, _ := setup(t)
	a.seedDevice(idB, "DeviceB")
	doPair(t, a, idB, "DeviceB") // should not panic or duplicate
	if !a.handlers.isTrusted(idB) {
		t.Error("expected B still trusted")
	}
}

func TestMutualPair_BothTrusted(t *testing.T) {
	a, b := setup(t)
	doPair(t, a, idB, "DeviceB")
	doPair(t, b, idA, "DeviceA")
	if !a.handlers.isTrusted(idB) {
		t.Error("A should trust B")
	}
	if !b.handlers.isTrusted(idA) {
		t.Error("B should trust A")
	}
}

// ── Incoming (ST pending/devices) ─────────────────────────────────────────────

func TestIncoming_ShowsWireSignalledTrust(t *testing.T) {
	// Incoming appears only from wire trusted:true — not from ST pending alone.
	a, b := setup(t)
	_ = a
	// Simulate A sending trusted:true to B via wire (A added B to ST).
	b.handlers.onTrusted(idA, true)
	assertPendingHas(t, b, idA)
}

func TestIncoming_STPendingAloneDoesNotShowIncoming(t *testing.T) {
	// ST pending without a wire trusted:true signal must NOT create an incoming request.
	// This was the source of phantom requests from stale BEP state.
	_, b := setup(t)
	b.st.addPending(idA, "DeviceA")
	incoming := getIncoming(t, b)
	for _, p := range incoming {
		if p.DeviceID == idA {
			t.Error("ST pending without wire signal must NOT appear as incoming")
		}
	}
}

func TestIncoming_ExcludesAlreadyTrusted(t *testing.T) {
	a, b := setup(t)
	_ = a
	b.seedDevice(idA, "DeviceA") // already trusted
	b.handlers.onTrusted(idA, true)
	incoming := getIncoming(t, b)
	for _, p := range incoming {
		if p.DeviceID == idA {
			t.Error("trusted device should NOT appear as incoming")
		}
	}
}

func TestIgnore_ClearsIncomingRequest(t *testing.T) {
	_, b := setup(t)
	// Simulate wire trust signal from A.
	b.handlers.onTrusted(idA, true)
	assertPendingHas(t, b, idA)
	doIgnore(t, b, idA)
	assertPendingEmpty(t, b)
}

// ── Cancel / Remove ───────────────────────────────────────────────────────────

func TestCancel_SyncsToSyncthing(t *testing.T) {
	a, _ := setup(t)
	a.seedDevice(idB, "DeviceB")
	doRemoveDevice(t, a, idB)
	assertNoDevice(t, a, idB)
	assertSyncthingNoDevice(t, a, idB)
}

func TestCancel_NotifiesB_ClearsIncomingOnB(t *testing.T) {
	a, b := setup(t)
	doPair(t, a, idB, "DeviceB") // A sends trusted:true → B sees A as incoming
	assertPendingHas(t, b, idA)
	doRemoveDevice(t, a, idB)    // A sends trusted:false → B clears incoming
	assertPendingEmpty(t, b)
}

func TestRemove_ClearsDeviceFromDB(t *testing.T) {
	a, _ := setup(t)
	a.seedDevice(idB, "DeviceB")
	a.st.setConnected(idB, true)
	doRemoveDevice(t, a, idB)
	assertNoDevice(t, a, idB)
}

// ── Connected -----------------------------------------------------------------

func TestConnected_ShownAfterMutualPair(t *testing.T) {
	a, b := setup(t)
	doPair(t, a, idB, "DeviceB")
	doPair(t, b, idA, "DeviceA")

	a.st.setConnected(idB, true)
	b.st.setConnected(idA, true)

	assertDeviceConnected(t, a, idB)
	assertDeviceConnected(t, b, idA)
}

func TestRemove_NotifiesB_ClearsDeviceOnB(t *testing.T) {
	a, b := setup(t)
	doPair(t, a, idB, "DeviceB")
	doPair(t, b, idA, "DeviceA")
	a.st.setConnected(idB, true)
	b.st.setConnected(idA, true)

	doRemoveDevice(t, a, idB)
	assertNoDevice(t, b, idA)
}

// ── Rename ────────────────────────────────────────────────────────────────────

func TestRename_UpdatesOwnName(t *testing.T) {
	a, _ := setup(t)
	doRename(t, a, "MyNewName")
	if got := a.handlers.state.Name(); got != "MyNewName" {
		t.Errorf("expected name %q, got %q", "MyNewName", got)
	}
}

func TestRename_PropagatesTo_PairedPeer(t *testing.T) {
	a, b := setup(t)
	doPair(t, a, idB, "DeviceB")
	doPair(t, b, idA, "DeviceA")
	assertDeviceExists(t, a, idB)

	doRename(t, a, "OscarsLaptop")
	assertDeviceName(t, b, idA, "OscarsLaptop")
}

func TestRename_PropagatesTo_DiscoverablePeer(t *testing.T) {
	a, b := setup(t)
	doRename(t, a, "RenamedDevice")
	assertPeerName(t, b, idA, "RenamedDevice")
}

func TestRename_NotRevertedByStaleUDP(t *testing.T) {
	a, b := setup(t)
	doPair(t, a, idB, "DeviceB")
	doPair(t, b, idA, "DeviceA")

	doRename(t, a, "NewName")
	assertDeviceName(t, b, idA, "NewName")

	// Simulate stale UDP packet — peerwire is active so this must be ignored.
	b.handlers.TrackPeer(discovery.Peer{
		DeviceID: idA,
		Name:     "OldName",
		Addr:     b.addr(),
		Port:     b.port(),
	})

	assertDeviceName(t, b, idA, "NewName")
}

// ── Sync ──────────────────────────────────────────────────────────────────────

// TestSync_DevicesInST verifies that trustDevice writes directly to ST (no sync needed).
func TestSync_DevicesInST(t *testing.T) {
	a, _ := setup(t)
	a.seedDevice(idB, "DeviceB")

	if !a.st.hasDevice(idB) {
		t.Error("expected B in Syncthing after trustDevice")
	}
}

// TestTrustedFromST verifies that trustedIDs is populated from ST's device list.
// ST is the sole source of truth — no DB needed.
func TestMigration_SeedsTrustedFromST(t *testing.T) {
	a, _ := setup(t)

	if a.handlers.isTrusted(idB) {
		t.Fatal("expected B not trusted initially")
	}

	// trustDevice adds to ST + trustedIDs — this is the only mechanism.
	a.handlers.trustDevice(idB, "DeviceB")

	if !a.handlers.isTrusted(idB) {
		t.Error("expected B trusted after trustDevice")
	}
	if !a.st.hasDevice(idB) {
		t.Error("expected B in ST after trustDevice")
	}
}

// TestSync_TrustDeviceIdempotent verifies trust persists.
func TestSync_TrustDeviceIdempotent(t *testing.T) {
	a, _ := setup(t)
	a.seedDevice(idB, "DeviceB")
	a.seedDevice(idB, "DeviceB") // idempotent
	if !a.st.hasDevice(idB) {
		t.Error("expected B still in Syncthing")
	}
}

// ── Phantom incoming (explicitlyRemoved guard) ────────────────────────────────

// TestIncoming_ExplicitlyRemovedShowsAsIncoming verifies that a device A explicitly
// removed can still show as incoming when they send trusted:true (re-pair request).
// The user must be able to accept them again — explicitlyRemoved only blocks ST
// from re-adding them automatically, not the user from accepting a new request.
func TestIncoming_ExplicitlyRemovedShowsAsIncoming(t *testing.T) {
	a, _ := setup(t)
	a.seedDevice(idB, "DeviceB")
	a.handlers.untrustDevice(idB) // sets explicitlyRemoved[B]

	// B tries to re-pair (sends trusted:true again).
	a.handlers.onTrusted(idB, true)

	// A must show B as incoming so the user can accept the re-pair request.
	assertPendingHas(t, a, idB)
}

// TestIncoming_ExplicitlyRemovedIgnoresSTPending reproduces the bug where a
// recently removed device still shows in ST pending (BEP backlog) and is
// wire-connected, causing it to appear as incoming.
func TestIncoming_FalseSignalClearsStaleTheyTrustUs(t *testing.T) {
	// When a device reconnects with trusted:false (they no longer have us),
	// any stale theyTrustUs entry must be cleared so they don't show as incoming.
	a, _ := setup(t)

	// Simulate stale state: B previously sent trusted:true but now has removed us.
	a.handlers.onTrusted(idB, true)
	assertPendingHas(t, a, idB) // B shows as incoming

	// B reconnects, sends trusted:false (they removed us).
	a.handlers.onTrusted(idB, false)

	assertPendingEmpty(t, a) // stale incoming must be gone
}

// TestPairCancel_NoPhantomIncomingOnEitherSide is the full scenario that the
// user reported: A requests trust, A cancels — both sides must be clean.
func TestPairCancel_NoPhantomIncomingOnEitherSide(t *testing.T) {
	a, b := setup(t)
	doPair(t, a, idB, "DeviceB")
	assertPendingHas(t, b, idA)
	doRemoveDevice(t, a, idB)
	assertPendingEmpty(t, a)
	assertPendingEmpty(t, b)
}

// TestPairCancelRePair_BSeesSecondRequest verifies that after A cancels and
// re-pairs, B sees the second incoming request.
// Without the fix: onCancelled on B calls untrustDevice(A) which sets
// explicitlyRemoved[A] on B, permanently blocking A's future requests.
func TestPairCancelRePair_BSeesSecondRequest(t *testing.T) {
	a, b := setup(t)

	// First pair + cancel
	doPair(t, a, idB, "DeviceB")
	assertPendingHas(t, b, idA)
	doRemoveDevice(t, a, idB)
	assertPendingEmpty(t, b)

	// Re-pair — B MUST show incoming again
	doPair(t, a, idB, "DeviceB")
	assertPendingHas(t, b, idA)
}

// ── Full flow ─────────────────────────────────────────────────────────────────

func TestFullFlow_PairAccept(t *testing.T) {
	a, b := setup(t)
	doPair(t, a, idB, "DeviceB") // A trusts B immediately
	doPair(t, b, idA, "DeviceA") // B trusts A immediately

	assertDeviceExists(t, a, idB)
	assertDeviceExists(t, b, idA)
}

func TestFullFlow_PairCancel(t *testing.T) {
	a, b := setup(t)
	doPair(t, a, idB, "DeviceB")
	doPair(t, b, idA, "DeviceA")

	assertDeviceExists(t, a, idB)
	doRemoveDevice(t, a, idB)
	assertNoDevice(t, a, idB)
	assertNoDevice(t, b, idA) // B is notified via Cancelled
}

func TestFullFlow_PairCancelPairAccept(t *testing.T) {
	a, b := setup(t)
	doPair(t, a, idB, "DeviceB")
	doPair(t, b, idA, "DeviceA")
	doRemoveDevice(t, a, idB)
	assertNoDevice(t, a, idB)

	// Re-pair
	doPair(t, a, idB, "DeviceB")
	doPair(t, b, idA, "DeviceA")
	assertDeviceExists(t, a, idB)
}
