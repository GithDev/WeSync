package syncthing

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeST is a minimal stand-in for Syncthing's REST API. It serves a
// configurable /rest/config/options and /rest/system/status, and records every
// PATCH body so tests can assert what WeSync wrote.
type fakeST struct {
	mu sync.Mutex

	// optionsJSON is returned verbatim for GET /rest/config/options.
	optionsJSON string
	// statusJSON is returned verbatim for GET /rest/system/status.
	statusJSON string

	// patches records the raw body of each PATCH /rest/config/options.
	patches []string
}

func (f *fakeST) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/config/options", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			f.mu.Lock()
			body := f.optionsJSON
			f.mu.Unlock()
			io.WriteString(w, body)
		case http.MethodPatch:
			b, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.patches = append(f.patches, string(b))
			f.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/rest/system/status", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		body := f.statusJSON
		f.mu.Unlock()
		io.WriteString(w, body)
	})
	return mux
}

func (f *fakeST) lastPatch() (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.patches) == 0 {
		return "", false
	}
	return f.patches[len(f.patches)-1], true
}

func (f *fakeST) patchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.patches)
}

// newFakeST wires a fakeST to an httptest server and returns a Client pointed at
// it. The caller must defer srv.Close().
func newFakeST(t *testing.T, f *fakeST) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	return NewClient(srv.URL, "test"), srv
}

// ── relayStatusFrom ──────────────────────────────────────────────────────────

// lst builds a listenerStatus; an empty errStr means a nil (no) error.
func lst(errStr string, lan, wan []string) listenerStatus {
	var e *string
	if errStr != "" {
		e = &errStr
	}
	return listenerStatus{Error: e, LANAddresses: lan, WANAddresses: wan}
}

func TestRelayStatusFrom(t *testing.T) {
	tcp := lst("", []string{"tcp://0.0.0.0:22000"}, []string{"tcp://0.0.0.0:22000"})
	quic := lst("", []string{"quic://0.0.0.0:22000"}, []string{"quic://0.0.0.0:22000"})

	tests := []struct {
		name        string
		entries     map[string]listenerStatus
		wantLive    bool
		wantAddrPfx string
		wantErr     string
	}{
		{
			name: "live via wanAddresses",
			entries: map[string]listenerStatus{
				"tcp://0.0.0.0:22000":                           tcp,
				"dynamic+https://relays.syncthing.net/endpoint": lst("", nil, []string{"relay://1.2.3.4:22067/?id=ABC"}),
			},
			wantLive:    true,
			wantAddrPfx: "relay://",
		},
		{
			name: "live via lanAddresses",
			entries: map[string]listenerStatus{
				"dynamic+https://relays.syncthing.net/endpoint": lst("", []string{"relay://5.6.7.8:22067"}, nil),
			},
			wantLive:    true,
			wantAddrPfx: "relay://",
		},
		{
			name: "live wins even when another listener errors",
			entries: map[string]listenerStatus{
				"quic://0.0.0.0:22000":                          lst("quic bind failed", nil, nil),
				"dynamic+https://relays.syncthing.net/endpoint": lst("", nil, []string{"relay://1.1.1.1:22067"}),
			},
			wantLive:    true,
			wantAddrPfx: "relay://",
		},
		{
			name: "dynamic relay pool error, not yet connected",
			entries: map[string]listenerStatus{
				"tcp://0.0.0.0:22000":                           tcp,
				"dynamic+https://relays.syncthing.net/endpoint": lst("could not get relay list", nil, nil),
			},
			wantLive: false,
			wantErr:  "could not get relay list",
		},
		{
			name: "static relay:// listener error",
			entries: map[string]listenerStatus{
				"relay://my.relay.example:22067": lst("dial tcp: connection refused", nil, nil),
			},
			wantLive: false,
			wantErr:  "dial tcp: connection refused",
		},
		{
			// Regression: a custom pool whose host lacks the substring "relay"
			// must still have its error attributed. This is why isRelaySource
			// matches the scheme, not a substring.
			name: "custom relay pool error (host without 'relay')",
			entries: map[string]listenerStatus{
				"dynamic+https://pool.example.com/endpoint": lst("tls handshake timeout", nil, nil),
			},
			wantLive: false,
			wantErr:  "tls handshake timeout",
		},
		{
			name: "no relay listener at all",
			entries: map[string]listenerStatus{
				"tcp://0.0.0.0:22000":  tcp,
				"quic://0.0.0.0:22000": quic,
			},
			wantLive: false,
			wantErr:  "",
		},
		{
			// A non-relay listener's error must NOT be reported as a relay error.
			name: "tcp listener error is not a relay error",
			entries: map[string]listenerStatus{
				"tcp://0.0.0.0:22000": lst("listen tcp: address already in use", nil, nil),
			},
			wantLive: false,
			wantErr:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := relayStatusFrom(tt.entries)
			if rs.Live != tt.wantLive {
				t.Errorf("Live = %v, want %v", rs.Live, tt.wantLive)
			}
			if tt.wantAddrPfx != "" && !strings.HasPrefix(rs.Address, tt.wantAddrPfx) {
				t.Errorf("Address = %q, want prefix %q", rs.Address, tt.wantAddrPfx)
			}
			if tt.wantLive && rs.Address == "" {
				t.Errorf("live status must carry an address")
			}
			if rs.Error != tt.wantErr {
				t.Errorf("Error = %q, want %q", rs.Error, tt.wantErr)
			}
			if rs.Live && rs.Error != "" {
				t.Errorf("live status must not carry an error, got %q", rs.Error)
			}
		})
	}
}

// ── ensureRelayListenAddress ─────────────────────────────────────────────────

func TestEnsureRelayListenAddress(t *testing.T) {
	opts := func(addrs ...string) string {
		b, _ := json.Marshal(addrs)
		return `{"listenAddresses":` + string(b) + `}`
	}

	tests := []struct {
		name        string
		listen      string
		wantPatch   bool   // did we expect a PATCH at all?
		wantInPatch string // substring expected in the PATCH body (when wantPatch)
	}{
		{name: "default already covers relay", listen: opts("default"), wantPatch: false},
		{name: "dynamic pool already present", listen: opts("dynamic+https://relays.syncthing.net/endpoint"), wantPatch: false},
		{name: "custom dynamic pool already present", listen: opts("dynamic+https://pool.example.com/endpoint"), wantPatch: false},
		{name: "static relay already present", listen: opts("tcp://0.0.0.0:22000", "relay://my.relay:22067"), wantPatch: false},
		{
			name:        "tcp/quic only -> append relay endpoint",
			listen:      opts("tcp://0.0.0.0:22000", "quic://0.0.0.0:22000"),
			wantPatch:   true,
			wantInPatch: relayListenAddr,
		},
		{
			name:        "empty -> reset to default",
			listen:      opts(),
			wantPatch:   true,
			wantInPatch: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeST{optionsJSON: tt.listen}
			c, srv := newFakeST(t, f)
			defer srv.Close()

			if err := c.ensureRelayListenAddress(); err != nil {
				t.Fatalf("ensureRelayListenAddress: %v", err)
			}

			got := f.patchCount()
			if tt.wantPatch && got == 0 {
				t.Fatalf("expected a PATCH, got none")
			}
			if !tt.wantPatch && got != 0 {
				body, _ := f.lastPatch()
				t.Fatalf("expected NO PATCH (already covered), got %d: %s", got, body)
			}
			if tt.wantPatch {
				body, _ := f.lastPatch()
				if !strings.Contains(body, tt.wantInPatch) {
					t.Errorf("PATCH body %q missing %q", body, tt.wantInPatch)
				}
				// The original direct addresses must be preserved (additive),
				// except the empty->default case which has none.
				if strings.Contains(tt.listen, "tcp://0.0.0.0:22000") && !strings.Contains(body, "tcp://0.0.0.0:22000") {
					t.Errorf("PATCH dropped existing tcp address: %s", body)
				}
			}
		})
	}
}

// TestEnsureRelayListenAddressIdempotent verifies that re-running against the
// list produced by a first append makes no further change.
func TestEnsureRelayListenAddressIdempotent(t *testing.T) {
	f := &fakeST{optionsJSON: `{"listenAddresses":["tcp://0.0.0.0:22000"]}`}
	c, srv := newFakeST(t, f)
	defer srv.Close()

	if err := c.ensureRelayListenAddress(); err != nil {
		t.Fatal(err)
	}
	if f.patchCount() != 1 {
		t.Fatalf("first run: want 1 patch, got %d", f.patchCount())
	}
	// Feed the appended result back as the new options and re-run.
	body, _ := f.lastPatch()
	f.mu.Lock()
	f.optionsJSON = body
	f.patches = nil
	f.mu.Unlock()

	if err := c.ensureRelayListenAddress(); err != nil {
		t.Fatal(err)
	}
	if f.patchCount() != 0 {
		body, _ := f.lastPatch()
		t.Fatalf("second run should be a no-op, got patch: %s", body)
	}
}

// ── SetConnectivityLevel(3) ───────────────────────────────────────────────────

// TestSetConnectivityLevel3EnablesAndEnsuresRelay verifies the whole level-3
// path: it must turn RelaysEnabled on AND guarantee a relay listen address when
// the existing addresses are direct-only.
func TestSetConnectivityLevel3EnablesAndEnsuresRelay(t *testing.T) {
	f := &fakeST{optionsJSON: `{"listenAddresses":["tcp://0.0.0.0:22000","quic://0.0.0.0:22000"]}`}
	c, srv := newFakeST(t, f)
	defer srv.Close()

	if err := c.SetConnectivityLevel(3); err != nil {
		t.Fatalf("SetConnectivityLevel(3): %v", err)
	}

	f.mu.Lock()
	patches := append([]string(nil), f.patches...)
	f.mu.Unlock()

	if len(patches) < 2 {
		t.Fatalf("want at least 2 PATCHes (options + listenAddresses), got %d: %v", len(patches), patches)
	}

	// The options PATCH must enable relays.
	if !strings.Contains(patches[0], `"relaysEnabled":true`) {
		t.Errorf("options PATCH did not enable relays: %s", patches[0])
	}
	// A later PATCH must add the relay listen endpoint.
	foundRelayListen := false
	for _, p := range patches[1:] {
		if strings.Contains(p, relayListenAddr) {
			foundRelayListen = true
		}
	}
	if !foundRelayListen {
		t.Errorf("no PATCH added the relay listen address %q: %v", relayListenAddr, patches)
	}
}

// TestSetConnectivityLevel3DefaultNeedsNoListenPatch verifies that when ST
// already has "default" (which expands to include the relay pool), level 3 does
// NOT rewrite the listen addresses.
func TestSetConnectivityLevel3DefaultNeedsNoListenPatch(t *testing.T) {
	f := &fakeST{optionsJSON: `{"listenAddresses":["default"]}`}
	c, srv := newFakeST(t, f)
	defer srv.Close()

	if err := c.SetConnectivityLevel(3); err != nil {
		t.Fatalf("SetConnectivityLevel(3): %v", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.patches {
		if strings.Contains(p, "listenAddresses") {
			t.Errorf("level 3 rewrote listenAddresses despite 'default': %s", p)
		}
	}
}
