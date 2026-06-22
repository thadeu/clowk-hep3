package ingest

import (
	"sync"
	"time"
)

// Deduper drops duplicate payloads seen within a short time window.
// Identical SIP messages arrive when more than one capture point (or a
// retransmit) mirrors the same packet; storing both would double every
// count. heplify uses a 500ms window — we match that intent.
//
// Implementation is a two-generation hash set rotated every window:
// O(1) per check, memory bounded to ~2 windows of unique payloads, no
// per-entry timestamp bookkeeping. An entry lives between 1× and 2× the
// window depending on arrival phase, which is the right ballpark for
// "near-instant duplicate".
//
// now is injected per call so tests drive the clock deterministically.
type Deduper struct {
	mu       sync.Mutex
	window   time.Duration
	cur      map[uint64]struct{}
	prev     map[uint64]struct{}
	rotateAt time.Time
}

// NewDeduper builds a Deduper with the given window. A non-positive
// window yields a Deduper that never dedups (every Seen returns false).
func NewDeduper(window time.Duration) *Deduper {
	return &Deduper{
		window: window,
		cur:    make(map[uint64]struct{}),
		prev:   make(map[uint64]struct{}),
	}
}

// Seen records hash h and reports whether it was already present within
// the window.
func (d *Deduper) Seen(h uint64, now time.Time) bool {
	if d.window <= 0 {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.rotateAt.IsZero() {
		d.rotateAt = now.Add(d.window)
	}

	if !now.Before(d.rotateAt) {
		// One window elapsed → age cur into prev. Two or more windows
		// elapsed (idle gap) → BOTH generations are stale, so drop them
		// entirely. Without this, a single rotation would keep an entry
		// alive in prev forever across a long quiet period.
		if now.Before(d.rotateAt.Add(d.window)) {
			d.prev = d.cur
		} else {
			d.prev = make(map[uint64]struct{})
		}

		d.cur = make(map[uint64]struct{})
		d.rotateAt = now.Add(d.window)
	}

	if _, ok := d.cur[h]; ok {
		return true
	}

	if _, ok := d.prev[h]; ok {
		// Refresh into the current generation so a steady stream of the
		// same payload keeps being recognised across rotations.
		d.cur[h] = struct{}{}

		return true
	}

	d.cur[h] = struct{}{}

	return false
}
