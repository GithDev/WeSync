package discovery

import "testing"

// TestShouldAnnounce_SplitInputs locks in the split-input model that the cycle
// tests (which flip both inputs together via enableDisc) can't exercise:
// announcing is the AND of the user preference and the debounce gate
// (announceReady), the preference survives a background↔foreground cycle, and
// going background silences announce without touching the preference.
// SetForeground(true) alone does NOT enable announcing — the 2-s debounce
// timer must fire first (simulated here by setting announceReady directly).
// Pure atomics — no sockets, so not net-gated.
func TestShouldAnnounce_SplitInputs(t *testing.T) {
	var s Service // zero value: only the atomics are used here

	if s.shouldAnnounce() {
		t.Fatal("fresh service must not announce")
	}

	// Preference on, but still background → must NOT announce.
	s.SetWantAnnounce(true)
	if s.shouldAnnounce() {
		t.Fatal("preference on while background must not announce")
	}

	// Foreground arrives but debounce has not yet cleared → still must NOT announce.
	s.SetForeground(true)
	if s.shouldAnnounce() {
		t.Fatal("SetForeground(true) before debounce must not announce")
	}

	// Debounce clears (simulating the 2-s timer firing).
	s.announceReady.Store(true)
	if !s.shouldAnnounce() {
		t.Fatal("preference on + debounce cleared must announce")
	}

	// Background cycle silences announce immediately and must preserve the preference.
	s.SetForeground(false)
	if s.shouldAnnounce() {
		t.Fatal("background must silence announce immediately")
	}
	if !s.WantAnnounce() {
		t.Fatal("preference must survive a background cycle (the clobber bug)")
	}

	// Returning to foreground does NOT immediately resume — debounce required again.
	s.SetForeground(true)
	if s.shouldAnnounce() {
		t.Fatal("foreground restore before debounce must not announce")
	}
	s.announceReady.Store(true)
	if !s.shouldAnnounce() {
		t.Fatal("foreground restore after debounce must resume announce")
	}

	// Turning the preference off while foreground + debounce cleared silences announce.
	s.SetWantAnnounce(false)
	if s.shouldAnnounce() {
		t.Fatal("preference off must silence announce even in foreground")
	}
}
