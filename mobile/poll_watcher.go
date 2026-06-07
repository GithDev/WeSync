package mobile

import (
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"wesync/internal/stmanager"
)

var poll struct {
	mu   sync.Mutex
	dirs map[string]time.Time // directory mtime snapshot; nil = cold start
}

// pollCheckChanged compares directory mtimes against the previous snapshot.
// Returns true when any directory was structurally modified — files or
// subdirectories added, deleted, or renamed inside it — all of which update
// the directory's own mtime on Linux (Android included).
//
// Content-only modifications (editing an existing file's bytes) do NOT
// update the parent directory's mtime and are therefore not detected here.
// For receive/sendreceive folders this is acceptable: OnTriggerAlarm opens
// a session via the anyFolderReceives backstop regardless of this result, so
// peer changes always sync. For sendonly folders a pure in-place edit will
// not trigger until a structural change also occurs in that folder.
//
// Returns true on cold start (nil snapshot) to trigger a catch-up sync.
// Directories only — a typical 10 000-file tree might have ~100 directories,
// so the "nothing changed" case costs ~100 stat()s instead of ~10 000.
func pollCheckChanged() bool {
	folders, err := stmanager.Folders()
	if err != nil || len(folders) == 0 {
		return false
	}
	next := make(map[string]time.Time)
	for _, f := range folders {
		scanDirMtimes(f.Path, next)
	}
	poll.mu.Lock()
	defer poll.mu.Unlock()
	if poll.dirs == nil {
		poll.dirs = next
		return true // cold start: trigger catch-up sync
	}
	changed := dirSnapChanged(poll.dirs, next)
	poll.dirs = next
	return changed
}

// resetPollSnapshot discards the snapshot so the next pollCheckChanged call
// acts as a cold start. Call when the folder set changes so a newly added
// folder isn't silently skipped until the next structural change.
func resetPollSnapshot() {
	poll.mu.Lock()
	poll.dirs = nil
	poll.mu.Unlock()
}

func scanDirMtimes(root string, snap map[string]time.Time) {
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error { //nolint:errcheck
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		// Skip all hidden directories (dot-prefix). This covers Syncthing's own
		// internal dirs (.stversions, .stfolder), version-control (.git), and
		// large tool caches (node_modules/.cache etc.) without hardcoding names.
		// The root itself is exempted so a folder path starting with '.' still works.
		if path != root && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		snap[path] = info.ModTime()
		return nil
	})
}

func dirSnapChanged(old, next map[string]time.Time) bool {
	if len(old) != len(next) {
		return true
	}
	for path, t := range next {
		if ot, ok := old[path]; !ok || !ot.Equal(t) {
			return true
		}
	}
	return false
}
