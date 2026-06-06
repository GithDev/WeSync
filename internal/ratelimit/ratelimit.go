// Package ratelimit provides a simple per-key fixed-window rate limiter.
// No external dependencies — uses only sync and time.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter counts events per key within a fixed time window.
// Old windows are lazily evicted on access to avoid a background goroutine.
type Limiter struct {
	mu        sync.Mutex
	entries   map[string]*bucket
	limit     int
	window    time.Duration
	lastSweep time.Time // last time Allow evicted stale entries
}

type bucket struct {
	count     int
	windowEnd time.Time
}

// New returns a Limiter that allows at most limit events per key per window.
func New(limit int, window time.Duration) *Limiter {
	return &Limiter{
		entries: make(map[string]*bucket),
		limit:   limit,
		window:  window,
	}
}

// Allow returns true if the key is within its rate limit, false if it has exceeded it.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()

	// Amortized eviction: sweep stale entries at most once per window. Without
	// this the map grows one entry per distinct key ever seen and never shrinks —
	// a memory-growth/DoS vector since the peer server is keyed by remote IP. The
	// once-per-window guard keeps the O(n) sweep off the hot path, and needs no
	// background goroutine (so the limiter stays self-contained).
	if now.After(l.lastSweep.Add(l.window)) {
		for k, b := range l.entries {
			if now.After(b.windowEnd) {
				delete(l.entries, k)
			}
		}
		l.lastSweep = now
	}

	b, ok := l.entries[key]
	if !ok || now.After(b.windowEnd) {
		l.entries[key] = &bucket{count: 1, windowEnd: now.Add(l.window)}
		return true
	}
	if b.count >= l.limit {
		return false
	}
	b.count++
	return true
}

// Evict removes stale entries to bound memory usage.
// Call periodically if the key space is large and unbounded.
func (l *Limiter) Evict() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, b := range l.entries {
		if now.After(b.windowEnd) {
			delete(l.entries, k)
		}
	}
}
