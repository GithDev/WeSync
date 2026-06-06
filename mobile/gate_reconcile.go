package mobile

import (
	"log"
	"time"

	"wesync/internal/stmanager"
)

// This file is the gate's process-control half: the reconcile loop and the
// single function that actually starts/stops ST. It consumes the pure
// decisions from gate_decision.go and drives the real Syncthing subprocess
// (via stmanager) and the platform host (radio/CPU locks) toward them.

// reconcileLoop is the ONLY place that starts/stops ST. It wakes on a kick
// (input changed) or on its own poll ticker (a session is open), recomputes
// desiredRunning from scratch, and drives the process toward it.
//
// The poll ticker is deliberately INDEPENDENT of kicks: it runs at a fixed
// cadence while a session is open and is never reset by an input change.
// (An earlier version re-armed a time.After on every kick, so a flapping
// network could starve the poll entirely and a sync would close mid-flight
// because probeST never ran to extend it.)
func (g *gate) reconcileLoop() {
	var ticker *time.Ticker
	var tick <-chan time.Time
	for {
		doPoll := false
		select {
		case <-g.kick:
		case <-tick:
			doPoll = true
		}
		g.reconcileOnce(doPoll)

		// Keep the poll ticker running while a session is open OR a foreground
		// grace is pending — the latter so the loop re-reconciles when the
		// grace lapses and tears ST down (no input change fires at expiry).
		g.mu.Lock()
		now := time.Now()
		keepPolling := now.Before(g.sessionEndsAt) || now.Before(g.foregroundUntil)
		g.mu.Unlock()
		switch {
		case keepPolling && ticker == nil:
			ticker = time.NewTicker(syncPollInterval)
			tick = ticker.C
		case !keepPolling && ticker != nil:
			ticker.Stop()
			ticker = nil
			tick = nil
		}
	}
}

// reconcileOnce: (1) optionally refresh the session deadline from ST,
// (2) drive the ST process toward desiredRunning, (3) push run-state to
// the host. All blocking I/O happens off the lock.
func (g *gate) reconcileOnce(poll bool) {
	g.mu.Lock()
	if g.stExePath == "" {
		g.mu.Unlock()
		return
	}
	snap := g.snapshotLocked()
	g.mu.Unlock()

	now := time.Now()
	if poll && snap.sessionOpen(now) {
		pr := probeST()
		g.mu.Lock()
		// Stall guard: progress = transferred bytes moved past the floor since the
		// last poll. !pr.ok (ST unreadable) counts as no progress, so a wedged REST
		// eventually stalls out instead of pinning ST forever.
		progressed := pr.ok && pr.transferred-g.lastTransferBytes > stallFloorBytes
		if pr.ok {
			g.lastTransferBytes = pr.transferred
		}
		var busy bool
		switch {
		case pr.folderBusy:
			// Our own scan/pull — bytes needn't flow over the network (scanning is
			// local CPU/disk), so this is NOT stall-guarded; ST flips to idle on its
			// own when done. Reset the counter so a quiet scan can't pre-stall the
			// peer-pull phase that follows it.
			g.stallPolls = 0
			busy = true
		case pr.peerBehind:
			// A connected peer is still pulling from us — keep the session alive,
			// but only while bytes are actually moving. The stall guard lets a stuck
			// transfer (or a wedged REST) lapse instead of pinning ST.
			var stalled bool
			g.stallPolls, stalled = stallTick(g.stallPolls, progressed)
			busy = !stalled
		default:
			g.stallPolls = 0
		}
		g.sessionEndsAt = nextSessionEnd(time.Now(), g.sessionStartedAt, g.sessionEndsAt, busy, pr.connected)
		g.mu.Unlock()
		// Not busy → ST has settled (or the pull stalled), so its completion view is
		// authoritative: reconcile the dirty flag to the real state — clear it when
		// every peer is caught up, keep it when one is still behind (e.g. offline or
		// a stalled pull). This is what eventually lets an all-send-only device stop
		// ticking and sleep — and what keeps it ticking until the data lands.
		if !busy {
			reconcileDirty()
		}
		g.mu.Lock()
		snap = g.snapshotLocked()
		g.mu.Unlock()
		now = time.Now()
	}

	want := snap.desiredRunning(now)
	have := stmanager.IsRunning()
	switch {
	case want && !have:
		if err := stmanager.Start(snap.stExePath); err != nil {
			log.Printf("gate: stmanager.Start: %v", err)
			g.emitEvent("error", "ST start failed: "+err.Error())
		} else {
			g.emitEvent("st_start", "ST started — "+snap.reasonString())
		}
	case !want && have:
		if err := stmanager.Stop(); err != nil {
			log.Printf("gate: stmanager.Stop: %v", err)
		}
		g.emitEvent("st_stop", "ST stopped — "+snap.reasonString())
	}

	g.notifyHost(want)
}

// notifyHost pushes a run-state edge to the platform wrapper so it can
// hold/release the radio + CPU. Deduped so we only fire on transitions.
func (g *gate) notifyHost(active bool) {
	g.mu.Lock()
	h := g.host
	changed := g.lastSyncActive != active
	g.lastSyncActive = active
	g.mu.Unlock()
	if h == nil || !changed {
		return
	}
	func() {
		defer func() { _ = recover() }() // never let a misbehaving host crash the gate
		h.OnSyncActive(active)
	}()
}

// reconcileDirty asks ST whether every accepted peer is caught up with us and
// sets the gate's dirty flag to match. Called while a session is open and ST has
// gone idle — at that point ST's completion view is authoritative, so this both
// clears dirty (everyone synced) and re-asserts it (a peer is still behind),
// overriding the watcher's best-effort guess with the real state. On error it
// leaves dirty as-is (a transient hiccup shouldn't drop a pending backup).
func reconcileDirty() {
	c, err := stClient()
	if err != nil {
		return
	}
	// Snapshot the change generation BEFORE the off-lock probe so we can tell
	// whether the watcher saw a new local change while we were asking ST.
	g.mu.Lock()
	gen := g.dirtyGen
	g.mu.Unlock()

	behind, err := c.AnyPeerBehind()
	if err != nil {
		return
	}

	g.mu.Lock()
	switch {
	case behind:
		g.dirty = true // a peer is still behind — definitely dirty
	case g.dirtyGen == gen:
		g.dirty = false // ST says everyone is caught up and nothing changed mid-probe
	default:
		// A watcher event bumped the generation while we probed; its change isn't
		// reflected in the completion we just read, so keep dirty set — the next
		// reconcile re-checks against fresh ST state.
	}
	g.mu.Unlock()
}

// probeResult is one poll's view of ST, split into the signals the keepalive
// and stall guard need.
type probeResult struct {
	folderBusy  bool  // our own folder is scanning/syncing (local work in progress)
	peerBehind  bool  // a connected peer still needs data from us (they're pulling)
	connected   bool  // at least one peer connected
	transferred int64 // cumulative in+out bytes — stall-guard progress signal
	ok          bool  // transferred was readable (ST REST answered)
}

// probeST samples ST for the keepalive decision. On any lookup failure it errs
// toward "there's work" (folderBusy / peerBehind true) so a transient hiccup
// doesn't tear ST down — but it leaves ok=false when the byte counter can't be
// read, so the stall guard treats that as no progress and a persistently wedged
// REST still lets the session lapse instead of pinning ST forever.
func probeST() probeResult {
	c, err := stClient()
	if err != nil {
		return probeResult{peerBehind: true} // assume work; ok=false → stall-guarded
	}
	r := probeResult{}
	if b, err := c.IsAnyFolderBusy(); err != nil {
		r.folderBusy = true
	} else {
		r.folderBusy = b
	}
	if behind, err := c.AnyConnectedPeerBehind(); err != nil {
		r.peerBehind = true
	} else {
		r.peerBehind = behind
	}
	if conns, err := c.ConnectedDeviceIDs(); err == nil {
		for _, on := range conns {
			if on {
				r.connected = true
				break
			}
		}
	}
	if tb, err := c.TransferredTotalBytes(); err == nil {
		r.transferred = tb
		r.ok = true
	}
	return r
}
