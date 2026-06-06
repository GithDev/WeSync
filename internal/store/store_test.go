package store

import (
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

const (
	devA = "AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA"
	devB = "BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB"
)

// ── Settings ──────────────────────────────────────────────────────────────────

func TestSettings_DefaultEmpty(t *testing.T) {
	s := openTest(t)
	name, err := s.GetName()
	if err != nil {
		t.Fatal(err)
	}
	if name != "" {
		t.Errorf("expected empty name, got %q", name)
	}
}

func TestSettings_SetAndGet(t *testing.T) {
	s := openTest(t)
	if err := s.SetName("laptop"); err != nil {
		t.Fatal(err)
	}
	name, _ := s.GetName()
	if name != "laptop" {
		t.Errorf("expected 'laptop', got %q", name)
	}
}


