package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

func TestAllow_WithinLimit(t *testing.T) {
	l := New(5, time.Minute)
	for i := 0; i < 5; i++ {
		if !l.Allow("ip1") {
			t.Fatalf("expected Allow true on attempt %d", i+1)
		}
	}
}

func TestAllow_ExceedsLimit(t *testing.T) {
	l := New(3, time.Minute)
	for i := 0; i < 3; i++ {
		l.Allow("ip1")
	}
	if l.Allow("ip1") {
		t.Error("expected Allow false after limit exceeded")
	}
}

func TestAllow_DifferentKeys_Independent(t *testing.T) {
	l := New(2, time.Minute)
	l.Allow("a")
	l.Allow("a")
	// "a" is now blocked, but "b" should be unaffected.
	if !l.Allow("b") {
		t.Error("expected Allow true for unrelated key")
	}
	if l.Allow("a") {
		t.Error("expected Allow false for exhausted key")
	}
}

func TestAllow_WindowResets(t *testing.T) {
	l := New(2, 50*time.Millisecond)
	l.Allow("ip1")
	l.Allow("ip1")
	if l.Allow("ip1") {
		t.Error("expected blocked within window")
	}
	time.Sleep(60 * time.Millisecond)
	if !l.Allow("ip1") {
		t.Error("expected allowed after window reset")
	}
}

// TestAllow_AutoEvictsStaleEntries guards the memory-growth fix: Allow itself
// must sweep stale entries (at most once per window), so the map can't grow one
// entry per distinct key forever without anyone calling Evict().
func TestAllow_AutoEvictsStaleEntries(t *testing.T) {
	l := New(5, 50*time.Millisecond)
	for i := 0; i < 100; i++ {
		l.Allow(fmt.Sprintf("ip-%d", i))
	}
	// Let every window (and the once-per-window sweep guard) expire, then a single
	// Allow must drop all 100 stale entries, leaving only the key it just touched.
	time.Sleep(60 * time.Millisecond)
	l.Allow("fresh")
	l.mu.Lock()
	n := len(l.entries)
	l.mu.Unlock()
	if n != 1 {
		t.Errorf("expected stale entries auto-swept by Allow (want 1), got %d", n)
	}
}

func TestEvict_RemovesStaleEntries(t *testing.T) {
	l := New(5, 50*time.Millisecond)
	l.Allow("ip1")
	l.Allow("ip2")
	time.Sleep(60 * time.Millisecond)
	l.Evict()
	l.mu.Lock()
	n := len(l.entries)
	l.mu.Unlock()
	if n != 0 {
		t.Errorf("expected 0 entries after evict, got %d", n)
	}
}
