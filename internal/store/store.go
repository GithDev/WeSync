package store

import (
	"fmt"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Store struct {
	db *gorm.DB
}

func Open(path string) (*Store, error) {
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	// SQLite has no concurrent writers; pin to a single connection so the
	// per-connection PRAGMAs below apply to every query and writes serialize.
	sqlDB.SetMaxOpenConns(1)

	if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		return nil, fmt.Errorf("set WAL: %w", err)
	}
	if err := db.Exec("PRAGMA foreign_keys=ON").Error; err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	if err := db.AutoMigrate(
		&Settings{},
		&PowerEvent{},
	); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// Ensure the singleton settings row exists; fail Open if it can't, so the
	// Get*/Set* helpers aren't operating against a row-less table.
	if err := db.FirstOrCreate(&Settings{}, Settings{ID: 1, Introducer: true}).Error; err != nil {
		return nil, fmt.Errorf("seed settings row: %w", err)
	}
	// Migrate existing rows that predate the Introducer field (default was false).
	if err := db.Model(&Settings{}).Where("id = 1 AND introducer = false").Update("introducer", true).Error; err != nil {
		return nil, fmt.Errorf("migrate introducer default: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) GetIntroducer() bool {
	var cfg Settings
	if err := s.db.First(&cfg, 1).Error; err != nil {
		return true // default on
	}
	return cfg.Introducer
}

func (s *Store) GetConnectivityLevel() int {
	var cfg Settings
	if err := s.db.First(&cfg, 1).Error; err != nil {
		return 1
	}
	if cfg.ConnectivityLevel < 1 || cfg.ConnectivityLevel > 3 {
		return 1
	}
	return cfg.ConnectivityLevel
}

func (s *Store) SetConnectivityLevel(level int) error {
	return s.db.Model(&Settings{}).Where("id = 1").Update("connectivity_level", level).Error
}

// GetVisible returns the persisted discoverability preference (default true).
func (s *Store) GetVisible() bool {
	var cfg Settings
	if err := s.db.First(&cfg, 1).Error; err != nil {
		return true
	}
	return cfg.Visible
}

func (s *Store) SetVisible(v bool) error {
	return s.db.Model(&Settings{}).Where("id = 1").Update("visible", v).Error
}

// UnpauseMigrationDone reports whether the one-shot "unpause folders the
// old gate left paused" migration has already run. False on a fresh row
// (and on upgrade from a schema without the column).
func (s *Store) UnpauseMigrationDone() bool {
	var cfg Settings
	if err := s.db.First(&cfg, 1).Error; err != nil {
		return false
	}
	return cfg.UnpauseMigrationDone
}

// MarkUnpauseMigrationDone records that the one-shot unpause migration has
// run so it never runs again (which would clobber user-paused folders).
func (s *Store) MarkUnpauseMigrationDone() error {
	return s.db.Model(&Settings{}).Where("id = ?", 1).Update("unpause_migration_done", true).Error
}

func (s *Store) GetName() (string, error) {
	var cfg Settings
	if err := s.db.First(&cfg, 1).Error; err != nil {
		return "", err
	}
	return cfg.Name, nil
}

func (s *Store) SetName(name string) error {
	return s.db.Model(&Settings{}).Where("id = ?", 1).Update("name", name).Error
}

// PowerSettings is the marshallable shape of the Settings table's power
// fields, returned by /api/power and consumed by the gate logic.
type PowerSettings struct {
	SyncTrigger              string   `json:"syncTrigger"`              // periodic | scheduled | on_change
	PeriodicMinutes          int      `json:"periodicMinutes"`          // when SyncTrigger == "periodic"
	ScheduledTimes           []string `json:"scheduledTimes"`           // "HH:MM" entries, when SyncTrigger == "scheduled"
	OnChangeDebounceMinutes  int      `json:"onChangeDebounceMinutes"`  // when SyncTrigger == "on_change"
	NetworkMode              string   `json:"networkMode"`              // trusted_wifi | any_wifi | any
	TrustedSSIDs             []string `json:"trustedSSIDs"`             // SSIDs that count as trusted when NetworkMode == "trusted_wifi"
	PauseWhenBatteryLow      bool     `json:"pauseWhenBatteryLow"`      // pause when the battery is low (Android low-battery warning level)
	KeepSyncingWhileCharging bool     `json:"keepSyncingWhileCharging"` // run continuously while plugged in
	BlockMeteredRoaming      bool     `json:"blockMeteredRoaming"`      // don't sync on roaming or metered WiFi (ordinary metered cellular still syncs)
}

func (s *Store) GetPowerSettings() (PowerSettings, error) {
	var cfg Settings
	if err := s.db.First(&cfg, 1).Error; err != nil {
		return PowerSettings{}, err
	}
	p := PowerSettings{
		SyncTrigger:              cfg.PowerSyncTrigger,
		PeriodicMinutes:          cfg.PowerPeriodicMinutes,
		OnChangeDebounceMinutes:  cfg.PowerOnChangeDebounceMinutes,
		NetworkMode:              cfg.PowerNetworkMode,
		PauseWhenBatteryLow:      cfg.PowerPauseWhenBatteryLow,
		KeepSyncingWhileCharging: cfg.PowerKeepSyncingWhileCharging,
		BlockMeteredRoaming:      cfg.PowerBlockMeteredRoaming,
	}
	_ = jsonUnmarshalStrings(cfg.PowerScheduledTimes, &p.ScheduledTimes)
	_ = jsonUnmarshalStrings(cfg.PowerTrustedSSIDs, &p.TrustedSSIDs)
	if p.ScheduledTimes == nil {
		p.ScheduledTimes = []string{}
	}
	if p.TrustedSSIDs == nil {
		p.TrustedSSIDs = []string{}
	}
	// "live" was removed in favour of periodic / scheduled / on_change.
	// Migrate any existing rows that still carry it so users don't end
	// up in a now-invalid state.
	if p.SyncTrigger == "" || p.SyncTrigger == "live" {
		p.SyncTrigger = "periodic"
	}
	if p.PeriodicMinutes <= 0 {
		p.PeriodicMinutes = 240
	}
	if p.OnChangeDebounceMinutes <= 0 {
		p.OnChangeDebounceMinutes = 1
	}
	if p.NetworkMode == "" {
		p.NetworkMode = "any_wifi"
	}
	return p, nil
}

func (s *Store) SetPowerSettings(p PowerSettings) error {
	scheduledJSON, _ := jsonMarshalStrings(p.ScheduledTimes)
	ssidsJSON, _ := jsonMarshalStrings(p.TrustedSSIDs)
	return s.db.Model(&Settings{}).Where("id = ?", 1).Updates(map[string]any{
		"power_sync_trigger":                p.SyncTrigger,
		"power_periodic_minutes":            p.PeriodicMinutes,
		"power_scheduled_times":             scheduledJSON,
		"power_on_change_debounce_minutes":  p.OnChangeDebounceMinutes,
		"power_network_mode":                p.NetworkMode,
		"power_trusted_ssids":               ssidsJSON,
		"power_pause_on_saver":              p.PauseWhenBatteryLow,
		"power_keep_syncing_while_charging": p.KeepSyncingWhileCharging,
		"power_block_metered_roaming":       p.BlockMeteredRoaming,
	}).Error
}
