package api

import (
	"testing"
	"wesync/internal/syncthing"
)

// The introducer-flag invariant (reconciled in updateTrust): a device is flagged
// as an ST introducer iff we paired with it directly — i.e. it has no introducedBy
// in any folder. An introduced device must NEVER be flagged, otherwise introducer
// trust cascades transitively across the mesh ("too many introducers").
func TestUpdateTrustIntroducerInvariant(t *testing.T) {
	inst := newInstance(t, "self", "self")
	st := inst.st

	// "direct" was paired by us (no introducedBy). "intro" was added by ST's
	// introducer mechanism (introducedBy set in the shared folder). Both start
	// unflagged; updateTrust must flag only "direct".
	st.mu.Lock()
	st.devices = []syncthing.Device{
		{DeviceID: "direct"},
		{DeviceID: "intro"},
	}
	st.folders = []syncthing.Folder{{
		ID: "f1",
		Devices: []syncthing.FolderDevice{
			{DeviceID: "direct"},
			{DeviceID: "intro", IntroducedBy: "direct"},
		},
	}}
	st.mu.Unlock()

	inst.handlers.updateTrust()

	st.mu.Lock()
	defer st.mu.Unlock()
	got := map[string]bool{}
	for _, d := range st.devices {
		got[d.DeviceID] = d.Introducer
	}
	if !got["direct"] {
		t.Error("direct-paired device should be flagged introducer, got false")
	}
	if got["intro"] {
		t.Error("introduced device must NOT be flagged introducer (cascade), got true")
	}
}
