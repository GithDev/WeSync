package discovery

import "testing"

// TestShouldAnnounce_SplitInputs locks in the split-input model that the cycle
// tests (which flip both inputs together via enableDisc) can't exercise:
// announcing is the AND of the user preference and foreground, the preference
// survives a background↔foreground cycle, and going background silences announce
// without touching the preference. Pure atomics — no sockets, so not net-gated.
func TestShouldAnnounce_SplitInputs(t *testing.T) {
	var s Service // zero value: only the wantAnnounce/foreground atomics are used here

	if s.shouldAnnounce() {
		t.Fatal("fresh service must not announce")
	}

	// Preference on, but still background → must NOT announce.
	s.SetWantAnnounce(true)
	if s.shouldAnnounce() {
		t.Fatal("preference on while background must not announce")
	}

	// Foreground arrives → now announces.
	s.SetForeground(true)
	if !s.shouldAnnounce() {
		t.Fatal("preference on + foreground must announce")
	}

	// Background cycle silences announce but must preserve the preference.
	s.SetForeground(false)
	if s.shouldAnnounce() {
		t.Fatal("background must silence announce")
	}
	if !s.WantAnnounce() {
		t.Fatal("preference must survive a background cycle (the clobber bug)")
	}

	// Returning to foreground resumes announce purely from the preserved preference.
	s.SetForeground(true)
	if !s.shouldAnnounce() {
		t.Fatal("foreground restore must resume announce from the preserved preference")
	}

	// Turning the preference off while foreground silences announce.
	s.SetWantAnnounce(false)
	if s.shouldAnnounce() {
		t.Fatal("preference off must silence announce even in foreground")
	}
}
