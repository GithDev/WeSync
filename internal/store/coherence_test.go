package store

import (
	"path/filepath"
	"testing"
)

// On Android two *Store handles point at the same wesync.db file: the power
// gate opens one in initGate, the API server another in backend.Run. The user
// reports that changing a power setting (PUT /api/power, written via the API
// handle) doesn't take effect until the app is hard-restarted — exactly the
// signature of the gate's handle reading STALE data after the API handle
// commits. This pins whether that's real, with no device needed.
func TestTwoHandlesSeeEachOthersWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wesync.db")

	api, err := Open(path) // stands in for backend.Run's store (the writer)
	if err != nil {
		t.Fatalf("open api handle: %v", err)
	}
	defer closeStore(api)
	gate, err := Open(path) // stands in for initGate's store (the reader)
	if err != nil {
		t.Fatalf("open gate handle: %v", err)
	}
	defer closeStore(gate)

	// First write through the API handle, read through the gate handle.
	if err := api.SetPowerSettings(PowerSettings{
		SyncTrigger: "periodic", NetworkMode: "any", PeriodicMinutes: 30,
	}); err != nil {
		t.Fatalf("first SetPowerSettings: %v", err)
	}
	got, err := gate.GetPowerSettings()
	if err != nil {
		t.Fatalf("first GetPowerSettings: %v", err)
	}
	if got.SyncTrigger != "periodic" || got.PeriodicMinutes != 30 {
		t.Fatalf("gate handle never saw API's first write: %+v", got)
	}

	// The real test: a SECOND write through the API handle, read again through
	// the already-used gate handle. If the gate reads the stale "periodic"
	// here, that IS the "must restart to apply settings" bug.
	if err := api.SetPowerSettings(PowerSettings{
		SyncTrigger: "on_change_poll", NetworkMode: "trusted_wifi", PeriodicMinutes: 60,
	}); err != nil {
		t.Fatalf("second SetPowerSettings: %v", err)
	}
	got, err = gate.GetPowerSettings()
	if err != nil {
		t.Fatalf("second GetPowerSettings: %v", err)
	}
	if got.SyncTrigger != "on_change_poll" || got.PeriodicMinutes != 60 {
		t.Fatalf("gate handle read STALE settings after API update: %+v — confirms the 'restart to apply' bug", got)
	}
}

// closeStore releases the underlying SQLite connection so the temp file can be
// removed on Windows (where an open handle blocks deletion).
func closeStore(s *Store) {
	if d, err := s.db.DB(); err == nil {
		_ = d.Close()
	}
}
