package api

import (
	"crypto/tls"
	"log"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"wesync/internal/discovery"
	"wesync/internal/node"
	"wesync/internal/peerwire"
	"wesync/internal/store"
	"wesync/internal/syncthing"
	"wesync/internal/sysinfo"
)

// SyncNowHook is set by the mobile wrapper at startup to expose the
// power-gate's manual trigger. nil on desktop (no power gate). The
// /api/power/sync-now handler is a no-op when not registered.
var SyncNowHook func()

// GateStatusHook returns a JSON snapshot of the power gate's current
// observed state. Mobile sets it at startup; desktop leaves it nil and
// /api/power/status returns an empty object.
var GateStatusHook func() string

// FoldersChangedHook is set by the mobile wrapper to re-apply per-folder ST
// config (notably the on-change debounce / fsWatcherDelayS) after a folder is
// added. The gate otherwise only pushes the debounce on a settings change and
// at startup, so a folder created/accepted afterward kept ST's default 10s
// delay until an app restart. nil on desktop. Best-effort — never blocks the
// request.
var FoldersChangedHook func()

func foldersChanged() {
	if FoldersChangedHook != nil {
		FoldersChangedHook()
	}
}

type modeRequest struct {
	Visible bool `json:"visible"` // true = announcing via UDP (others can discover us)
}

type pairRequest struct {
	DeviceID string `json:"deviceID"`
	Name     string `json:"name"`
}

// DeviceWithStatus is a known client with its live connection and pairing state.
type DeviceWithStatus struct {
	DeviceID  string `json:"deviceID"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	STPaired  bool   `json:"stPaired"` // true = explicitly paired by user
	Accepted  bool   `json:"accepted"` // true = device has connected via BEP (lastSeen non-empty)
	// LastSeen is ST's last contact time (RFC3339), used to anchor an offline
	// peer's honest "in sync as of <time>" instead of an eternal "up to date".
	// Empty when never seen / unknown.
	LastSeen string              `json:"lastSeen,omitempty"`
	Info     *sysinfo.DeviceInfo `json:"info,omitempty"`
}

// IncomingRequest is a pair request received from a peer via WS hello.
type IncomingRequest struct {
	DeviceID string `json:"deviceID"`
	Name     string `json:"name"`
}

type wsState struct {
	MyID           string                    `json:"myID"`
	Name           string                    `json:"name"`
	Devices        []DeviceWithStatus        `json:"devices"`
	Incoming       []IncomingRequest         `json:"incoming"`
	Outgoing       map[string]string         `json:"outgoing"`
	Visible        bool                      `json:"visible"`   // announcing via UDP (others can discover us)
	Listening      bool                      `json:"listening"` // receiving UDP announcements (we can discover others)
	PendingFolders []syncthing.PendingFolder `json:"pendingFolders"`
	Folders        []store.FolderWithDevices `json:"folders"`
}

// discoveryService is the subset of discovery.Service used by Handlers.
type discoveryService interface {
	IsListening() bool       // true = UI open (foreground), listening for peers
	WantAnnounce() bool      // the user's discoverability preference
	SetWantAnnounce(bool)    // visibility toggle: sets the preference (the only writer)
	SetForeground(bool)      // UI open/close gate (the only writer)
	PresentAddr(string) bool // are we CURRENTLY hearing this source IP announce? (gates discoverable)
}

// Handlers holds all WeSync application state and wires together the
// database, Syncthing client, peer discovery, and browser WebSocket hub.
type Handlers struct {
	st   syncthing.Backend
	db   *store.Store
	disc discoveryService
	hub  *Hub
	wire *peerwire.Hub

	selfID string

	// state owns all mutable in-memory node state — peers, trust sets, folder
	// acceptance, and this node's name — behind its own lock (see internal/node).
	// Every read returns a copy, so the concurrent-map-access crash class is gone
	// by construction. Handlers is now transport + orchestration on top of it.
	state *node.State

	// completionRefreshing prevents concurrent RefreshFolderCompletion runs.
	// If an event fires while a refresh is already running, we skip rather than
	// pile up N×M HTTP calls to ST.
	completionRefreshing atomic.Bool

	// folderPeerProgress[folderID][deviceID] tracks per-peer sync progress across
	// completion sweeps (last seen needBytes + when it last dropped) so
	// RefreshFolderCompletion can derive a "stalled" flag — B needs data from us
	// but nothing has flowed for a while. Rebuilt each sweep, so it self-prunes.
	folderPeerProgress   map[string]map[string]peerProgress
	folderPeerProgressMu sync.Mutex

	// active is true while the UI is open (≥1 WS client) — i.e. foreground.
	// It gates peerwire: when false we keep wire quiet (no outbound maintenance)
	// because wire is only WeSync's control plane (pairing, folder offers,
	// names). File sync is unaffected — Syncthing connects on its own over the
	// LAN regardless. Set via SetActive from the hub's active-change callback.
	active atomic.Bool

	pushCh         chan struct{}
	pipelineCh     chan struct{} // single-item queue: at most one pending pipeline run
	pendingFolders *folderPendingCache
}

func NewHandlers(st syncthing.Backend, db *store.Store, selfID, name string, port, stPort int, selfInfo sysinfo.DeviceInfo, tlsCert *tls.Certificate, disc discoveryService, hub *Hub) *Handlers {
	h := &Handlers{
		st:             st,
		db:             db,
		selfID:         selfID,
		disc:           disc,
		hub:            hub,
		state:          node.New(),
		pushCh:         make(chan struct{}, 1),
		pipelineCh:     make(chan struct{}, 1),
		pendingFolders: &folderPendingCache{},
	}
	h.state.SetName(name)
	// Populate trustedIDs from ST — ST's configured device list is the source of truth.
	// "Other devices" (untrusted MESH participants) are NOT added to ST's global config
	// and therefore don't appear here. They come via pending/devices in listFolders().
	if stDevs, err := st.ListDevices(); err == nil {
		for _, d := range stDevs {
			if d.DeviceID != selfID {
				h.state.Trust(d.DeviceID)
			}
		}
		// Introducer flags are reconciled by updateTrust() (runs at boot via the
		// pipeline) per the introducedBy=="" invariant — see pipeline.go. We must
		// NOT blanket-flag every ST device here: that re-flags introduced devices
		// on every restart and cascades introducer trust across the mesh.
	}
	h.wire = peerwire.NewHub(selfID, name, port, stPort, selfInfo, tlsCert, peerwire.Callbacks{
		OnHello:     h.onHello,
		OnPeerState: h.onPeerState,
		OnAccepted:  h.onAccepted,
		OnCancelled: h.onCancelled,
		OnFolderRemove: func(fromDeviceID, folderID, targetDeviceID string) {
			// Accept from trusted devices OR known participants in the specific folder.
			if !h.isTrusted(fromDeviceID) && !h.isFolderParticipant(fromDeviceID, folderID) {
				log.Printf("SECURITY: ignoring FolderRemove from non-participant %s", shortID(fromDeviceID))
				return
			}
			if targetDeviceID == "" {
				// We were kicked. Drop the kicker from our folder; in a mesh the
				// folder keeps syncing with the remaining peers. If they were the
				// only peer (2-device share, the common case), the folder becomes
				// a single-participant island that isn't a sync relationship
				// anymore — remove it from ST so it stops showing up in
				// WeSync's folder list. Files on disk are untouched: ST's
				// RemoveFolder only drops the folder config, not the directory.
				dbg("folder: ← kicked from %.8s by %s", folderID, shortID(fromDeviceID))
				h.removeDeviceFromFolderInST(folderID, fromDeviceID)
				if len(folderParticipants(h.listFolders(), folderID)) == 0 {
					if err := h.st.RemoveFolder(folderID); err != nil {
						log.Printf("OnFolderRemove kicked: RemoveFolder %s: %v", shortID(folderID), err)
					} else {
						dbg("folder: ← left empty after kick — dropped from ST (files on disk preserved)")
					}
				}
			} else {
				dbg("folder: ← %s removed from %.8s by %s", shortID(targetDeviceID), folderID, shortID(fromDeviceID))
				h.removeDeviceFromFolderInST(folderID, targetDeviceID)
			}
			h.SchedulePush()
		},
		OnFolderAccept: func(fromDeviceID, folderID, _ string) {
			// Peer accepted our folder invite — mark them as accepted via wire (reliable, session-specific).
			// No security check needed: TLS cert already authenticated the sender, and marking
			// someone as accepted is harmless (only affects our display state).
			if fromDeviceID == "" || fromDeviceID == h.selfID {
				return
			}
			dbg("folder: ← %s accepted %.8s via wire", shortID(fromDeviceID), folderID)
			h.state.SetWireAccepted(folderID, fromDeviceID, true)
			h.SchedulePush()
		},
		OnFolderDecline: func(fromDeviceID, folderID string) {
			// Peer declined our folder invite — remove them from the folder.
			if !h.isTrusted(fromDeviceID) && !h.isFolderParticipant(fromDeviceID, folderID) {
				log.Printf("SECURITY: ignoring FolderDecline from non-participant %s", shortID(fromDeviceID))
				return
			}
			log.Printf("peer %s declined folder %s — removing from folder", shortID(fromDeviceID), shortID(folderID))
			h.removeDeviceFromFolderInST(folderID, fromDeviceID)
			h.SchedulePush()
		},
		OnPeerVerified: func(deviceID, certFP string) {
			// Store cert fingerprint in memory only — no DB write here.
			// clients table is written only when a folder is actually shared.
			h.state.SetPeerCertFP(deviceID, certFP)
			log.Printf("peer %s TLS-verified (in-memory only)", shortID(deviceID))
		},
		// cert_fp IS the identity — a different cert means a different client, not an
		// impersonator. Always allow; onHello will create a separate client record.
		OnValidateCertFP: func(_, _ string) bool { return true },
		OnTrusted:        h.onTrusted,
	}, func() []string {
		return nil // outgoing list removed — pairing is ST-direct, no wire signaling
	}, h.isTrusted, h.isExplicitlyRemoved, h.isMutuallyTrusted)
	// Pre-populate peers from ST so names are available before wire PeerState arrives.
	if stDevices, err := st.ListDevices(); err == nil {
		for _, d := range stDevices {
			h.state.SetPeerNameIfEmpty(d.DeviceID, d.Name)
		}
	}
	go h.pushWorker()
	go h.pipelineWorker()
	return h
}

// listFolders returns all folders from Syncthing with per-device accepted status.
// Device list comes entirely from ST's folder config — source of truth via BEP.
//
// Always returns a non-nil slice so the API serializes as `[]` rather than
// `null` even when ST is transiently unreachable (e.g. mid-restart). The
// frontend distinguishes "no folders" from "request failed" via HTTP status,
// not via null payloads.
func (h *Handlers) listFolders() []store.FolderWithDevices {
	stFolders, err := h.st.ListFolders()
	if err != nil {
		return []store.FolderWithDevices{}
	}

	// BEPLive comes from ST's connection set — the real sync link — exactly like
	// the device list's `connected` (see snapshot()). Earlier this used peerwire
	// liveness (h.wire.PeerAddr), which flips down whenever the ephemeral setup
	// channel drops even though ST is syncing fine — making a device read
	// "connected" on the Devices page but "offline" on the folder/folder-detail
	// pages. Both pages must reflect the same authoritative signal. Per-folder
	// /rest/db/completion would be more precise but costs a round-trip per
	// (folder, device) on every push; device-level ST connection is the same
	// proxy the device list already trusts.
	stConnected, _ := h.st.ConnectedDeviceIDs()

	result := make([]store.FolderWithDevices, 0, len(stFolders))
	for _, f := range stFolders {
		deviceIDs := make([]string, 0, len(f.Devices))
		for _, d := range f.Devices {
			if d.DeviceID != h.selfID {
				deviceIDs = append(deviceIDs, d.DeviceID)
			}
		}
		wireAcc := h.state.WireAccepted(f.ID)
		remoteSeq := h.state.FolderAccepted(f.ID)
		peerInfo := h.state.FolderPeerInfoFor(f.ID)
		trustedSnap := h.state.Trusted()
		accepted := make(map[string]bool, len(deviceIDs))
		trusted := make(map[string]bool, len(deviceIDs))
		states := make(map[string]string, len(deviceIDs))
		devicePeer := make(map[string]store.PeerDetail, len(deviceIDs))
		for _, did := range deviceIDs {
			pi := peerInfo[did]
			state := node.DeriveFolderRelationState(node.FolderRelationDimensions{
				InDeviceList:      true,
				InRemoteSequence:  remoteSeq[did],
				WireAccepted:      wireAcc[did],
				BEPLive:           stConnected[did],
				RemoteStatePaused: pi.RemoteState == "paused",
				PeerNeed:          pi.Need,
				PeerStalled:       pi.Stalled,
			})
			accepted[did] = state.IsAccepted()
			states[did] = string(state)
			trusted[did] = trustedSnap[did]
			if pi.Need || pi.Completion > 0 {
				devicePeer[did] = store.PeerDetail{NeedBytes: pi.NeedBytes, NeedItems: pi.NeedItems, Completion: pi.Completion}
			}
		}
		result = append(result, store.FolderWithDevices{
			Folder:         store.Folder{ID: f.ID, Label: f.Label, Path: f.Path, Type: f.Type},
			DeviceIDs:      deviceIDs,
			DeviceAccepted: accepted,
			DeviceTrusted:  trusted,
			DeviceState:    states,
			DevicePeer:     devicePeer,
		})
	}
	return result
}

// isFolderParticipant returns true if deviceID is listed in the given folder.
func (h *Handlers) isFolderParticipant(deviceID, folderID string) bool {
	for _, f := range h.listFolders() {
		if f.ID != folderID {
			continue
		}
		for _, did := range f.DeviceIDs {
			if did == deviceID {
				return true
			}
		}
	}
	return false
}

// isTrusted returns true if deviceID has been explicitly paired (in-memory set, source: ST).
func (h *Handlers) isTrusted(deviceID string) bool {
	return h.state.IsTrusted(deviceID)
}

// isMutuallyTrusted returns true when both sides trust each other:
// we have them in trustedIDs AND they sent us trusted=true via wire.
func (h *Handlers) isMutuallyTrusted(deviceID string) bool {
	return h.state.IsMutuallyTrusted(deviceID)
}

// isExplicitlyRemoved returns true if the device was explicitly removed and should
// receive trusted:false on next wire Hello (handles the offline removal case).
func (h *Handlers) isExplicitlyRemoved(deviceID string) bool {
	return h.state.IsExplicitlyRemoved(deviceID)
}

// trustDevice adds a device to the trusted set (ST + memory).
// ST is the sole source of truth — no DB write needed.
func (h *Handlers) trustDevice(deviceID, name string) {
	if err := h.st.AddDevice(deviceID, name, ""); err != nil {
		log.Printf("trustDevice %s: %v", shortID(deviceID), err)
	}
	if err := h.st.UpdateDeviceIntroducer(deviceID, h.db.GetIntroducer()); err != nil {
		log.Printf("trustDevice %s: set introducer: %v", shortID(deviceID), err)
	}
	h.state.Trust(deviceID)
}

// untrustDevice removes a device from the trusted set (ST + memory).
func (h *Handlers) untrustDevice(deviceID string) {
	log.Printf("trust: removing %s from trusted set", shortID(deviceID))
	if err := h.st.RemoveDevice(deviceID); err != nil {
		log.Printf("untrustDevice %s: %v", shortID(deviceID), err)
	}
	h.state.Untrust(deviceID)
	// Signal the removed device so they clear our incoming request immediately.
	h.notify(deviceID, peerwire.Message{
		Type:     peerwire.Hello,
		DeviceID: h.selfID, // needed in no-TLS mode; cert-based mode ignores it
		Trusted:  boolPtr(false),
	})
}

func boolPtr(v bool) *bool { return &v }

// MaintainConnections ensures outbound peerwire connections exist for all known
// clients using their last recorded address. Falls back to UDP-discovered
// addresses when the stored address is absent or stale.
// Call this after pairing changes and from the reconnect ticker.
// No-op while backgrounded (see SetActive): wire stays quiet, ST keeps syncing.
func (h *Handlers) MaintainConnections() {
	if !h.active.Load() {
		return
	}
	trustedIDs := h.state.Trusted()
	livePeers := h.state.Peers()
	if len(trustedIDs) == 0 {
		return
	}

	stAddrs, _ := h.st.GetConnectedAddresses()
	selfPort := h.wire.SelfPort()

	for id := range trustedIDs {
		if live, ok := livePeers[id]; ok && live.Addr != "" && live.Port != 0 {
			h.wire.Connect(id, live.Addr, live.Port)
			continue
		}
		if ip, ok := stAddrs[id]; ok && ip != "" {
			h.wire.Connect(id, ip, selfPort)
		}
	}
}

// SetForeground switches the whole node between foreground (UI visible) and
// background (UI hidden): UDP announce+listen and peerwire all on or all off.
// Background goes fully silent immediately — no grace — so peers see us drop at
// once. File sync is unaffected: Syncthing connects on its own over the LAN.
func (h *Handlers) SetForeground(active bool) {
	h.disc.SetForeground(active)
	h.SetActive(active)
	// Push the new visible/listening state so the UI reflects it immediately —
	// otherwise reopening the window leaves it showing the stale "off" snapshot.
	h.SchedulePush()
}

// SetActive switches peerwire between foreground (full) and background (quiet).
// Foreground: accept inbound again and (re)establish outbound connections to
// trusted peers. Background: stop accepting AND close every connection — both
// inbound and outbound — so the wire goes fully silent and a peer can't keep us
// connected. Wire is only WeSync's control plane, so going quiet costs nothing
// for file sync, which Syncthing handles on its own.
func (h *Handlers) SetActive(active bool) {
	h.active.Store(active)
	if active {
		h.wire.SetAccepting(true)
		go h.MaintainConnections()
	} else {
		h.wire.SetAccepting(false) // reject + close inbound
		h.wire.DisconnectAll()     // close outbound
	}
}

// Active is PUT /api/active {active bool}. The desktop app calls it the instant
// its window is hidden to tray (active:false) or re-shown (active:true) — a
// reliable foreground signal that the UI WebSocket count can't give us, since
// the webview stays connected while hidden.
func (h *Handlers) Active(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Active bool `json:"active"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Active {
		log.Printf("active: foreground (UI visible) — discovery + wire on")
	} else {
		log.Printf("active: background (UI hidden) — discovery + wire off, no grace")
	}
	h.SetForeground(req.Active)
	w.WriteHeader(http.StatusNoContent)
}

// Connectivity gets or sets the connectivity level (GET/PUT /api/connectivity).
func (h *Handlers) Connectivity(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]int{"level": h.db.GetConnectivityLevel()})
	case http.MethodPut:
		var req struct {
			Level int `json:"level"`
		}
		if err := decodeJSON(r, &req); err != nil || req.Level < 1 || req.Level > 3 {
			http.Error(w, "level must be 1, 2 or 3", http.StatusBadRequest)
			return
		}
		if err := h.db.SetConnectivityLevel(req.Level); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := h.st.SetConnectivityLevel(req.Level); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		log.Printf("connectivity level set to %d", req.Level)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ConnectivityStatus reports the live relay AND global-discovery health in one
// payload (GET /api/connectivity-status), derived from a single ST system-status
// read. The ConnectivitySection polls this at levels 2-3 so the card can show
// whether the device is actually announcing / relaying, not merely enabled.
func (h *Handlers) ConnectivityStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cs, err := h.st.ConnectivityStatus()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, cs)
}

// PowerEvents returns the most recent power-gate audit-trail entries —
// useful for users who want to verify that the autonomous wake/sync/sleep
// loop actually ran while the app was closed. Capped at the store's
// internal max (~200) regardless of `?limit=`.
func (h *Handlers) PowerEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	events, err := h.db.ListPowerEvents(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, events)
}

// PowerStatus returns a JSON snapshot of the power gate's current
// observed state (network, battery, charging, foreground, etc.). The
// frontend's StatusPanel polls this so the user can see at a glance
// which conditions are currently met. On desktop the gate doesn't run;
// we return `{}`.
func (h *Handlers) PowerStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if GateStatusHook == nil {
		_, _ = w.Write([]byte("{}"))
		return
	}
	_, _ = w.Write([]byte(GateStatusHook()))
}

// PowerSyncNow opens a trigger window immediately — same effect as an
// AlarmManager tick. Used by the "Sync now" button so the user can
// verify the gate end-to-end without waiting for the next interval.
func (h *Handlers) PowerSyncNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Mobile registers the hook on startup; desktop leaves it nil.
	// Either way the call is best-effort — folder picker / sync logic
	// still works without it.
	if SyncNowHook != nil {
		SyncNowHook()
	}
	w.WriteHeader(http.StatusNoContent)
}

// Power is the GET/PUT endpoint for the Android wrapper's power-management
// preferences (sync trigger, network gate, battery, charging). Settings live
// in the same wesync.db row as the rest of Settings; the gate inside the
// mobile package re-reads them when the wrapper calls RefreshPowerSettings
// after a successful PUT.
func (h *Handlers) Power(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		p, err := h.db.GetPowerSettings()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, p)
	case http.MethodPut:
		var p store.PowerSettings
		if err := decodeJSON(r, &p); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		// Defensive validation — clamp / fix common mistakes so the gate
		// can rely on these values without re-checking.
		switch p.SyncTrigger {
		case "periodic", "scheduled", "on_change", "on_change_poll":
		default:
			p.SyncTrigger = "periodic"
		}
		switch p.NetworkMode {
		case "any", "any_wifi", "trusted_wifi":
		default:
			p.NetworkMode = "any_wifi"
		}
		if p.PeriodicMinutes <= 0 {
			p.PeriodicMinutes = 120
		}
		if p.OnChangeDebounceMinutes <= 0 {
			p.OnChangeDebounceMinutes = 5
		}
		if p.ScheduledTimes == nil {
			p.ScheduledTimes = []string{}
		}
		if p.TrustedSSIDs == nil {
			p.TrustedSSIDs = []string{}
		}
		if err := h.db.SetPowerSettings(p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) Status(w http.ResponseWriter, r *http.Request) {
	name := h.state.Name()
	writeJSON(w, struct {
		MyID      string `json:"myID"`
		Name      string `json:"name"`
		BuildTime string `json:"buildTime"`
	}{MyID: h.selfID, Name: name, BuildTime: BuildTime})
}

func (h *Handlers) Mode(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, modeRequest{Visible: h.disc.WantAnnounce()})
	case http.MethodPut:
		var req modeRequest
		if err := decodeJSON(r, &req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		// "visible" is the user's discoverability PREFERENCE — the only writer of it.
		// Actual UDP announce is computed as preference AND foreground in the service,
		// so this never fights the lifecycle gate. Persist FIRST and apply to the live
		// service only if the write succeeds, so disk and runtime never disagree (a
		// failed write that still changed runtime would silently revert on restart).
		if err := h.db.SetVisible(req.Visible); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.disc.SetWantAnnounce(req.Visible)
		if req.Visible {
			log.Printf("visible: now announcing via UDP — peers can discover us")
		} else {
			log.Printf("visible: stopped UDP announcing — listening passively for peers")
		}
		// No MaintainConnections here: this toggles only UDP announce visibility,
		// which has no bearing on outbound peerwire connections to already-trusted
		// peers (those are driven by foreground/SetActive). Re-dialing every trusted
		// device on each visibility tap was wasted work.
		w.WriteHeader(http.StatusNoContent)
		h.SchedulePush()
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) Name(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPut) {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Name == "" {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := h.db.SetName(req.Name); err != nil {
		log.Printf("Name: db.SetName: %v", err)
	}
	h.state.SetName(req.Name)

	h.wire.SetSelfName(req.Name)
	if err := h.st.UpdateDevice(h.selfID, req.Name); err != nil {
		log.Printf("Name: st.UpdateDevice: %v", err)
	}
	h.wire.BroadcastPeerState()
	w.WriteHeader(http.StatusNoContent)
	h.SchedulePush()
}

// notify sends a wire message to a peer in a background goroutine.
// For non-critical notifications where delivery failure is acceptable.
func (h *Handlers) notify(deviceID string, msg peerwire.Message) {
	go h.wire.SendSync(deviceID, msg, 5*time.Second) //nolint:errcheck
}

func (h *Handlers) Peers(w http.ResponseWriter, r *http.Request) {
	peers := h.state.Peers()
	list := make([]discovery.Peer, 0, len(peers))
	for _, p := range peers {
		list = append(list, p)
	}
	writeJSON(w, list)
}
