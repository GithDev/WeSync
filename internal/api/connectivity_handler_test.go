package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"wesync/internal/syncthing"
)

// TestConnectivityStatusHandler verifies GET /api/connectivity-status returns the
// backend's combined relay + global-discovery status as JSON, and rejects
// non-GET methods.
func TestConnectivityStatusHandler(t *testing.T) {
	inst := newInstance(t, "DEV-A", "node-a")

	t.Run("relay live + discovery visible", func(t *testing.T) {
		inst.st.mu.Lock()
		inst.st.connectivityStatus = syncthing.ConnectivityStatus{
			Relay:     syncthing.RelayStatus{Live: true, Address: "relay://1.2.3.4:22067"},
			Discovery: syncthing.GlobalDiscoveryStatus{Live: true, Servers: 4, OK: 3},
		}
		inst.st.mu.Unlock()

		rec := httptest.NewRecorder()
		inst.handlers.ConnectivityStatus(rec, httptest.NewRequest(http.MethodGet, "/api/connectivity-status", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var got syncthing.ConnectivityStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
		}
		if !got.Relay.Live || got.Relay.Address != "relay://1.2.3.4:22067" {
			t.Errorf("relay: got %+v, want live with address", got.Relay)
		}
		if !got.Discovery.Live || got.Discovery.Servers != 4 || got.Discovery.OK != 3 {
			t.Errorf("discovery: got %+v, want live 3/4", got.Discovery)
		}
	})

	t.Run("errors surfaced", func(t *testing.T) {
		inst.st.mu.Lock()
		inst.st.connectivityStatus = syncthing.ConnectivityStatus{
			Relay:     syncthing.RelayStatus{Live: false, Error: "can't reach a relay"},
			Discovery: syncthing.GlobalDiscoveryStatus{Live: false, Servers: 2, Error: "500 Internal Server Error"},
		}
		inst.st.mu.Unlock()

		rec := httptest.NewRecorder()
		inst.handlers.ConnectivityStatus(rec, httptest.NewRequest(http.MethodGet, "/api/connectivity-status", nil))

		var got syncthing.ConnectivityStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Relay.Live || got.Relay.Error != "can't reach a relay" {
			t.Errorf("relay: got %+v, want not-live with error", got.Relay)
		}
		if got.Discovery.Live || got.Discovery.Error != "500 Internal Server Error" {
			t.Errorf("discovery: got %+v, want not-live with error", got.Discovery)
		}
	})

	t.Run("rejects non-GET", func(t *testing.T) {
		rec := httptest.NewRecorder()
		inst.handlers.ConnectivityStatus(rec, httptest.NewRequest(http.MethodPost, "/api/connectivity-status", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", rec.Code)
		}
	})
}
