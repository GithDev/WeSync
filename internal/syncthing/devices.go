package syncthing

import (
	"net"
	"time"
)

type Device struct {
	DeviceID          string   `json:"deviceID"`
	Name              string   `json:"name"`
	Addresses         []string `json:"addresses"`
	Paused            bool     `json:"paused"`
	Introducer        bool     `json:"introducer"`        // peer may introduce other devices. Invariant (reconciled in updateTrust): true iff a direct pairing (introducedBy=="") AND the global Introducer setting is on. Introduced devices are never flagged — that would cascade introducer trust across the mesh.
	AutoAcceptFolders bool     `json:"autoAcceptFolders"` // always false — WeSync controls folder acceptance
}

type PendingDevice struct {
	DeviceID  string   `json:"deviceID"`
	Name      string   `json:"name"`
	Address   string   `json:"address"`
	FolderIDs []string // folders this device offered in BEP ClusterConfig
}

func (c *Client) ListDevices() ([]Device, error) {
	var cfg struct {
		Devices []Device `json:"devices"`
	}
	return cfg.Devices, c.get("/rest/config", &cfg)
}

// AddDevice adds a device to Syncthing. addr should be a Syncthing sync address
// like "tcp://192.168.1.1:22000"; pass "" to use Syncthing's dynamic discovery.
func (c *Client) AddDevice(id, name, addr string) error {
	addresses := []string{"dynamic"}
	if addr != "" {
		addresses = []string{addr}
	}
	d := Device{
		DeviceID:          id,
		Name:              name,
		Addresses:         addresses,
		Introducer:        false, // set later by updateTrust/trustDevice per the introducedBy=="" invariant — AddDevice itself stays neutral
		AutoAcceptFolders: false, // always off — WeSync controls folder acceptance explicitly
	}
	return c.post("/rest/config/devices", d, nil)
}

// UpdateDevice updates the name of an existing device in Syncthing config.
func (c *Client) UpdateDevice(id, name string) error {
	return c.patch("/rest/config/devices/"+id, map[string]string{"name": name})
}

// UpdateDeviceIntroducer sets the Introducer flag on a device.
// When true, devices introduced by this device are automatically trusted.
func (c *Client) UpdateDeviceIntroducer(id string, enabled bool) error {
	return c.patch("/rest/config/devices/"+id, map[string]any{"introducer": enabled})
}

func (c *Client) RemoveDevice(id string) error {
	return c.del("/rest/config/devices/" + id)
}

func (c *Client) PendingDevices() ([]PendingDevice, error) {
	raw := map[string]struct {
		Name    string         `json:"name"`
		Address string         `json:"address"`
		Folders map[string]any `json:"folders"` // folderID → metadata
	}{}
	if err := c.get("/rest/cluster/pending/devices", &raw); err != nil {
		return nil, err
	}
	out := make([]PendingDevice, 0, len(raw))
	for id, info := range raw {
		folderIDs := make([]string, 0, len(info.Folders))
		for fid := range info.Folders {
			folderIDs = append(folderIDs, fid)
		}
		out = append(out, PendingDevice{
			DeviceID:  id,
			Name:      info.Name,
			Address:   info.Address,
			FolderIDs: folderIDs,
		})
	}
	return out, nil
}

func (c *Client) DismissPendingDevice(id string) error {
	return c.del("/rest/cluster/pending/devices?device=" + id)
}

func (c *Client) ConnectedDeviceIDs() (map[string]bool, error) {
	var resp struct {
		Connections map[string]struct {
			Connected bool `json:"connected"`
		} `json:"connections"`
	}
	if err := c.get("/rest/system/connections", &resp); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(resp.Connections))
	for id, conn := range resp.Connections {
		out[id] = conn.Connected
	}
	return out, nil
}

// TransferredTotalBytes returns ST's cumulative bytes transferred (in + out)
// across all connections. The power gate samples the delta between polls as a
// "is the network actually moving data?" signal for its stall guard — a
// connected peer that is "behind" but transferring nothing must not pin ST
// awake forever.
func (c *Client) TransferredTotalBytes() (int64, error) {
	var resp struct {
		Total struct {
			InBytesTotal  int64 `json:"inBytesTotal"`
			OutBytesTotal int64 `json:"outBytesTotal"`
		} `json:"total"`
	}
	if err := c.get("/rest/system/connections", &resp); err != nil {
		return 0, err
	}
	return resp.Total.InBytesTotal + resp.Total.OutBytesTotal, nil
}

// DeviceLastSeen returns the last time ST saw each device, from
// /rest/stats/device. The honest status UI uses it to anchor an offline peer's
// "in sync as of <time>" instead of an eternal "up to date". Devices missing
// from the map (or with an unparseable timestamp) are simply absent.
func (c *Client) DeviceLastSeen() (map[string]time.Time, error) {
	var raw map[string]struct {
		LastSeen string `json:"lastSeen"`
	}
	if err := c.get("/rest/stats/device", &raw); err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(raw))
	for id, s := range raw {
		if t, err := time.Parse(time.RFC3339, s.LastSeen); err == nil && !t.IsZero() {
			out[id] = t
		}
	}
	return out, nil
}

// PauseDevice pauses ST's connection to a device. Triggers a reconnect when
// resumed, which causes a fresh ClusterConfig exchange.
func (c *Client) PauseDevice(deviceID string) error {
	return c.post("/rest/system/pause?device="+deviceID, nil, nil)
}

// ResumeDevice resumes ST's connection to a previously paused device.
func (c *Client) ResumeDevice(deviceID string) error {
	return c.post("/rest/system/resume?device="+deviceID, nil, nil)
}

// GetConnectedAddresses returns the IP address (no port) for each device
// currently connected to Syncthing. Used by MaintainConnections to reach
// the WeSync peerwire port on devices we know via BEP but not yet via UDP.
func (c *Client) GetConnectedAddresses() (map[string]string, error) {
	var resp struct {
		Connections map[string]struct {
			Connected bool   `json:"connected"`
			Address   string `json:"address"` // "host:port" of ST connection
		} `json:"connections"`
	}
	if err := c.get("/rest/system/connections", &resp); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(resp.Connections))
	for id, conn := range resp.Connections {
		if !conn.Connected || conn.Address == "" {
			continue
		}
		host, _, err := net.SplitHostPort(conn.Address)
		if err != nil {
			continue
		}
		out[id] = host
	}
	return out, nil
}
