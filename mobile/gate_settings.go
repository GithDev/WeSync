package mobile

import (
	"fmt"
	"log"
	"time"

	"wesync/internal/stmanager"
	"wesync/internal/syncthing"
)

// This file is where the gate touches the outside world for *settings*: it
// reads PowerSettings from SQLite and resets each folder's ST fsWatcher delay
// to the default. It also holds the one-shot folder-unpause migration and the
// shared ST client helper. Keeping these side-effecting bits out of
// gate_decision.go preserves that file's "pure, testable" property.

func refreshSettingsFromDB() error {
	g.mu.Lock()
	s := g.store
	g.mu.Unlock()
	if s == nil {
		return nil
	}
	settings, err := s.GetPowerSettings()
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.settings = settings
	g.mu.Unlock()
	// Diagnostic: surface every settings (re)load in Recent activity so a
	// runtime PUT /api/power can be told apart from the startup load — if no
	// "settings loaded" line appears when you change a setting, the
	// notify→ACTION_REARM→RefreshPowerSettings chain isn't reaching the gate.
	g.emitEvent("settings", fmt.Sprintf("loaded: trigger=%s net=%s", settings.SyncTrigger, settings.NetworkMode))
	// Reset each folder's ST fsWatcher to its default. ST only runs inside a
	// session and scans on start, so its own fsWatcher just needs the default
	// — this also clears any long delay a previous app version pushed in.
	go applyFSWatcherDelay()
	return nil
}

// onFoldersChanged reacts to the folder set changing (registered as
// api.FoldersChangedHook): reset the poll snapshot so the next on_change_poll
// alarm treats the new folder as a fresh start, and reset its ST fsWatcher delay.
func onFoldersChanged() {
	resetPollSnapshot()
	go applyFSWatcherDelay()
}

// applyFSWatcherDelay resets each folder's ST fsWatcherDelayS to ST's default.
// Old app versions pushed a custom delay into this field; ST's default is the
// right value now. Runs async so callers don't block on N round-trips to ST.
func applyFSWatcherDelay() {
	delaySecs := 10
	c, err := stClient()
	if err != nil {
		log.Printf("gate: applyFSWatcherDelay: stClient: %v", err)
		return
	}
	folders, err := c.ListFolders()
	if err != nil {
		log.Printf("gate: applyFSWatcherDelay: ListFolders: %v", err)
		return
	}
	applied := 0
	for _, f := range folders {
		if err := c.SetFolderFSWatcherDelay(f.ID, delaySecs); err != nil {
			log.Printf("gate: applyFSWatcherDelay: %s: %v", f.ID, err)
			continue
		}
		applied++
	}
	g.emitEvent("settings", fmt.Sprintf("reset ST fsWatcherDelay to %ds on %d folder(s)", delaySecs, applied))
}

// forceUnpauseAllFoldersOnce walks every folder and clears its paused flag
// exactly once per install, on the upgrade path from the old gate code
// (which paused folders to throttle sync). Guarded by a persistent flag so
// it never runs again — otherwise it would clobber folders the user
// intentionally paused via the UI. Runs async so ST's boot isn't blocked.
func forceUnpauseAllFoldersOnce() {
	g.mu.Lock()
	s := g.store
	g.mu.Unlock()
	if s == nil || s.UnpauseMigrationDone() {
		return
	}
	for i := 0; i < 20; i++ {
		time.Sleep(2 * time.Second)
		c, err := stClient()
		if err != nil {
			continue
		}
		folders, err := c.ListFolders()
		if err != nil {
			continue
		}
		anyUnpaused, anyFailed := false, false
		for _, f := range folders {
			if !f.Paused {
				continue
			}
			if err := c.SetFolderPaused(f.ID, false); err != nil {
				log.Printf("gate: migrate-unpause %s: %v", f.ID, err)
				anyFailed = true
				continue
			}
			anyUnpaused = true
		}
		if anyFailed {
			continue // retry on next iteration until all folders are unpaused
		}
		if anyUnpaused {
			g.emitEvent("migrate", "unpaused folders that the old gate had left paused")
		}
		if err := s.MarkUnpauseMigrationDone(); err != nil {
			log.Printf("gate: mark unpause migration done: %v", err)
		}
		return
	}
}

func stClient() (*syncthing.Client, error) {
	key, err := stmanager.APIKey()
	if err != nil {
		return nil, err
	}
	return syncthing.NewClient(stmanager.APIURL, key), nil
}
