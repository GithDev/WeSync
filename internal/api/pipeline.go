package api

import (
	"log"
	"time"
	"wesync/internal/node"
)

// pipelineRetryDelay is how long to wait before re-running the pipeline when
// Syncthing is not yet responding, so a down/restarting ST doesn't turn the
// worker into a busy-spin.
const pipelineRetryDelay = 2 * time.Second

// pipeline runs the state update steps in fixed order.
// Each step reads from its sources and produces complete, correct state for its domain.
// The order never changes.
//
//	UpdateOnline  — wire connections, UDP peer reachability
//	UpdateTrust   — trusted device set vs ST, wire-sourced peer state
//	UpdateFolders — folder config, pending invites, completion state
//	Push          — broadcast updated state to UI
func (h *Handlers) pipeline() {
	if !h.stReady() {
		// ST not up yet — retry after a delay rather than immediately re-queuing,
		// which would spin the worker (and hammer ST) while it's down/restarting.
		time.AfterFunc(pipelineRetryDelay, h.SchedulePipeline)
		return
	}
	h.updateTrust()
	h.updateFolders()
	h.Push()
}

// stReady returns true if Syncthing is responding.
func (h *Handlers) stReady() bool {
	_, err := h.st.ListDevices()
	return err == nil
}

// updateTrust ensures trustedIDs matches ST and picks up Introducer-added devices.
func (h *Handlers) updateTrust() {
	stDevices, err := h.st.ListDevices()
	if err != nil {
		return
	}

	// Devices added via the ST Introducer (IntroducedBy != "" in any folder) are
	// auto-trusted and skip the trust-request flow — no "incoming" card shown.
	introduced := make(map[string]bool)
	folders, foldersErr := h.st.ListFolders()
	if foldersErr == nil {
		for _, f := range folders {
			for _, d := range f.Devices {
				if d.IntroducedBy != "" {
					introduced[d.DeviceID] = true
				}
			}
		}
	}

	// Introducer-flag invariant: a device is an introducer iff we paired with it
	// directly (introducedBy == "") and the global Introducer setting is on. An
	// introduced device is NEVER flagged — flagging it would cascade introducer
	// trust transitively across the mesh (every introduced peer would in turn be
	// allowed to introduce others), which is the "too many introducers" footgun.
	// The mesh still forms: introductions propagate across the direct-pair edges
	// that DO carry the flag. The global setting is the master kill-switch.
	//
	// Skip entirely if ListFolders failed — with an empty `introduced` map we'd
	// momentarily flip introduced devices to introducer=true, then back on the
	// next tick. Only patch on a real change so we don't rewrite ST config (and
	// trigger reconnect churn) every pipeline run.
	if foldersErr == nil {
		wantOn := h.db.GetIntroducer()
		for _, d := range stDevices {
			if d.DeviceID == h.selfID {
				continue
			}
			want := wantOn && !introduced[d.DeviceID]
			if d.Introducer != want {
				if err := h.st.UpdateDeviceIntroducer(d.DeviceID, want); err != nil {
					log.Printf("updateTrust: set introducer %s=%v: %v", shortID(d.DeviceID), want, err)
				}
			}
		}
	}

	devices := make([]node.DeviceName, 0, len(stDevices))
	for _, d := range stDevices {
		devices = append(devices, node.DeviceName{ID: d.DeviceID, Name: d.Name})
	}
	// State reconciles trusted/theyTrustUs/peer-names under its lock and returns
	// the explicitly-removed devices ST still lists, for us to drop from folders.
	toRemove := h.state.ReconcileTrust(devices, introduced, h.selfID)

	// With Introducer ON, don't fight re-addition to global device list — that causes
	// an infinite loop. Just ensure re-added devices are removed from all folder configs.
	for _, id := range toRemove {
		h.removeDeviceFromAllFolders(id)
	}
	// Note: explicitlyRemoved is NOT cleared here — it persists until the peer
	// acknowledges removal (sends trusted:false) or trustDevice() is called.
}

// updateFolders refreshes folder state from ST.
func (h *Handlers) updateFolders() {
	h.RefreshPendingFolders()
	h.RefreshFolderCompletion()
}

// ── Pipeline scheduler ────────────────────────────────────────────────────────

// SchedulePipeline triggers a pipeline run, collapsing multiple concurrent
// triggers into at most one queued execution (after the current run finishes).
// SchedulePipeline queues a FULL state refresh: re-read ST (trust + folders),
// rebuild the in-memory caches, then push to the UI. Debounced (single-item
// queue) and re-queued until ST is ready.
//
// SchedulePipeline vs SchedulePush — pick by what changed:
//
//   - SchedulePipeline: use after ANYTHING that may have changed trust/folder
//     state in Syncthing, or after an ST event. It re-derives state FROM ST, so
//     it can't go stale. This is the safe default — when unsure, use this.
//   - SchedulePush (push.go): push-only. Re-broadcasts the CURRENT in-memory
//     snapshot without re-reading ST. Use ONLY when the change is already
//     reflected in memory (e.g. you just mutated theyTrustUs/peers under the
//     lock) and you just need the UI to see it. If the real change lives in ST
//     and not yet in the caches, SchedulePush shows stale state until the next
//     pipeline run — that's the foot-gun, so prefer SchedulePipeline if in doubt.
func (h *Handlers) SchedulePipeline() {
	select {
	case h.pipelineCh <- struct{}{}:
	default: // already queued
	}
}

// pipelineWorker runs the pipeline serially from the channel.
func (h *Handlers) pipelineWorker() {
	for range h.pipelineCh {
		h.pipeline()
	}
}
