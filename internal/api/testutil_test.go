package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"wesync/internal/discovery"
	"wesync/internal/store"
	"wesync/internal/syncthing"
	"wesync/internal/sysinfo"
)

// ── mock Syncthing backend ─────────────────────────────────────────────────────

type mockSyncthing struct {
	mu                 sync.Mutex
	devices            []syncthing.Device
	folders            []syncthing.Folder
	pending            []syncthing.PendingDevice
	pendingFolders     []syncthing.PendingFolder
	connected          map[string]bool
	ignorePatterns     map[string][]string // folderID → patterns
	connectivityStatus syncthing.ConnectivityStatus
}

func newMockSyncthing() *mockSyncthing {
	return &mockSyncthing{
		connected:      make(map[string]bool),
		ignorePatterns: make(map[string][]string),
	}
}

func (m *mockSyncthing) setIgnorePatterns(folderID string, patterns []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ignorePatterns[folderID] = patterns
}

func (m *mockSyncthing) ListDevices() ([]syncthing.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]syncthing.Device, len(m.devices))
	copy(out, m.devices)
	return out, nil
}

func (m *mockSyncthing) GetConnectedAddresses() (map[string]string, error) {
	return map[string]string{}, nil
}

func (m *mockSyncthing) UpdateDevice(id, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, d := range m.devices {
		if d.DeviceID == id {
			m.devices[i].Name = name
			return nil
		}
	}
	return nil
}

func (m *mockSyncthing) ConnectedDeviceIDs() (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]bool, len(m.connected))
	for k, v := range m.connected {
		out[k] = v
	}
	return out, nil
}

func (m *mockSyncthing) DeviceCompletion(_, _ string) (syncthing.PeerNeed, error) {
	return syncthing.PeerNeed{RemoteState: "valid"}, nil
}

func (m *mockSyncthing) DeviceLastSeen() (map[string]time.Time, error) {
	return map[string]time.Time{}, nil
}

func (m *mockSyncthing) AddDevice(id, name, addr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.devices {
		if d.DeviceID == id {
			return nil
		}
	}
	m.devices = append(m.devices, syncthing.Device{DeviceID: id, Name: name, Addresses: []string{addr}})
	return nil
}

func (m *mockSyncthing) RemoveDevice(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, d := range m.devices {
		if d.DeviceID == id {
			m.devices = append(m.devices[:i], m.devices[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *mockSyncthing) PendingDevices() ([]syncthing.PendingDevice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]syncthing.PendingDevice, len(m.pending))
	copy(out, m.pending)
	return out, nil
}

func (m *mockSyncthing) GetFolderStatus(_ string) (syncthing.FolderStatus, error) {
	return syncthing.FolderStatus{State: "idle"}, nil
}

func (m *mockSyncthing) GetFolderIgnores(folderID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ignorePatterns[folderID], nil
}
func (m *mockSyncthing) SetFolderIgnores(folderID string, patterns []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ignorePatterns[folderID] = patterns
	return nil
}

func (m *mockSyncthing) SetFolderPaused(_ string, _ bool) error { return nil }
func (m *mockSyncthing) GetFolderPaused(_ string) (bool, error) { return false, nil }
func (m *mockSyncthing) RescanFolder(_ string) error            { return nil }
func (m *mockSyncthing) RevertFolder(_ string) error            { return nil }
func (m *mockSyncthing) SetConnectivityLevel(_ int) error       { return nil }
func (m *mockSyncthing) ConnectivityStatus() (syncthing.ConnectivityStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connectivityStatus, nil
}
func (m *mockSyncthing) PauseDevice(_ string) error                    { return nil }
func (m *mockSyncthing) ResumeDevice(_ string) error                   { return nil }
func (m *mockSyncthing) UpdateDeviceIntroducer(id string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, d := range m.devices {
		if d.DeviceID == id {
			m.devices[i].Introducer = enabled
			return nil
		}
	}
	return nil
}

func (m *mockSyncthing) ListFolders() ([]syncthing.Folder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]syncthing.Folder, len(m.folders))
	copy(out, m.folders)
	return out, nil
}

func (m *mockSyncthing) AddFolder(f syncthing.Folder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.folders = append(m.folders, f)
	return nil
}

func (m *mockSyncthing) UpdateFolder(f syncthing.Folder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, existing := range m.folders {
		if existing.ID == f.ID {
			m.folders[i] = f
			return nil
		}
	}
	return nil
}

func (m *mockSyncthing) RemoveFolder(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, f := range m.folders {
		if f.ID == id {
			m.folders = append(m.folders[:i], m.folders[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *mockSyncthing) PendingFolders() ([]syncthing.PendingFolder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]syncthing.PendingFolder, len(m.pendingFolders))
	copy(out, m.pendingFolders)
	return out, nil
}

func (m *mockSyncthing) DismissPendingFolder(folderID, deviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, pf := range m.pendingFolders {
		if pf.FolderID == folderID && pf.DeviceID == deviceID {
			m.pendingFolders = append(m.pendingFolders[:i], m.pendingFolders[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *mockSyncthing) addPendingFolder(folderID, label, deviceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pendingFolders = append(m.pendingFolders, syncthing.PendingFolder{
		FolderID: folderID, Label: label, DeviceID: deviceID,
	})
}

func (m *mockSyncthing) DismissPendingDevice(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, p := range m.pending {
		if p.DeviceID == id {
			m.pending = append(m.pending[:i], m.pending[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *mockSyncthing) addPending(id, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending = append(m.pending, syncthing.PendingDevice{DeviceID: id, Name: name})
}

func (m *mockSyncthing) setConnected(id string, up bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected[id] = up
}

func (m *mockSyncthing) hasPending(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.pending {
		if p.DeviceID == id {
			return true
		}
	}
	return false
}

func (m *mockSyncthing) hasDevice(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.devices {
		if d.DeviceID == id {
			return true
		}
	}
	return false
}

// ── mock discovery service ────────────────────────────────────────────────────

type mockDiscovery struct{ want, fg atomic.Bool }

func (m *mockDiscovery) IsListening() bool         { return m.fg.Load() }
func (m *mockDiscovery) WantAnnounce() bool        { return m.want.Load() }
func (m *mockDiscovery) SetWantAnnounce(v bool)    { m.want.Store(v) }
func (m *mockDiscovery) SetForeground(v bool)      { m.fg.Store(v) }
func (m *mockDiscovery) PresentAddr(_ string) bool { return true }

// ── test instance (one WeSync node) ──────────────────────────────────────────

type instance struct {
	id       string
	name     string
	st       *mockSyncthing
	disc     *mockDiscovery
	handlers *Handlers
	srv      *httptest.Server
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	return db
}

func newInstance(t *testing.T, id, name string) *instance {
	t.Helper()
	st := newMockSyncthing()
	disc := &mockDiscovery{}
	disc.SetWantAnnounce(true)
	disc.SetForeground(true)
	hub := NewHub()
	h := NewHandlers(st, newTestStore(t), id, name, 0, 0, sysinfo.DeviceInfo{}, nil, disc, hub)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pair", h.Pair)
	mux.HandleFunc("/api/active", h.Active)
	mux.HandleFunc("/api/incoming", h.Incoming)
	mux.HandleFunc("/api/devices", h.Devices)
	mux.HandleFunc("/api/name", h.Name)
	mux.HandleFunc("/api/folder/share", h.FolderShare)
	mux.HandleFunc("/api/folder/accept", h.FolderAccept)
	mux.HandleFunc("/api/folder/decline", h.FolderDecline)
	mux.HandleFunc("/api/folder/device", h.FolderRemoveDevice)
	mux.HandleFunc("/api/folder/list", h.FolderList)
	mux.HandleFunc("/api/folders", h.FolderList)
	mux.HandleFunc("/api/folder/ignores", h.FolderIgnoresHandler)
	mux.HandleFunc("/api/folder/conflicts", h.FolderConflictsList)
	mux.HandleFunc("/api/folder/conflict", h.FolderConflictDelete)
	mux.HandleFunc("/api/folder", h.FolderRemove)
	mux.HandleFunc("/peer/ws", h.wire.ServeWS)
	mux.HandleFunc("/api/ws", hub.ServeWS(h))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &instance{id: id, name: name, st: st, disc: disc, handlers: h, srv: srv}
}

func (inst *instance) port() int {
	u, _ := url.Parse(inst.srv.URL)
	p, _ := strconv.Atoi(u.Port())
	return p
}

func (inst *instance) addr() string {
	u, _ := url.Parse(inst.srv.URL)
	return u.Hostname()
}

// trackPeer registers other as a known UDP peer on this instance.
func (inst *instance) trackPeer(other *instance) {
	inst.handlers.TrackPeer(discovery.Peer{
		SID:      other.id, // use device ID as SID in tests so adoption is immediate
		DeviceID: other.id,
		Name:     other.name,
		Addr:     other.addr(),
		Port:     other.port(),
	})
}

// seedDevice adds a trusted device directly, simulating a previously paired device
// without going through the full pair HTTP flow.
func (inst *instance) seedDevice(id, name string) {
	inst.handlers.trustDevice(id, name)
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func doPair(t *testing.T, from *instance, targetID, targetName string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"deviceID": targetID, "name": targetName})
	resp, err := http.Post(from.srv.URL+"/api/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("pair request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("pair: expected 204, got %d", resp.StatusCode)
	}
}

func doRemoveDevice(t *testing.T, from *instance, targetID string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, from.srv.URL+"/api/devices?id="+targetID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("remove device failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove: expected 204, got %d", resp.StatusCode)
	}
}

func doRename(t *testing.T, inst *instance, name string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name})
	req, _ := http.NewRequest(http.MethodPut, inst.srv.URL+"/api/name", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("rename failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("rename: expected 204, got %d", resp.StatusCode)
	}
}

func assertDeviceName(t *testing.T, inst *instance, id, wantName string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		for _, d := range getDevices(t, inst) {
			if d.DeviceID == id {
				if d.Name == wantName {
					return
				}
				if time.Now().After(deadline) {
					t.Errorf("%s: device %s name = %q, want %q", inst.name, id[:7], d.Name, wantName)
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Errorf("%s: device %s not found", inst.name, id[:7])
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func doIgnore(t *testing.T, from *instance, targetID string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete,
		from.srv.URL+"/api/incoming?id="+targetID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ignore failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("ignore: expected 204, got %d", resp.StatusCode)
	}
}

func getIncoming(t *testing.T, inst *instance) []IncomingRequest {
	t.Helper()
	resp, err := http.Get(inst.srv.URL + "/api/incoming")
	if err != nil {
		t.Fatalf("get incoming: %v", err)
	}
	defer resp.Body.Close()
	var out []IncomingRequest
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func getFolders(t *testing.T, inst *instance) []store.FolderWithDevices {
	t.Helper()
	resp, err := http.Get(inst.srv.URL + "/api/folder/list")
	if err != nil {
		t.Fatalf("get folders: %v", err)
	}
	defer resp.Body.Close()
	var out []store.FolderWithDevices
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func getDevices(t *testing.T, inst *instance) []DeviceWithStatus {
	t.Helper()
	resp, err := http.Get(inst.srv.URL + "/api/devices")
	if err != nil {
		t.Fatalf("get devices: %v", err)
	}
	defer resp.Body.Close()
	var out []DeviceWithStatus
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// ── assertion helpers ─────────────────────────────────────────────────────────

func assertDeviceExists(t *testing.T, inst *instance, id string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		for _, d := range getDevices(t, inst) {
			if d.DeviceID == id {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Errorf("%s: expected device %s to exist", inst.name, id[:7])
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertPendingHas(t *testing.T, inst *instance, id string) {
	t.Helper()
	// Incoming state is delivered via async WS hello — poll briefly.
	deadline := time.Now().Add(1000 * time.Millisecond)
	for {
		incoming := getIncoming(t, inst)
		for _, p := range incoming {
			if p.DeviceID == id {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Errorf("%s: expected %s in incoming, got %v", inst.name, id[:7], incoming)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertPendingEmpty(t *testing.T, inst *instance) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		incoming := getIncoming(t, inst)
		if len(incoming) == 0 {
			return
		}
		if time.Now().After(deadline) {
			ids := make([]string, len(incoming))
			for i, p := range incoming {
				ids[i] = fmt.Sprintf("%s(%s)", p.Name, p.DeviceID[:7])
			}
			t.Errorf("%s: expected incoming to be empty, got %v", inst.name, ids)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertDeviceConnected(t *testing.T, inst *instance, id string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		inst.handlers.SchedulePipeline() // trigger pipeline to refresh state
		for _, d := range getDevices(t, inst) {
			if d.DeviceID == id {
				if !d.Connected {
					t.Errorf("%s: device %s exists but not connected", inst.name, id[:7])
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Errorf("%s: device %s not found in devices list", inst.name, id[:7])
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertPeerName(t *testing.T, inst *instance, id, wantName string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		p, ok := inst.handlers.state.Peer(id)
		if ok && p.Name == wantName {
			return
		}
		if time.Now().After(deadline) {
			got := ""
			if ok {
				got = p.Name
			}
			t.Errorf("%s: peer %s name = %q, want %q", inst.name, id[:7], got, wantName)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertSyncthingHasDevice(t *testing.T, inst *instance, id string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		if inst.st.hasDevice(id) {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("%s: expected %s in Syncthing", inst.name, id[:7])
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertSyncthingNoDevice(t *testing.T, inst *instance, id string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		if !inst.st.hasDevice(id) {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("%s: expected %s absent from Syncthing", inst.name, id[:7])
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertNoDevice(t *testing.T, inst *instance, id string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		found := false
		for _, d := range getDevices(t, inst) {
			if d.DeviceID == id {
				found = true
				break
			}
		}
		if !found {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("%s: device %s should not be present", inst.name, id[:7])
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ── Folder HTTP helpers ───────────────────────────────────────────────────────

func doFolderAccept(t *testing.T, inst *instance, folderID, deviceID, path string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"folderID": folderID, "deviceID": deviceID,
		"path": path, "direction": "sendreceive",
	})
	resp, err := http.Post(inst.srv.URL+"/api/folder/accept", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("folder accept: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("folder accept: expected 204, got %d", resp.StatusCode)
	}
}

func doFolderDecline(t *testing.T, inst *instance, folderID, deviceID string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"folderID": folderID, "deviceID": deviceID})
	resp, err := http.Post(inst.srv.URL+"/api/folder/decline", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("folder decline: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("folder decline: expected 204, got %d", resp.StatusCode)
	}
}

func doFolderRemoveDevice(t *testing.T, inst *instance, folderID, deviceID string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("%s/api/folder/device?folderID=%s&deviceID=%s", inst.srv.URL, folderID, deviceID), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("folder remove device: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("folder remove device: expected 204, got %d", resp.StatusCode)
	}
}

func doFolderLeave(t *testing.T, inst *instance, folderID string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("%s/api/folder?id=%s", inst.srv.URL, folderID), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("folder leave: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("folder leave: expected 204, got %d", resp.StatusCode)
	}
}

// ── Folder assertion helpers ──────────────────────────────────────────────────

func folderByIDInList(folders []store.FolderWithDevices, folderID string) *store.FolderWithDevices {
	for i := range folders {
		if folders[i].ID == folderID {
			return &folders[i]
		}
	}
	return nil
}

func assertFolderHasDevice(t *testing.T, inst *instance, folderID, deviceID string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		f := folderByIDInList(getFolders(t, inst), folderID)
		if f != nil {
			for _, did := range f.DeviceIDs {
				if did == deviceID {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Errorf("%s: folder %s should contain device %s", inst.name, folderID[:8], deviceID[:7])
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertFolderMissingDevice(t *testing.T, inst *instance, folderID, deviceID string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		f := folderByIDInList(getFolders(t, inst), folderID)
		found := false
		if f != nil {
			for _, did := range f.DeviceIDs {
				if did == deviceID {
					found = true
					break
				}
			}
		}
		if !found {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("%s: folder %s should NOT contain device %s", inst.name, folderID[:8], deviceID[:7])
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertFolderExists(t *testing.T, inst *instance, folderID string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		if folderByIDInList(getFolders(t, inst), folderID) != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("%s: folder %s not found", inst.name, folderID[:8])
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertFolderNotExists(t *testing.T, inst *instance, folderID string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		if folderByIDInList(getFolders(t, inst), folderID) == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("%s: folder %s should not exist", inst.name, folderID[:8])
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
