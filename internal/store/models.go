package store

// Settings stores WeSync-specific configuration.
// This is the only table remaining — all other state lives in Syncthing.
type Settings struct {
	ID                uint   `gorm:"primaryKey"`
	Name              string `gorm:"not null;default:''"`
	ConnectivityLevel int    `gorm:"not null;default:1"` // 1=local, 2=discovery, 3=relay
	Introducer        bool   `gorm:"not null;default:true"`
	// Discoverability preference (UDP announce). Persisted so the visibility
	// toggle survives restarts; actual announcing is still gated on foreground.
	Visible bool `gorm:"not null;default:true"`

	// Power settings — Android-only today (desktop runs ST continuously and
	// ignores the gate). Default trigger is on_change_poll with a 4 h (240 min)
	// alarm interval.
	//
	// Explicit `column:` tags are required because GORM's default
	// snake_case naming splits the SSIDs acronym in surprising ways
	// (it became power_trusted_s_s_i_ds). Pinning the names keeps the
	// map-based UPDATE in store.go aligned with the actual schema.
	PowerSyncTrigger         string `gorm:"column:power_sync_trigger;not null;default:'on_change_poll'"`
	PowerPeriodicMinutes     int    `gorm:"column:power_periodic_minutes;not null;default:240"`
	PowerOnChangePollMinutes int    `gorm:"column:power_on_change_poll_minutes;not null;default:5"`
	PowerScheduledTimes  string `gorm:"column:power_scheduled_times;not null;default:'[]'"`
	PowerNetworkMode     string `gorm:"column:power_network_mode;not null;default:'any_wifi'"`
	PowerTrustedSSIDs    string `gorm:"column:power_trusted_ssids;not null;default:'[]'"`
	// Pause syncing when the battery is LOW — the level at which Android shows
	// its low-battery warning (ACTION_BATTERY_LOW). NOT battery-saver mode,
	// which is a different signal. The column name predates the rename; kept
	// as-is to avoid a needless migration.
	PowerPauseWhenBatteryLow bool `gorm:"column:power_pause_on_saver;not null;default:true"`

	// When charging, run ST continuously regardless of the trigger/battery
	// gate — battery isn't a concern when plugged in. Still respects the
	// network gate (privacy + metered). Default OFF: it's an opt-in power
	// trade-off, and a default-on value (combined with an earlier flaky charging
	// read) is what made fresh installs never sleep. Detection is now status-
	// based (see PowerController.isCharging), so when a user does enable it, it
	// only holds ST up while genuinely on power.
	PowerKeepSyncingWhileCharging bool `gorm:"column:power_keep_syncing_while_charging;not null;default:false"`
	// Don't sync on connections that cost extra: roaming abroad, or a metered
	// WiFi (a phone hotspot, tethering, or a network the user marked metered).
	// NOT ordinary metered cellular — Android marks all cellular metered, so
	// that's just the user's normal data plan and still syncs. Default ON —
	// protective, consistent with the other power toggles where true = safe.
	PowerBlockMeteredRoaming bool `gorm:"column:power_block_metered_roaming;not null;default:true"`

	// One-shot marker for the gate's "unpause folders the old gate left
	// paused" migration. The gate no longer touches per-folder pause
	// state, so this must run exactly once on upgrade — never again, or
	// it would clobber folders the user intentionally paused via the UI.
	UnpauseMigrationDone bool `gorm:"column:unpause_migration_done;not null;default:false"`
}

// FolderWithDevices is the folder representation used throughout the API layer.
// Populated by listFolders() which reads directly from Syncthing — no DB involved.
type FolderWithDevices struct {
	Folder
	DeviceIDs   []string          `json:"deviceIDs"`
	DeviceTypes map[string]string `json:"deviceTypes,omitempty"`
	// DeviceAccepted is the boolean shorthand "B accepted F" — kept for the
	// existing frontend contract. New code should consume DeviceState instead.
	DeviceAccepted map[string]bool `json:"deviceAccepted"`
	// DeviceTrusted: true = explicitly paired, false = other device (introduced via mesh).
	DeviceTrusted map[string]bool `json:"deviceTrusted"`
	// DeviceState is the per-device FolderRelationState — the central enum.
	// Canonical list + predicates live in internal/node/derive.go (mirrored in
	// docs/state-model.md): not-member, invited, and accepted-{paused-local,
	// paused-remote, syncing, sending, stalled, idle, behind-offline, offline}.
	// All UI state decisions go through this string, not DeviceAccepted.
	DeviceState map[string]string `json:"deviceState"`
	// DevicePeer carries per-device sync detail for this folder: how much B still
	// needs FROM US and B's completion of our data. Keyed by deviceID, present
	// only for devices with pending or in-progress work. Drives the honest
	// "Sending — X left" / "N items not yet sent" labels alongside DeviceState.
	DevicePeer map[string]PeerDetail `json:"devicePeer,omitempty"`
}

// PeerDetail is the numeric companion to a device's FolderRelationState: what B
// still lacks of our data for this folder.
type PeerDetail struct {
	NeedBytes  int64   `json:"needBytes"`
	NeedItems  int     `json:"needItems"`
	Completion float64 `json:"completion"` // 0–100
}

// PowerEvent is a short log entry for one transition in the power gate.
// Stored persistently so the user can inspect what happened while the
// process was dead — the whole point of an ephemeral-app model is that
// you can't watch logs live. Trimmed automatically; we keep ~200 most
// recent entries.
type PowerEvent struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Timestamp int64  `gorm:"not null;index" json:"-"` // unix ms — sortable, sub-second resolution
	TimeISO   string `gorm:"-" json:"timestamp"`      // populated by reader, never written
	Kind      string `gorm:"not null" json:"kind"`    // start, stop, trigger, sync, gate, error
	Message   string `gorm:"not null" json:"message"`
}

// Folder is the basic folder metadata returned to the frontend.
type Folder struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Path  string `json:"path"`
	Type  string `json:"type"` // sendonly | receiveonly | sendreceive
}
