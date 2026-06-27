// Package store is clowk-hep3's pluggable persistence layer. The writer
// funnels every parsed SIP message into a Store; the HEP_STORE env selects
// the backend(s):
//
//	HEP_STORE=ndjson        (default) append NDJSON to the shared volume
//	HEP_STORE=pg            write to Postgres (opt-in fallback)
//	HEP_STORE=pg,ndjson     dual-write to both (de-risk / warm standby)
//
// NDJSON is the primary path; Postgres is kept behind the same seam so it
// can be flipped back on with one env var, and removed later if NDJSON
// proves out. See ~/code/plans/hep3-stack.md (section 2026-06-26).
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/thadeu/clowk-hep3/internal/config"
	"github.com/thadeu/clowk-hep3/internal/models"
)

// Store is a write sink for SIP messages plus retention + lifecycle. It is
// a superset of ingest.Sink (Enqueue), so any Store can drive the
// processor directly.
type Store interface {
	// Enqueue hands a message to the backend's batching writer. Best
	// effort: the hot path must not block on a slow backend.
	Enqueue(models.SipMessage)
	// Purge drops data older than cutoff, returning the units removed
	// (rows for pg, files for ndjson).
	Purge(ctx context.Context, cutoff time.Time) (int64, error)
	// Close flushes buffered messages and releases resources.
	Close() error
}

// Open builds the Store selected by cfg.Stores (already normalized +
// validated by config.Load). A single backend is returned directly; two or
// more are wrapped in a tee that fans writes out to each.
func Open(ctx context.Context, cfg config.Config, logger *slog.Logger) (Store, error) {
	if logger == nil {
		logger = slog.Default()
	}

	stores := make([]Store, 0, len(cfg.Stores))

	for _, name := range cfg.Stores {
		var (
			s   Store
			err error
		)

		switch name {
		case "ndjson":
			s, err = openNDJSON(cfg.DataDir, cfg.DBBulk, cfg.DBTimer, logger)
		case "pg":
			s, err = openPG(ctx, cfg, logger)
		default:
			err = fmt.Errorf("unknown store %q", name)
		}

		if err != nil {
			closeAll(stores)

			return nil, fmt.Errorf("store %s: %w", name, err)
		}

		stores = append(stores, s)
	}

	if len(stores) == 0 {
		return nil, errors.New("HEP_STORE selected no backends")
	}

	if len(stores) == 1 {
		return stores[0], nil
	}

	return tee(stores), nil
}

func closeAll(stores []Store) {
	for _, s := range stores {
		_ = s.Close()
	}
}

// tee fans Enqueue out to every backend (dual-write). Each backend keeps
// its own buffer + goroutine, so they drain independently; Purge and Close
// aggregate across all and join their errors.
type tee []Store

func (t tee) Enqueue(m models.SipMessage) {
	for _, s := range t {
		s.Enqueue(m)
	}
}

func (t tee) Purge(ctx context.Context, cutoff time.Time) (int64, error) {
	var (
		total int64
		errs  []error
	)

	for _, s := range t {
		n, err := s.Purge(ctx, cutoff)
		total += n

		if err != nil {
			errs = append(errs, err)
		}
	}

	return total, errors.Join(errs...)
}

func (t tee) Close() error {
	var errs []error

	for _, s := range t {
		if err := s.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
