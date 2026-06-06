package syncthing

type Folder struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Path    string         `json:"path"`
	Type    string         `json:"type"`
	Paused  bool           `json:"paused"`
	Devices []FolderDevice `json:"devices"`
}

type FolderDevice struct {
	DeviceID           string `json:"deviceID"`
	IntroducedBy       string `json:"introducedBy"`
	EncryptionPassword string `json:"encryptionPassword"`
}

func (c *Client) ListFolders() ([]Folder, error) {
	var cfg struct {
		Folders []Folder `json:"folders"`
	}
	return cfg.Folders, c.get("/rest/config", &cfg)
}

func (c *Client) AddFolder(f Folder) error {
	if f.Type == "" {
		f.Type = "sendreceive"
	}
	return c.post("/rest/config/folders", f, nil)
}

func (c *Client) UpdateFolder(f Folder) error {
	return c.patch("/rest/config/folders/"+f.ID, f)
}

func (c *Client) SetFolderPaused(id string, paused bool) error {
	return c.patch("/rest/config/folders/"+id, map[string]bool{"paused": paused})
}

// SetFolderFSWatcherDelay configures how long ST's per-folder filesystem
// watcher waits after the last change before triggering a scan. This is
// the on-change debounce — keeping folders unpaused while a long delay
// is set is how the power gate's "on_change" mode is supposed to work.
// `seconds` >= 1 (10 is ST's default).
func (c *Client) SetFolderFSWatcherDelay(id string, seconds int) error {
	if seconds < 1 {
		seconds = 10
	}
	return c.patch("/rest/config/folders/"+id, map[string]int{"fsWatcherDelayS": seconds})
}

// GetFolderPaused returns whether a folder is paused in Syncthing config.
// The db/status endpoint does not include paused state — it lives in config.
func (c *Client) GetFolderPaused(id string) (bool, error) {
	var cfg struct {
		Paused bool `json:"paused"`
	}
	return cfg.Paused, c.get("/rest/config/folders/"+id, &cfg)
}

func (c *Client) RemoveFolder(id string) error {
	return c.del("/rest/config/folders/" + id)
}

// RescanFolder triggers an immediate rescan of a folder in Syncthing.
// Call this after making changes to the folder's contents (e.g. creating .stfolder).
func (c *Client) RescanFolder(folderID string) error {
	return c.post("/rest/db/scan?folder="+folderID, nil, nil)
}

// RevertFolder undoes all local changes in a receive-only folder: locally
// edited files are overwritten with the cluster version and locally added files
// are deleted. Syncthing no-ops this for folders that aren't receive-only.
func (c *Client) RevertFolder(folderID string) error {
	return c.post("/rest/db/revert?folder="+folderID, nil, nil)
}

// FolderStatus is the live sync state of a folder from Syncthing.
//
// RemoteSequence is the persistent per-device sequence counter ST keeps from
// each remote's BEP cluster config. Critically: a device's presence as a key
// here means the device has at some point sent us a cluster config that
// includes this folder — i.e. they configured (accepted) the folder on their
// side. The entry persists across device-offline and folder-paused; it does
// NOT appear for devices that are listed on the folder but never accepted it.
// Use `_, ok := RemoteSequence[did]` as the authoritative acceptance signal.
type FolderStatus struct {
	State          string           `json:"state"` // idle | scanning | syncing | error
	NeedFiles      int              `json:"needFiles"`
	NeedBytes      int64            `json:"needBytes"`
	GlobalFiles    int              `json:"globalFiles"`
	GlobalBytes    int64            `json:"globalBytes"`
	LocalFiles     int              `json:"localFiles"`
	InSyncFiles    int              `json:"inSyncFiles"`
	PullErrors     int              `json:"pullErrors"`
	Error          string           `json:"error"`
	StateChanged   string           `json:"stateChanged"`
	Paused         bool             `json:"paused"`
	RemoteSequence map[string]int64 `json:"remoteSequence"`
	// ScanPct is the scan completion (0–100) while State == "scanning", sourced
	// from FolderScanProgress events (db/status carries no scan progress). 0 when
	// not scanning or before the first progress event has arrived.
	ScanPct float64 `json:"scanPct"`

	// Receive-only folders track items changed/added locally that haven't been
	// (and won't be) sent to the cluster. >0 means this device holds local
	// changes the source doesn't have — the trigger for the "Revert local
	// changes" affordance.
	ReceiveOnlyChangedFiles int   `json:"receiveOnlyChangedFiles"`
	ReceiveOnlyChangedBytes int64 `json:"receiveOnlyChangedBytes"`
	ReceiveOnlyTotalItems   int   `json:"receiveOnlyTotalItems"`
}

// GetFolderStatus returns the live sync status for a folder from Syncthing.
func (c *Client) GetFolderStatus(folderID string) (FolderStatus, error) {
	var s FolderStatus
	if err := c.get("/rest/db/status?folder="+folderID, &s); err != nil {
		return s, err
	}
	// Overlay the event-sourced scan %. Passing scanning=false also clears any
	// stale cache entry once the scan has finished.
	s.ScanPct = c.takeScanProgress(folderID, s.State == "scanning")
	return s, nil
}

// IsAnyFolderBusy reports whether any non-paused folder is currently
// making active progress — state == "syncing" or "scanning". We
// deliberately do NOT count "idle + NeedBytes > 0" as busy: that's the
// state ST sits in when a peer is offline (work pending, nothing it
// can do). Treating that as busy would pin the foreground service
// indefinitely waiting for someone to reconnect. The next trigger or
// peer-reconnect event will start a fresh sync window when there's
// actually work to do.
//
// Error / stopped states are also "not busy" — we're not making
// progress on them either, and pinning the service won't help.
func (c *Client) IsAnyFolderBusy() (bool, error) {
	folders, err := c.ListFolders()
	if err != nil {
		return false, err
	}
	for _, f := range folders {
		if f.Paused {
			continue
		}
		st, err := c.GetFolderStatus(f.ID)
		if err != nil {
			// Treat lookup failure as "busy" — safer to keep service
			// alive an extra polling cycle than tear down on a
			// transient HTTP hiccup. The maxSession cap in the gate
			// prevents that from looping forever.
			return true, err
		}
		if st.State == "syncing" || st.State == "scanning" {
			return true, nil
		}
	}
	return false, nil
}

// PeerNeed is /rest/db/completion for one (folder, device): how much the remote
// still needs FROM US (>0 means our local state hasn't fully propagated to them),
// plus its live BEP state for this folder. This is the device-level "is everyone
// caught up?" signal — distinct from a folder's own idle/syncing state, which
// only reflects what WE still need to pull. Both the power gate and the honest
// folder-relation UI read it (rolled up vs per-peer), so the truth lives here.
type PeerNeed struct {
	Completion  float64 `json:"completion"`
	NeedItems   int     `json:"needItems"`
	NeedDeletes int     `json:"needDeletes"`
	NeedBytes   int64   `json:"needBytes"`
	RemoteState string  `json:"remoteState"` // "valid" | "paused" | "notSharing" | "unknown"
}

// DeviceCompletion returns the per-(folder, device) completion view.
func (c *Client) DeviceCompletion(folderID, deviceID string) (PeerNeed, error) {
	var p PeerNeed
	return p, c.get("/rest/db/completion?folder="+folderID+"&device="+deviceID, &p)
}

// anyPeerBehind reports whether any accepted remote still needs items from us,
// across all non-paused folders. With connectedOnly=false it counts offline
// peers too (completion reflects last-known index); with connectedOnly=true it
// considers only peers with a live connection right now.
//
// On ANY lookup error it reports true (assume behind): over-syncing wastes a
// little battery, under-syncing silently loses data, and we never want the latter.
func (c *Client) anyPeerBehind(connectedOnly bool) (bool, error) {
	folders, err := c.ListFolders()
	if err != nil {
		return true, err
	}
	var conns map[string]bool
	if connectedOnly {
		if conns, err = c.ConnectedDeviceIDs(); err != nil {
			return true, err
		}
	}
	selfID := ""
	if st, err := c.SystemStatus(); err == nil {
		selfID = st.MyID
	}
	for _, f := range folders {
		if f.Paused {
			continue
		}
		for _, d := range f.Devices {
			if d.DeviceID == "" || d.DeviceID == selfID {
				continue
			}
			if connectedOnly && !conns[d.DeviceID] {
				continue
			}
			p, err := c.DeviceCompletion(f.ID, d.DeviceID)
			if err != nil {
				return true, err
			}
			if p.NeedItems > 0 || p.NeedDeletes > 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

// AnyPeerBehind reports whether our local state has NOT fully propagated to every
// accepted peer, INCLUDING offline ones. The power gate uses it for the backstop
// tick / dirty flag: false means everyone is caught up (safe to sleep), true
// means there's still pending work — even if the only peer that needs it is
// currently offline (so it isn't silently dropped).
func (c *Client) AnyPeerBehind() (bool, error) { return c.anyPeerBehind(false) }

// AnyConnectedPeerBehind is AnyPeerBehind restricted to peers with a live
// connection: "is someone actively able to pull from us right now?". The power
// gate uses THIS (not AnyPeerBehind) to keep a sync session alive while a client
// is downloading — gating on connected peers means a permanently-offline peer
// can never pin ST awake.
func (c *Client) AnyConnectedPeerBehind() (bool, error) { return c.anyPeerBehind(true) }

// PendingFolder is a folder offered by a connected device via BEP ClusterConfig
// that hasn't been configured locally yet.
type PendingFolder struct {
	FolderID string `json:"folderID"`
	Label    string `json:"label"`
	DeviceID string `json:"deviceID"`
}

// PendingFolders returns folders that connected devices want to share with us.
// Syncthing discovers these automatically via BEP — no extra signalling needed.
func (c *Client) PendingFolders() ([]PendingFolder, error) {
	// Syncthing response: map[folderID]{offeredBy: map[deviceID]{label, time, ...}}
	var raw map[string]struct {
		OfferedBy map[string]struct {
			Label string `json:"label"`
		} `json:"offeredBy"`
	}
	if err := c.get("/rest/cluster/pending/folders", &raw); err != nil {
		return nil, err
	}
	var out []PendingFolder
	for folderID, entry := range raw {
		for deviceID, info := range entry.OfferedBy {
			out = append(out, PendingFolder{
				FolderID: folderID,
				Label:    info.Label,
				DeviceID: deviceID,
			})
		}
	}
	return out, nil
}

// GetFolderIgnores returns the ignore patterns for a folder.
func (c *Client) GetFolderIgnores(folderID string) ([]string, error) {
	var resp struct {
		Ignore []string `json:"ignore"`
	}
	if err := c.get("/rest/db/ignores?folder="+folderID, &resp); err != nil {
		return nil, err
	}
	if resp.Ignore == nil {
		return []string{}, nil
	}
	return resp.Ignore, nil
}

// SetFolderIgnores replaces the ignore patterns for a folder.
func (c *Client) SetFolderIgnores(folderID string, patterns []string) error {
	return c.post("/rest/db/ignores?folder="+folderID, map[string][]string{"ignore": patterns}, nil)
}

// ConflictFile represents a Syncthing conflict copy found in a folder.
type ConflictFile struct {
	Path         string `json:"path"`
	OriginalPath string `json:"originalPath"` // path without the .sync-conflict- part
}

// DismissPendingFolder removes a folder from the pending list without accepting.
func (c *Client) DismissPendingFolder(folderID, deviceID string) error {
	return c.del("/rest/cluster/pending/folders?folder=" + folderID + "&device=" + deviceID)
}
