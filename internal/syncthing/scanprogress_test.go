package syncthing

import "testing"

func TestScanProgress(t *testing.T) {
	c := NewClient("http://localhost:0", "key")

	// No data yet → 0 while scanning.
	if got := c.takeScanProgress("f1", true); got != 0 {
		t.Fatalf("empty cache: got %v, want 0", got)
	}

	// total <= 0 means "size unknown" — skipped so the UI keeps the
	// indeterminate bar instead of showing a stuck 0%.
	c.setScanProgress("f1", 5, 0)
	if got := c.takeScanProgress("f1", true); got != 0 {
		t.Fatalf("total=0 should be ignored: got %v, want 0", got)
	}

	// Normal progress.
	c.setScanProgress("f1", 25, 100)
	if got := c.takeScanProgress("f1", true); got != 25 {
		t.Fatalf("progress: got %v, want 25", got)
	}

	// Overshoot is clamped to 100.
	c.setScanProgress("f1", 150, 100)
	if got := c.takeScanProgress("f1", true); got != 100 {
		t.Fatalf("overshoot clamp: got %v, want 100", got)
	}

	// Leaving the scanning state clears the entry so the next scan starts fresh.
	if got := c.takeScanProgress("f1", false); got != 0 {
		t.Fatalf("not-scanning read: got %v, want 0", got)
	}
	if got := c.takeScanProgress("f1", true); got != 0 {
		t.Fatalf("entry should have been cleared: got %v, want 0", got)
	}
}
