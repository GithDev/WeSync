package mobile

import (
	"log"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"wesync/internal/stmanager"

	"github.com/syncthing/notify"
)

// This file is the on_change file watcher: a lightweight inotify watch over the
// shared folder paths that opens a sync session shortly after a local change,
// so ST (the heavy part) can sleep between syncs instead of sitting resident.
//
// Detection here is BEST-EFFORT on purpose. inotify misses events on some
// Android storage (FUSE/emulated), and the process can be killed — so the
// watcher is the LOW-LATENCY path, not the guarantee. The guarantee comes from
// elsewhere: ST does a full scan on every cold-start catch-up and on every
// backstop tick, which sweeps up anything the watcher missed. A missed event
// therefore delays a sync, it doesn't lose one.
//
// Debounce is a FIXED delay from the FIRST change, not a reset-on-every-event
// window: once a change schedules a wake, further changes are ignored until it
// fires. ST scans the whole tree when it wakes so it picks up the entire flurry
// regardless — and a fixed delay can't be pushed indefinitely by continuous
// edits the way a reset-debounce can, which matters for a sync tool.

// changeBatcher coalesces a stream of change notifications into at most one
// fire() per delay window, measured from the first change in an idle period.
// fire is injected so the watcher wires it to OpenSyncSession while tests can
// observe it directly.
type changeBatcher struct {
	mu      sync.Mutex
	delay   time.Duration
	fire    func()
	pending bool
}

func newChangeBatcher(delay time.Duration, fire func()) *changeBatcher {
	return &changeBatcher{delay: delay, fire: fire}
}

// notifyChange records a change. The first change in an idle period schedules
// fire() after delay; changes while a fire is pending are dropped.
func (b *changeBatcher) notifyChange() {
	b.mu.Lock()
	if b.pending {
		b.mu.Unlock()
		return
	}
	b.pending = true
	b.mu.Unlock()
	time.AfterFunc(b.delay, func() {
		b.mu.Lock()
		b.pending = false
		b.mu.Unlock()
		b.fire()
	})
}

type watcher struct {
	ch      chan notify.EventInfo
	batcher *changeBatcher
}

var (
	// watchMu serialises the watcher lifecycle so a burst of settings changes
	// (on→off→on) can't leave two watchers, or a half-torn-down one, behind.
	watchMu   sync.Mutex
	fileWatch *watcher // non-nil only while the on_change watcher is running
)

// updateWatcher starts the watcher in on_change and stops it everywhere else.
// Runs the (blocking) start/stop off the caller's goroutine — registering an
// inotify watch walks the tree — serialised by watchMu.
func updateWatcher(active bool) {
	if active {
		go startWatcher()
	} else {
		go stopWatcher()
	}
}

// restartWatcherIfActive re-arms the watcher when the folder set changed, so a
// newly added/accepted folder is watched without an app restart. No-op unless
// we're in on_change.
func restartWatcherIfActive() {
	g.mu.Lock()
	onChange := g.settings.SyncTrigger == "on_change"
	g.mu.Unlock()
	if !onChange {
		return
	}
	go func() {
		stopWatcher()
		startWatcher()
	}()
}

func startWatcher() {
	watchMu.Lock()
	defer watchMu.Unlock()
	if fileWatch != nil {
		return // already running
	}

	folders, err := stmanager.Folders()
	if err != nil {
		log.Printf("watch: read folders: %v", err)
		return
	}

	g.mu.Lock()
	delayMin := g.settings.OnChangeDebounceMinutes
	g.mu.Unlock()
	delay := time.Duration(delayMin) * time.Minute
	if delay <= 0 {
		delay = time.Minute
	}

	ch := make(chan notify.EventInfo, 256)
	w := &watcher{
		ch: ch,
		batcher: newChangeBatcher(delay, func() {
			g.emitEvent("watch", "file change settled — opening sync session")
			OpenSyncSession()
		}),
	}

	watched := 0
	for _, f := range folders {
		if f.Path == "" {
			continue
		}
		// "/..." = recursive watch (syncthing/notify), the same mechanism ST's
		// own fsWatcher uses on these paths.
		if err := notify.Watch(filepath.Join(f.Path, "..."), ch, notify.All); err != nil {
			log.Printf("watch: %s: %v", f.Path, err)
			continue
		}
		watched++
	}
	if watched == 0 {
		notify.Stop(ch)
		log.Printf("watch: no folders to watch")
		return
	}

	fileWatch = w
	go w.consume()
	g.emitEvent("watch", "watching "+strconv.Itoa(watched)+" folder(s) for changes")
}

func stopWatcher() {
	watchMu.Lock()
	defer watchMu.Unlock()
	if fileWatch == nil {
		return
	}
	// Stop unregisters all watches on the channel before returning, so no send
	// can race the close below.
	notify.Stop(fileWatch.ch)
	close(fileWatch.ch)
	fileWatch = nil
}

// consume drains inotify events: every event marks us dirty (so the backstop
// tick keeps retrying even if a session can't start yet — e.g. peer offline)
// and feeds the batcher, which opens a session once the changes settle.
func (w *watcher) consume() {
	for range w.ch {
		markDirty()
		w.batcher.notifyChange()
	}
}
