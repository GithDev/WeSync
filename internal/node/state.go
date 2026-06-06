// Package node owns WeSync's in-memory per-node state: the peer / trust / folder
// maps the rest of the app reads to render the UI and drive peerwire.
//
// ALL access goes through this type's RWMutex-guarded API, and every read
// returns a COPY: map reads copy the map, and Peer reads deep-copy each Peer
// (including its *DeviceInfo and the slices inside it, via Peer.Clone). Callers
// therefore can never alias internal state and read it after the lock is
// released — the data-race class that previously crashed the process (fatal
// error: concurrent map read and map write) is gone by construction, not patched
// per call site.
//
// Syncthing remains the source of truth; this is the derived projection of it
// (plus a few wire-only fast-path signals) that snapshot() and MaintainConnections
// read from.
package node

import (
	"net"
	"sync"
	"time"

	"wesync/internal/discovery"
	"wesync/internal/sysinfo"
)

type State struct {
	mu sync.RWMutex

	name string // this node's display name (mutable; was Handlers.name)

	peers      map[string]discovery.Peer
	peerCertFP map[string]string // deviceID → TLS cert fingerprint (set on peer verify)
	trustedIDs map[string]bool   // deviceIDs trusted via explicit pairing — source: ST device list

	// theyTrustUs: peers that sent trusted=true via wire Hello. With trustedIDs:
	//   accepted = trustedIDs[id] && theyTrustUs[id]
	//   waiting  = trustedIDs[id] && !theyTrustUs[id]
	//   incoming = !trustedIDs[id] && theyTrustUs[id]
	theyTrustUs map[string]bool

	// explicitlyRemoved: devices we just unpaired; suppresses re-add from stale ST
	// config until the peer acknowledges (sends trusted:false) or is re-trusted.
	explicitlyRemoved map[string]bool

	// folderAccepted[folderID][deviceID]: ST-derived BEP acceptance (RemoteSequence).
	folderAccepted map[string]map[string]bool
	// wireAccepted[folderID][deviceID]: peerwire FolderAccept fast-path (session-only).
	wireAccepted map[string]map[string]bool

	// folderPeer[folderID][deviceID]: cached per-(folder,device) completion view
	// from RefreshFolderCompletion's ST sweep — what B still needs FROM US, plus
	// its live BEP state and a derived "stalled" flag. Drives the honest
	// per-peer folder-relation states (sending / stalled / synced / behind-offline).
	// Distinct from folderAccepted (a different ST endpoint, persistent acceptance).
	folderPeer map[string]map[string]FolderPeerInfo
	// deviceLastSeen[deviceID]: ST's last-seen time per device (/rest/stats/device).
	// The honest UI anchors an offline peer's "in sync as of <time>" to this.
	deviceLastSeen map[string]time.Time
}

// FolderPeerInfo is the cached completion view for one (folder, device): how much
// B still needs FROM US, its live folder state, and whether the transfer has
// stalled (needBytes not decreasing). Computed in internal/api and stored here.
type FolderPeerInfo struct {
	Need        bool // B still needs items/deletes/bytes from us (computed with full completion)
	NeedBytes   int64
	NeedItems   int
	Completion  float64 // 0–100, B's completion of OUR data
	RemoteState string  // "valid" | "paused" | "notSharing" | "unknown"
	Stalled     bool
}

// New returns an empty, ready-to-use State.
func New() *State {
	return &State{
		peers:             map[string]discovery.Peer{},
		peerCertFP:        map[string]string{},
		trustedIDs:        map[string]bool{},
		theyTrustUs:       map[string]bool{},
		explicitlyRemoved: map[string]bool{},
		folderAccepted:    map[string]map[string]bool{},
		wireAccepted:      map[string]map[string]bool{},
		folderPeer:        map[string]map[string]FolderPeerInfo{},
		deviceLastSeen:    map[string]time.Time{},
	}
}

// DeviceName is the minimal device identity ReconcileTrust needs from ST.
type DeviceName struct {
	ID   string
	Name string
}

// ── self name ───────────────────────────────────────────────────────────────

func (s *State) Name() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.name
}

func (s *State) SetName(n string) {
	s.mu.Lock()
	s.name = n
	s.mu.Unlock()
}

// ── reads (always return copies) ─────────────────────────────────────────────

// Peers returns a deep copy of the peer map (each Peer is cloned, including its
// Info pointer), so callers share no mutable state with the internal map.
func (s *State) Peers() map[string]discovery.Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]discovery.Peer, len(s.peers))
	for k, v := range s.peers {
		out[k] = v.Clone()
	}
	return out
}

// Peer returns a deep copy of a single peer entry by deviceID.
func (s *State) Peer(id string) (discovery.Peer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.peers[id]
	return p.Clone(), ok
}

// Trusted returns a copy of the trusted-device set.
func (s *State) Trusted() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyBool(s.trustedIDs)
}

// TheyTrustUs returns a copy of the "peers who trust us" set.
func (s *State) TheyTrustUs() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyBool(s.theyTrustUs)
}

// FolderAccepted returns a copy of the BEP-acceptance set for one folder.
func (s *State) FolderAccepted(folderID string) map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyBool(s.folderAccepted[folderID])
}

// WireAccepted returns a copy of the wire-acceptance set for one folder.
func (s *State) WireAccepted(folderID string) map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyBool(s.wireAccepted[folderID])
}

// ── predicates ───────────────────────────────────────────────────────────────

func (s *State) IsTrusted(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.trustedIDs[id]
}

func (s *State) IsMutuallyTrusted(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.trustedIDs[id] && s.theyTrustUs[id]
}

func (s *State) IsExplicitlyRemoved(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.explicitlyRemoved[id]
}

func (s *State) PeerCertFP(id string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.peerCertFP[id]
}

// ── trust mutators ───────────────────────────────────────────────────────────

// Trust adds id to the trusted set and clears any explicit-removal mark.
func (s *State) Trust(id string) {
	s.mu.Lock()
	s.trustedIDs[id] = true
	delete(s.explicitlyRemoved, id)
	s.mu.Unlock()
}

// Untrust removes id from trusted + theyTrustUs and marks it explicitly removed.
func (s *State) Untrust(id string) {
	s.mu.Lock()
	delete(s.trustedIDs, id)
	delete(s.theyTrustUs, id)
	s.explicitlyRemoved[id] = true
	s.mu.Unlock()
}

// SetTheyTrustUs records (v=true) or clears (v=false) that id trusts us.
func (s *State) SetTheyTrustUs(id string, v bool) {
	s.mu.Lock()
	if v {
		s.theyTrustUs[id] = true
	} else {
		delete(s.theyTrustUs, id)
	}
	s.mu.Unlock()
}

func (s *State) SetPeerCertFP(id, fp string) {
	s.mu.Lock()
	s.peerCertFP[id] = fp
	s.mu.Unlock()
}

// ── peer mutators ────────────────────────────────────────────────────────────

// MergePeer is the canonical peer-record update from a wire Hello (was
// Handlers.updatePeerLocked). With no usable addr it only refreshes a name on an
// existing entry; otherwise it upserts, preferring a routable addr and carrying
// forward an existing name/info when the new Hello omits them.
func (s *State) MergePeer(deviceID, name, addr string, port int, info *sysinfo.DeviceInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if addr == "" || port == 0 {
		if name != "" {
			if p, ok := s.peers[deviceID]; ok {
				p.Name = name
				s.peers[deviceID] = p
			}
		}
		return
	}
	resolvedAddr := addr
	if !IsRoutableAddr(addr) {
		if existing, ok := s.peers[deviceID]; ok && existing.Addr != "" {
			resolvedAddr = existing.Addr
		}
	}
	if existing, ok := s.peers[deviceID]; ok {
		if name == "" && existing.Name != "" {
			name = existing.Name
		}
		if info == nil && existing.Info != nil {
			info = existing.Info
		}
	} else if name == "" {
		name = shortID(deviceID)
	}
	s.peers[deviceID] = discovery.Peer{DeviceID: deviceID, Name: name, Addr: resolvedAddr, Port: port, Info: info}
}

// AddPeerIfAbsent inserts p only when no entry for p.DeviceID exists yet — used
// to re-surface an unpaired peer as discoverable without clobbering a fresher
// existing record.
func (s *State) AddPeerIfAbsent(p discovery.Peer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.peers[p.DeviceID]; !ok {
		s.peers[p.DeviceID] = p
	}
}

// SetPeerNameInfo upserts a peer's name/info (was the onPeerState body).
func (s *State) SetPeerNameInfo(deviceID, name string, info *sysinfo.DeviceInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.peers[deviceID]
	p.DeviceID = deviceID
	if name != "" {
		p.Name = name
	}
	if info != nil {
		p.Info = info
	}
	s.peers[deviceID] = p
}

// SetPeerNameIfEmpty seeds a peer's name only when it has none yet (used to
// pre-populate names from ST at startup and from BEP ClusterConfig).
func (s *State) SetPeerNameIfEmpty(deviceID, name string) {
	if name == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.peers[deviceID]
	p.DeviceID = deviceID
	if p.Name == "" {
		p.Name = name
	}
	s.peers[deviceID] = p
}

// ── folder acceptance mutators ───────────────────────────────────────────────

// ReplaceFolderAccepted swaps in a freshly-built BEP-acceptance map (atomic).
func (s *State) ReplaceFolderAccepted(next map[string]map[string]bool) {
	s.mu.Lock()
	s.folderAccepted = next
	s.mu.Unlock()
}

// ReplaceFolderPeerInfo swaps in a freshly-built per-(folder,device) completion
// map (atomic). Built by RefreshFolderCompletion from ST's completion sweep.
func (s *State) ReplaceFolderPeerInfo(next map[string]map[string]FolderPeerInfo) {
	s.mu.Lock()
	s.folderPeer = next
	s.mu.Unlock()
}

// FolderPeerInfoFor returns a copy of the cached completion view for one folder.
func (s *State) FolderPeerInfoFor(folderID string) map[string]FolderPeerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.folderPeer[folderID]
	out := make(map[string]FolderPeerInfo, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// ReplaceDeviceLastSeen swaps in a fresh per-device last-seen map (atomic).
func (s *State) ReplaceDeviceLastSeen(next map[string]time.Time) {
	s.mu.Lock()
	s.deviceLastSeen = next
	s.mu.Unlock()
}

// DeviceLastSeen returns ST's last-seen time for one device (zero, false if unknown).
func (s *State) DeviceLastSeen(id string) (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.deviceLastSeen[id]
	return t, ok
}

// SetWireAccepted records a peerwire FolderAccept for (folderID, deviceID).
func (s *State) SetWireAccepted(folderID, deviceID string, v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wireAccepted[folderID] == nil {
		s.wireAccepted[folderID] = map[string]bool{}
	}
	s.wireAccepted[folderID][deviceID] = v
}

// ResetAccepted clears both wire and BEP acceptance for a (folder, device) — used
// when a device is newly (re-)added so stale acceptance doesn't carry over.
func (s *State) ResetAccepted(folderID, deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.wireAccepted[folderID], deviceID)
	if s.folderAccepted[folderID] != nil {
		s.folderAccepted[folderID][deviceID] = false
	}
}

// ── composite operations (mirror the old multi-step locked blocks) ───────────

// TrustSignalOutcome reports the side effects a peer trust signal requires; the
// caller performs them OUTSIDE the state lock (they touch ST / the wire).
type TrustSignalOutcome struct {
	// ReassertRemoval: peer (re)asserts trust but we explicitly removed them —
	// show them as incoming and re-send trusted:false so they know.
	ReassertRemoval bool
	// DismissPending: peer withdrew trust and we had recorded their trust —
	// clear them from ST's pending list.
	DismissPending bool
	// CascadeUntrust: ...and we still trusted them — perform a full untrust.
	CascadeUntrust bool
}

// OnPeerTrustSignal applies a peer's wire-declared trust state to theyTrustUs and
// returns what the caller must do next (was the locked core of Handlers.onTrusted).
func (s *State) OnPeerTrustSignal(id string, trusted bool) TrustSignalOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out TrustSignalOutcome
	if trusted {
		s.theyTrustUs[id] = true
		out.ReassertRemoval = s.explicitlyRemoved[id]
		return out
	}
	// trusted == false
	hadTrust := s.theyTrustUs[id]
	delete(s.theyTrustUs, id)
	if hadTrust {
		out.DismissPending = true
		out.CascadeUntrust = s.trustedIDs[id]
	}
	return out
}

// ReconcileTrust aligns trusted/theyTrustUs/peer-names with ST's device list
// (picking up Introducer-added devices), and returns the explicitly-removed
// devices ST still lists so the caller can drop them from folders outside the
// lock (was the locked core of Handlers.updateTrust).
func (s *State) ReconcileTrust(devices []DeviceName, introduced map[string]bool, selfID string) (toRemove []string) {
	stSet := make(map[string]bool, len(devices))
	for _, d := range devices {
		if d.ID != selfID {
			stSet[d.ID] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range devices {
		if d.ID == selfID {
			continue
		}
		if !s.trustedIDs[d.ID] && !s.explicitlyRemoved[d.ID] {
			s.trustedIDs[d.ID] = true
			if introduced[d.ID] {
				// Auto-introduced via ST Introducer — mark mutually trusted now;
				// no trust-request card. Wire confirms when they connect.
				s.theyTrustUs[d.ID] = true
			}
		}
		if d.Name != "" {
			if p := s.peers[d.ID]; p.Name == "" {
				p.DeviceID = d.ID
				p.Name = d.Name
				s.peers[d.ID] = p
			}
		}
	}
	for id := range s.explicitlyRemoved {
		if stSet[id] {
			toRemove = append(toRemove, id)
		}
	}
	return toRemove
}

// ── helpers ──────────────────────────────────────────────────────────────────

func copyBool(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func shortID(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

// IsRoutableAddr reports whether addr is usable for an outbound connection:
// not link-local, APIPA, or private (unique-local fd::/8) IPv6.
func IsRoutableAddr(addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 == nil && len(ip) == 16 && ip[0] == 0xfd {
		return false
	}
	return true
}
