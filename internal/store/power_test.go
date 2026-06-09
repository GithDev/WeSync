package store

import (
	"reflect"
	"testing"
)

func TestPowerSettings_Defaults(t *testing.T) {
	s := openTest(t)
	p, err := s.GetPowerSettings()
	if err != nil {
		t.Fatalf("GetPowerSettings: %v", err)
	}
	want := PowerSettings{
		SyncTrigger:              "on_change_poll",
		PeriodicMinutes:          240,
		OnChangePollMinutes:      5,
		ScheduledTimes:           []string{},
		NetworkMode:              "any_wifi",
		TrustedSSIDs:             []string{},
		PauseWhenBatteryLow:      true,
		KeepSyncingWhileCharging: false, // opt-in power trade-off; default off
		BlockMeteredRoaming:      true,
	}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("defaults mismatch\n got:  %+v\n want: %+v", p, want)
	}
}

func TestPowerSettings_RoundTripTrustedWifiEmpty(t *testing.T) {
	s := openTest(t)
	in := PowerSettings{
		SyncTrigger:         "periodic",
		PeriodicMinutes:     120,
		OnChangePollMinutes: 5,
		ScheduledTimes:      []string{},
		NetworkMode:         "trusted_wifi",
		TrustedSSIDs:        []string{},
		PauseWhenBatteryLow: false,
	}
	if err := s.SetPowerSettings(in); err != nil {
		t.Fatalf("SetPowerSettings: %v", err)
	}
	out, err := s.GetPowerSettings()
	if err != nil {
		t.Fatalf("GetPowerSettings: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("mismatch\n got:  %+v\n want: %+v", out, in)
	}
}

func TestPowerSettings_RoundTripTrustedWifiWithSSIDs(t *testing.T) {
	s := openTest(t)
	in := PowerSettings{
		SyncTrigger:         "on_change_poll",
		PeriodicMinutes:     60,
		OnChangePollMinutes: 10,
		ScheduledTimes:      []string{"02:00", "14:30"},
		NetworkMode:         "trusted_wifi",
		TrustedSSIDs:        []string{"Home", "Office WiFi"},
		PauseWhenBatteryLow: true,
	}
	if err := s.SetPowerSettings(in); err != nil {
		t.Fatalf("SetPowerSettings: %v", err)
	}
	out, err := s.GetPowerSettings()
	if err != nil {
		t.Fatalf("GetPowerSettings: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("mismatch\n got:  %+v\n want: %+v", out, in)
	}
}

func TestPowerSettings_RoundTripScheduledTimes(t *testing.T) {
	s := openTest(t)
	in := PowerSettings{
		SyncTrigger:         "scheduled",
		PeriodicMinutes:     120,
		OnChangePollMinutes: 5,
		ScheduledTimes:      []string{"00:00", "06:00", "12:00", "18:00"},
		NetworkMode:         "any",
		TrustedSSIDs:        []string{},
		PauseWhenBatteryLow: false,
	}
	if err := s.SetPowerSettings(in); err != nil {
		t.Fatalf("SetPowerSettings: %v", err)
	}
	out, err := s.GetPowerSettings()
	if err != nil {
		t.Fatalf("GetPowerSettings: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("mismatch\n got:  %+v\n want: %+v", out, in)
	}
}

// Verifies the "switch to trusted_wifi while SSID list is empty" path —
// this is exactly the radio-flip the user triggered that 500'd.
func TestPowerSettings_SwitchToTrustedWifiKeepsDefaults(t *testing.T) {
	s := openTest(t)
	// Start from defaults
	first, err := s.GetPowerSettings()
	if err != nil {
		t.Fatalf("GetPowerSettings: %v", err)
	}
	// Just flip networkMode to trusted_wifi
	first.NetworkMode = "trusted_wifi"
	if err := s.SetPowerSettings(first); err != nil {
		t.Fatalf("SetPowerSettings: %v", err)
	}
	out, err := s.GetPowerSettings()
	if err != nil {
		t.Fatalf("GetPowerSettings: %v", err)
	}
	if out.NetworkMode != "trusted_wifi" {
		t.Errorf("expected trusted_wifi, got %q", out.NetworkMode)
	}
	if out.TrustedSSIDs == nil {
		t.Errorf("TrustedSSIDs should never be nil")
	}
}

func TestPowerSettings_AddSSID(t *testing.T) {
	s := openTest(t)
	p, _ := s.GetPowerSettings()
	p.NetworkMode = "trusted_wifi"
	if err := s.SetPowerSettings(p); err != nil {
		t.Fatalf("first set: %v", err)
	}
	p, _ = s.GetPowerSettings()
	p.TrustedSSIDs = append(p.TrustedSSIDs, "HomeWiFi")
	if err := s.SetPowerSettings(p); err != nil {
		t.Fatalf("second set: %v", err)
	}
	out, _ := s.GetPowerSettings()
	if len(out.TrustedSSIDs) != 1 || out.TrustedSSIDs[0] != "HomeWiFi" {
		t.Errorf("expected [HomeWiFi], got %v", out.TrustedSSIDs)
	}
}
