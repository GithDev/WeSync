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
		// Once ST is awake we let the sync run to completion — there is no stall
		// guard. The session stays "busy" (extending) while our own folder is
		// scanning/syncing OR a connected peer still needs data from us; it lapses
		// only when ST is genuinely idle with nobody behind.
		busy := keepaliveBusy(pr)
		g.sessionEndsAt = nextSessionEnd(time.Now(), g.sessionStartedAt, g.sessionEndsAt, busy, pr.connected)
		g.mu.Unlock()
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

// probeResult is one poll's view of ST, split into the signals the keepalive needs.
type probeResult struct {
	folderBusy bool // our own folder is scanning/syncing (local work in progress)
	peerBehind bool // a connected peer still needs data from us (they're pulling)
	connected  bool // at least one peer connected
}

// probeST samples ST for the keepalive decision. On any lookup failure it errs
// toward "there's work" (folderBusy / peerBehind true) so a transient hiccup
// doesn't tear ST down mid-sync.
func probeST() probeResult {
	c, err := stClient()
	if err != nil {
		return probeResult{peerBehind: true} // assume work on a transient failure
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
	return r
}
