package node

// FolderRelationState — see docs/state-model.md for the full model.
//
// This is A's view of folder F for one peer B. Exactly one state at a time.
// Resolution order is fixed in DeriveFolderRelationState — first match wins.
//
// Add states to this enum and to the derive function together; never grow
// scattered string comparisons elsewhere in the codebase.
type FolderRelationState string

const (
	FolderRelationNotMember            FolderRelationState = "not-member"
	FolderRelationInvited              FolderRelationState = "invited"
	FolderRelationAcceptedPausedLocal  FolderRelationState = "accepted-paused-local"
	FolderRelationAcceptedPausedRemote FolderRelationState = "accepted-paused-remote"
	FolderRelationAcceptedSyncing      FolderRelationState = "accepted-syncing"
	// AcceptedSending / AcceptedStalled split the old "B is connected and we're
	// idle" case by whether B still needs data FROM US (PeerNeed) and whether it's
	// actually flowing. This is the honesty fix: a sender that has scanned shows
	// "idle" locally even while a peer is mid-download — the old accepted-idle
	// claimed "up to date" then. See docs/state-model.md.
	FolderRelationAcceptedSending FolderRelationState = "accepted-sending"
	FolderRelationAcceptedStalled FolderRelationState = "accepted-stalled"
	// AcceptedIdle now means genuinely caught up: connected, our folder idle, AND
	// B needs nothing from us. AcceptedBehindOffline is the offline counterpart of
	// AcceptedSending — B is gone but still owes data, so it isn't silently "synced".
	FolderRelationAcceptedIdle          FolderRelationState = "accepted-idle"
	FolderRelationAcceptedBehindOffline FolderRelationState = "accepted-behind-offline"
	FolderRelationAcceptedOffline       FolderRelationState = "accepted-offline"
)

// FolderRelationDimensions is the atomic input to DeriveFolderRelationState.
// Each field maps directly to one source-of-truth in ST (or, for WireAccepted,
// the peerwire fast-path that accelerates but never replaces ST).
//
// Populate every field from a single snapshot — don't mix data from different
// refresh ticks, or the derivation may briefly inhabit an impossible combination
// (e.g. inRemoteSequence=false while wireAccepted=true after a re-invite race).
type FolderRelationDimensions struct {
	// InDeviceList: B is listed in folder.Devices (we configured B as a participant).
	InDeviceList bool
	// InRemoteSequence: B is a key in /rest/db/status?folder=F → remoteSequence.
	// Authoritative "B accepted F" signal — persists across B going offline and
	// across F being paused. See docs/state-model.md, Level 2.
	InRemoteSequence bool
	// WireAccepted: a peerwire FolderAccept message arrived from B for F.
	// Fast-path accelerator — supplements InRemoteSequence; must never be the
	// only source (wire can be down legitimately).
	WireAccepted bool
	// BEPLive: A's ST has an active BEP session with B for F right now
	// (/rest/db/completion → remoteState == "valid").
	BEPLive bool
	// FolderState: A's local folder state (/rest/db/status → state).
	// Values: "idle" | "scanning" | "syncing" | "error" | ...
	FolderState string
	// FolderPausedLocally: we paused F on our side (folder.paused in config).
	FolderPausedLocally bool
	// RemoteStatePaused: B paused F on their side (/rest/db/completion →
	// remoteState == "paused").
	RemoteStatePaused bool
	// PeerNeed: B still needs items FROM US for this folder
	// (/rest/db/completion → needItems/needDeletes/needBytes > 0). This is the
	// device-level "has our state actually reached B?" signal — independent of
	// our own FolderState, which only reflects what WE need to pull. The cache in
	// internal/api fills this from the per-(F,B) completion sweep.
	PeerNeed bool
	// PeerStalled: B needs data from us but none has flowed for a while
	// (needBytes hasn't decreased across recent refreshes). Only meaningful when
	// PeerNeed is set. Surfaces a stuck transfer instead of a fake "syncing".
	PeerStalled bool
}

// DeriveFolderRelationState reduces the dimensions to exactly one state.
// Resolution order matches docs/state-model.md — first matching predicate wins.
// Stays pure (no I/O, no globals) so the table test exhaustively covers it.
func DeriveFolderRelationState(d FolderRelationDimensions) FolderRelationState {
	if !d.InDeviceList {
		return FolderRelationNotMember
	}
	// Acceptance: either ST persistently knows B has F, OR we got the wire
	// shortcut. Either signal alone confirms acceptance.
	accepted := d.InRemoteSequence || d.WireAccepted
	if !accepted {
		return FolderRelationInvited
	}
	// Accepted → refine by liveness and pause state.
	if d.FolderPausedLocally {
		return FolderRelationAcceptedPausedLocal
	}
	if d.RemoteStatePaused {
		return FolderRelationAcceptedPausedRemote
	}
	if !d.BEPLive {
		// Offline, but honest about pending work: if B still owes us data it
		// isn't "synced", it's waiting for B to come back.
		if d.PeerNeed {
			return FolderRelationAcceptedBehindOffline
		}
		return FolderRelationAcceptedOffline
	}
	// Our own folder is actively scanning/pulling — that local work dominates the
	// row regardless of any single peer's need.
	switch d.FolderState {
	case "syncing", "scanning":
		return FolderRelationAcceptedSyncing
	}
	// Our folder is idle. The honest question is now per-peer: does B still need
	// data from us? (A sender sits "idle" while serving a download — the old code
	// called that accepted-idle / "up to date", which was the lie.)
	if d.PeerNeed {
		if d.PeerStalled {
			return FolderRelationAcceptedStalled
		}
		return FolderRelationAcceptedSending
	}
	return FolderRelationAcceptedIdle
}

// IsAccepted is the boolean shorthand the legacy API still uses on
// DeviceAccepted map entries. True iff the state implies B accepted F.
func (s FolderRelationState) IsAccepted() bool {
	switch s {
	case FolderRelationAcceptedPausedLocal,
		FolderRelationAcceptedPausedRemote,
		FolderRelationAcceptedSyncing,
		FolderRelationAcceptedSending,
		FolderRelationAcceptedStalled,
		FolderRelationAcceptedIdle,
		FolderRelationAcceptedBehindOffline,
		FolderRelationAcceptedOffline:
		return true
	}
	return false
}
