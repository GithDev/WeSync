package syncthing

import (
	"sort"
	"strconv"
	"strings"
)

type Options struct {
	GlobalAnnounceEnabled bool     `json:"globalAnnounceEnabled"`
	GlobalAnnounceServers []string `json:"globalAnnounceServers"`
	LocalAnnounceEnabled  bool     `json:"localAnnounceEnabled"`
	RelaysEnabled         bool     `json:"relaysEnabled"`
	NATEnabled            bool     `json:"natEnabled"`
	URAccepted            int      `json:"urAccepted"`
	StunKeepaliveStartS   int      `json:"stunKeepaliveStartS"`
	StunKeepaliveMinS     int      `json:"stunKeepaliveMinS"`
	ListenAddresses       []string `json:"listenAddresses"`
}

// lockOptions is a minimal struct for PATCH /rest/config/options that only
// touches the fields WeSync cares about. Fields absent here are never sent,
// so Syncthing leaves them (e.g. ListenAddresses) untouched.
type lockOptions struct {
	GlobalAnnounceEnabled bool     `json:"globalAnnounceEnabled"`
	GlobalAnnounceServers []string `json:"globalAnnounceServers"`
	LocalAnnounceEnabled  bool     `json:"localAnnounceEnabled"`
	RelaysEnabled         bool     `json:"relaysEnabled"`
	NATEnabled            bool     `json:"natEnabled"`
	URAccepted            int      `json:"urAccepted"`
	StunKeepaliveStartS   int      `json:"stunKeepaliveStartS"`
	StunKeepaliveMinS     int      `json:"stunKeepaliveMinS"`
}

// SetGUIEnabled enables or disables Syncthing's web GUI AND its REST API.
// WeSync requires the REST API to be enabled at all times.
// Only call this in debug mode to restore a previously disabled GUI.
func (c *Client) SetGUIEnabled(enabled bool) error {
	return c.patch("/rest/config/gui", map[string]bool{"enabled": enabled})
}

// LockToLocalNetwork configures level 1: LAN only, nothing external.
// No global discovery, no relays, no UPnP/NAT.
func (c *Client) LockToLocalNetwork() error {
	return c.SetConnectivityLevel(1)
}

// Syncthing's default STUN keepalive intervals. STUN — and therefore QUIC/UDP
// hole-punching — only runs when NATEnabled is true AND both of these are >= 1
// (see Syncthing's OptionsConfiguration.IsStunDisabled). Sending 0 would keep
// STUN disabled even with NATEnabled on, so we send the upstream defaults.
const (
	stunKeepaliveStartS = 180
	stunKeepaliveMinS   = 20
)

// SetConnectivityLevel applies one of three security/connectivity profiles to ST.
// Level 1: LAN only — most private, nothing external.
// Level 2: + Global Discovery — devices find each other by ID across networks.
//
//	IP and device ID shared with Syncthing Foundation's discovery servers.
//
// Level 3: + Relay — traffic routed through relay servers when direct fails.
//
//	Not recommended: relay operators can see connection metadata.
//
// NATEnabled is Syncthing's single master switch for NAT traversal: it gates BOTH
// STUN/QUIC hole-punching AND UPnP/NAT-PMP port mapping — they cannot be separated.
// Without it a NAT'd peer has no reachable path and global discovery only finds
// addresses it can never connect to. So it is ON at levels 2 and 3, and OFF only at
// level 1. Enabling it means ST WILL attempt UPnP/NAT-PMP port mappings at those levels.
func (c *Client) SetConnectivityLevel(level int) error {
	switch level {
	case 2:
		return c.patch("/rest/config/options", lockOptions{
			GlobalAnnounceEnabled: true,
			LocalAnnounceEnabled:  true,
			RelaysEnabled:         false,
			NATEnabled:            true, // enables STUN hole-punching (UPnP/NAT-PMP comes bundled)
			URAccepted:            -1,
			StunKeepaliveStartS:   stunKeepaliveStartS,
			StunKeepaliveMinS:     stunKeepaliveMinS,
		})
	case 3:
		if err := c.patch("/rest/config/options", lockOptions{
			GlobalAnnounceEnabled: true,
			LocalAnnounceEnabled:  true,
			RelaysEnabled:         true,
			NATEnabled:            true, // enables STUN hole-punching (UPnP/NAT-PMP comes bundled)
			URAccepted:            -1,
			StunKeepaliveStartS:   stunKeepaliveStartS,
			StunKeepaliveMinS:     stunKeepaliveMinS,
		}); err != nil {
			return err
		}
		// RelaysEnabled alone is a no-op if no listen address pulls in the relay
		// pool. Selecting relay mode must guarantee the relay endpoint is present.
		return c.ensureRelayListenAddress()
	default: // level 1
		return c.patch("/rest/config/options", lockOptions{
			GlobalAnnounceEnabled: false,
			GlobalAnnounceServers: []string{},
			LocalAnnounceEnabled:  true,
			RelaysEnabled:         false,
			NATEnabled:            false, // no external traversal: no STUN, no UPnP/NAT-PMP
			URAccepted:            -1,
			StunKeepaliveStartS:   stunKeepaliveStartS,
			StunKeepaliveMinS:     stunKeepaliveMinS,
		})
	}
}

func (c *Client) GetOptions() (Options, error) {
	var o Options
	return o, c.get("/rest/config/options", &o)
}

// relayListenAddr is Syncthing's relay-pool endpoint. A listen address pulling
// in the pool is what actually makes ST dial a relay; "default" already expands
// to include it.
const relayListenAddr = "dynamic+https://relays.syncthing.net/endpoint"

// isRelaySource reports whether a string pulls in a relay. It applies to two
// shapes ST uses interchangeably: a ListenAddresses entry and a
// connectionServiceStatus key. "default" expands to include the relay pool;
// "dynamic+..." IS a relay pool; "relay://..." is an explicit static relay.
// (A status key is never "default" — that branch is just harmless there.)
func isRelaySource(s string) bool {
	return s == "default" || strings.HasPrefix(s, "dynamic") || strings.HasPrefix(s, "relay://")
}

// sortedKeys returns a map's keys in stable order, so status readers that pick a
// single representative error don't flap between equally-valid errors across
// polls (Go map iteration is randomised).
func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// ensureRelayListenAddress guarantees ST's ListenAddresses include a relay
// source, so RelaysEnabled isn't a silent no-op. Idempotent: a no-op when
// "default" or a relay/dynamic entry is already present. The relay endpoint is
// added last so GetListenPort still reads the direct tcp/quic port first.
//
// We never strip it again for lower levels — RelaysEnabled:false disables the
// relay regardless of whether the listen address lingers.
func (c *Client) ensureRelayListenAddress() error {
	o, err := c.GetOptions()
	if err != nil {
		return err
	}
	for _, a := range o.ListenAddresses {
		if isRelaySource(a) {
			return nil // already covered
		}
	}
	if len(o.ListenAddresses) == 0 {
		// Empty means ST listens on nothing; "default" restores tcp+quic+relay.
		return c.patch("/rest/config/options", map[string][]string{"listenAddresses": {"default"}})
	}
	addrs := append(append([]string{}, o.ListenAddresses...), relayListenAddr)
	return c.patch("/rest/config/options", map[string][]string{"listenAddresses": addrs})
}

// RelayStatus reports whether ST currently has a working relay connection.
type RelayStatus struct {
	Live    bool   `json:"live"`
	Address string `json:"address,omitempty"` // a relay:// address when live
	Error   string `json:"error,omitempty"`   // relay listener error when not live
}

// systemStatus is the subset of GET /rest/system/status that WeSync reads to
// derive connectivity health. Relay and global-discovery state both live in this
// one response, so a single fetch serves both — see ConnectivityStatus.
type systemStatus struct {
	ConnectionServiceStatus map[string]listenerStatus `json:"connectionServiceStatus"`
	DiscoveryStatus         map[string]discoveryEntry `json:"discoveryStatus"`
}

type listenerStatus struct {
	Error        *string  `json:"error"`
	LANAddresses []string `json:"lanAddresses"`
	WANAddresses []string `json:"wanAddresses"`
}

type discoveryEntry struct {
	Error *string `json:"error"`
}

// ConnectivityStatus bundles relay and global-discovery health, both derived
// from a single /rest/system/status read.
type ConnectivityStatus struct {
	Relay     RelayStatus           `json:"relay"`
	Discovery GlobalDiscoveryStatus `json:"discovery"`
}

// ConnectivityStatus fetches ST's system status once and derives both the relay
// and global-discovery views from it. The UI polls this single endpoint instead
// of one per concern, halving the round-trips and giving a consistent snapshot.
func (c *Client) ConnectivityStatus() (ConnectivityStatus, error) {
	var s systemStatus
	if err := c.get("/rest/system/status", &s); err != nil {
		return ConnectivityStatus{}, err
	}
	return ConnectivityStatus{
		Relay:     relayStatusFrom(s.ConnectionServiceStatus),
		Discovery: discoveryStatusFrom(s.DiscoveryStatus),
	}, nil
}

// relayStatusFrom decides whether a relay listener is actually up. A working
// relay listener exposes a relay:// address with no error; if the relay listener
// is present but failing, its error is surfaced so the UI can explain why.
func relayStatusFrom(css map[string]listenerStatus) RelayStatus {
	var lastErr string
	for _, key := range sortedKeys(css) {
		st := css[key]
		for _, a := range append(append([]string{}, st.WANAddresses...), st.LANAddresses...) {
			if strings.HasPrefix(a, "relay://") {
				return RelayStatus{Live: true, Address: a}
			}
		}
		// The status key is the configured listener endpoint (relay:// or
		// dynamic+http(s)://, incl. custom pools whose host lacks "relay").
		if isRelaySource(key) && st.Error != nil && *st.Error != "" {
			lastErr = *st.Error
		}
	}
	return RelayStatus{Live: false, Error: lastErr}
}

// GlobalDiscoveryStatus reports whether ST is successfully reaching the global
// discovery servers — i.e. whether this device is announcing itself so peers can
// find it by device ID across the internet.
type GlobalDiscoveryStatus struct {
	Live    bool   `json:"live"`            // at least one global discovery server is reachable
	Servers int    `json:"servers"`         // number of configured global discovery servers
	OK      int    `json:"ok"`              // how many of those are currently reachable
	Error   string `json:"error,omitempty"` // a representative error when none are reachable
}

// discoveryStatusFrom reports whether global announce/lookup is working. ST keys
// each discovery method in discoveryStatus by name: global servers are
// "global@https://..."; LAN methods are "IPv4 local" etc. We count only the
// global entries — Live means at least one had a nil error (its last
// announce/lookup succeeded). When none are reachable, a representative error is
// surfaced so the UI can explain why discovery isn't working.
func discoveryStatusFrom(ds map[string]discoveryEntry) GlobalDiscoveryStatus {
	var st GlobalDiscoveryStatus
	var lastErr string
	for _, key := range sortedKeys(ds) {
		if !strings.HasPrefix(key, "global") {
			continue
		}
		e := ds[key]
		st.Servers++
		if e.Error == nil || *e.Error == "" {
			st.OK++
		} else {
			lastErr = *e.Error
		}
	}
	st.Live = st.OK > 0
	if !st.Live {
		st.Error = lastErr
	}
	return st
}

// GetListenPort returns the TCP port Syncthing is using for sync connections.
// Falls back to 22000 (Syncthing default) if it cannot be determined.
func (c *Client) GetListenPort() int {
	o, err := c.GetOptions()
	if err != nil {
		return 22000
	}
	for _, addr := range o.ListenAddresses {
		if addr == "" || addr == "default" {
			return 22000
		}
		// Format: "tcp://0.0.0.0:22000", "tcp4://...", etc.
		if i := strings.LastIndex(addr, ":"); i >= 0 {
			if p, err := strconv.Atoi(addr[i+1:]); err == nil && p > 0 {
				return p
			}
		}
	}
	return 22000
}
