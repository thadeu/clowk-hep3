package ingest

import (
	"testing"
	"time"
)

func TestDeduper_DuplicateWithinWindow(t *testing.T) {
	d := NewDeduper(500 * time.Millisecond)
	t0 := time.Unix(1700000000, 0)

	if d.Seen(42, t0) {
		t.Error("first sight reported as duplicate")
	}

	if !d.Seen(42, t0.Add(100*time.Millisecond)) {
		t.Error("second sight within window not reported as duplicate")
	}
}

func TestDeduper_DistinctHashes(t *testing.T) {
	d := NewDeduper(500 * time.Millisecond)
	t0 := time.Unix(1700000000, 0)

	if d.Seen(1, t0) {
		t.Error("hash 1 first sight reported duplicate")
	}

	if d.Seen(2, t0) {
		t.Error("hash 2 (different) reported duplicate")
	}
}

// After two full window rotations an old hash must be forgotten — the
// dedup window is bounded, not permanent.
func TestDeduper_ExpiresAfterWindow(t *testing.T) {
	d := NewDeduper(500 * time.Millisecond)
	t0 := time.Unix(1700000000, 0)

	d.Seen(42, t0)

	// Advance past two rotations (cur→prev→dropped).
	later := t0.Add(1200 * time.Millisecond)

	if d.Seen(42, later) {
		t.Error("hash still considered duplicate after 2+ windows; window is not bounded")
	}
}

func TestDeduper_ZeroWindowNeverDedups(t *testing.T) {
	d := NewDeduper(0)
	t0 := time.Unix(1700000000, 0)

	first := d.Seen(7, t0)
	second := d.Seen(7, t0)

	if first || second {
		t.Error("zero window should never report duplicates")
	}
}
