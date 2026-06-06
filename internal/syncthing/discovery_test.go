package syncthing

import "testing"

// de builds a discoveryEntry; an empty errStr means a nil (no) error.
func de(errStr string) discoveryEntry {
	var e *string
	if errStr != "" {
		e = &errStr
	}
	return discoveryEntry{Error: e}
}

func TestDiscoveryStatusFrom(t *testing.T) {
	const (
		v4     = "global@https://discovery-announce-v4.syncthing.net/v2/"
		v6     = "global@https://discovery-announce-v6.syncthing.net/v2/"
		lookup = "global@https://discovery-lookup.syncthing.net/v2/"
	)

	tests := []struct {
		name        string
		entries     map[string]discoveryEntry
		wantLive    bool
		wantServers int
		wantOK      int
		wantErr     string
	}{
		{
			// The real-world shape: IPv6 announce fails on a v4-only network,
			// the two others succeed. LAN methods are not counted.
			name: "mixed global, LAN ignored",
			entries: map[string]discoveryEntry{
				"IPv4 local": de(""),
				"IPv6 local": de(""),
				v4:           de(""),
				v6:           de("lookup v6: no data"),
				lookup:       de(""),
			},
			wantLive:    true,
			wantServers: 3,
			wantOK:      2,
			wantErr:     "", // live → no error surfaced
		},
		{
			name:        "all global reachable",
			entries:     map[string]discoveryEntry{v4: de(""), v6: de(""), lookup: de("")},
			wantLive:    true,
			wantServers: 3,
			wantOK:      3,
		},
		{
			name:        "no global servers (LAN only)",
			entries:     map[string]discoveryEntry{"IPv4 local": de(""), "IPv6 local": de("")},
			wantLive:    false,
			wantServers: 0,
			wantOK:      0,
			wantErr:     "",
		},
		{
			name:        "empty discovery status",
			entries:     map[string]discoveryEntry{},
			wantLive:    false,
			wantServers: 0,
		},
		{
			// All global down → not live, with a representative error. Sorted
			// iteration makes it deterministic: lookup > announce-v6 > announce-v4.
			name: "all global down → representative error",
			entries: map[string]discoveryEntry{
				v4:     de("v4 down"),
				v6:     de("v6 down"),
				lookup: de("lookup down"),
			},
			wantLive:    false,
			wantServers: 3,
			wantOK:      0,
			wantErr:     "lookup down",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds := discoveryStatusFrom(tt.entries)
			if ds.Live != tt.wantLive {
				t.Errorf("Live = %v, want %v", ds.Live, tt.wantLive)
			}
			if ds.Servers != tt.wantServers {
				t.Errorf("Servers = %d, want %d", ds.Servers, tt.wantServers)
			}
			if ds.OK != tt.wantOK {
				t.Errorf("OK = %d, want %d", ds.OK, tt.wantOK)
			}
			if ds.Error != tt.wantErr {
				t.Errorf("Error = %q, want %q", ds.Error, tt.wantErr)
			}
			if ds.Live && ds.Error != "" {
				t.Errorf("live status must not carry an error, got %q", ds.Error)
			}
		})
	}
}

// TestDiscoveryStatusErrorDeterministic pins that the representative error is the
// sorted-last erroring server, so it never flaps across reads.
func TestDiscoveryStatusErrorDeterministic(t *testing.T) {
	entries := map[string]discoveryEntry{
		"global@https://a.example/v2/": de("err-a"),
		"global@https://b.example/v2/": de("err-b"),
		"global@https://c.example/v2/": de("err-c"),
	}
	if got := discoveryStatusFrom(entries).Error; got != "err-c" {
		t.Errorf("Error = %q, want sorted-last %q", got, "err-c")
	}
}
