package syncthing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetConnectedAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"connections": map[string]any{
				"DEVICE-A": map[string]any{"connected": true, "address": "192.168.1.5:22000"},
				"DEVICE-B": map[string]any{"connected": false, "address": "192.168.1.6:22000"},
				"DEVICE-C": map[string]any{"connected": true, "address": ""},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test")
	addrs, err := c.GetConnectedAddresses()
	if err != nil {
		t.Fatal(err)
	}
	if addrs["DEVICE-A"] != "192.168.1.5" {
		t.Errorf("expected 192.168.1.5, got %q", addrs["DEVICE-A"])
	}
	if _, ok := addrs["DEVICE-B"]; ok {
		t.Error("disconnected device should not appear")
	}
	if _, ok := addrs["DEVICE-C"]; ok {
		t.Error("empty address should not appear")
	}
}
