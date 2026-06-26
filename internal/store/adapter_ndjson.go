package store

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/thadeu/clowk-hep3/internal/models"
)

const (
	ndjsonPrefix = "sip-"
	ndjsonSuffix = ".ndjson"
	// bucketLayout names one file per UTC hour. Fixed-width so lexical
	// order == chronological order: the reader enumerates sip-*.ndjson
	// sorted, and retention compares bucket strings directly.
	bucketLayout = "2006-01-02T15"
)

// ndjsonStore appends parsed SIP messages as NDJSON (one JSON document per
// line) to hourly-rotated files in a shared volume. A single writer
// goroutine owns all file I/O, so lines are never interleaved and the
// reader (voodu-hep3) can tail safely.
type ndjsonStore struct {
	dir   string
	bulk  int
	timer time.Duration
	now   func() time.Time

	in   chan models.SipMessage
	done chan struct{}

	// File state. cur/buf are touched only by writeLoop; curBucket is also
	// read by Purge (another goroutine), so the mutex guards it.
	mu        sync.Mutex
	cur       *os.File
	buf       *bufio.Writer
	curBucket string
}

func openNDJSON(dir string, bulk int, flush time.Duration) (*ndjsonStore, error) {
	return newNDJSONStore(dir, bulk, flush, time.Now)
}

// newNDJSONStore is the testable constructor: now is injected so rotation
// and retention can be driven deterministically.
func newNDJSONStore(dir string, bulk int, flush time.Duration, now func() time.Time) (*ndjsonStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("HEP_DATA_DIR is required for the ndjson store")
	}

	// 0750/0600: SIP records are PII. The reader (voodu-hep3) shares this
	// volume and must run as the same uid to read them.
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	if bulk <= 0 {
		bulk = 200
	}

	if flush <= 0 {
		flush = 4 * time.Second
	}

	s := &ndjsonStore{
		dir:   dir,
		bulk:  bulk,
		timer: flush,
		now:   now,
		in:    make(chan models.SipMessage, bulk*4),
		done:  make(chan struct{}),
	}

	go s.writeLoop()

	return s, nil
}

// Enqueue hands a message to the batching writer. Blocks only when the
// buffer is full (back-pressure against a stalled disk).
func (s *ndjsonStore) Enqueue(m models.SipMessage) {
	s.in <- m
}

// Close stops the writer (flushing buffered messages) and closes the file.
func (s *ndjsonStore) Close() error {
	close(s.in)
	<-s.done

	return nil
}

func (s *ndjsonStore) writeLoop() {
	defer close(s.done)

	ticker := time.NewTicker(s.timer)
	defer ticker.Stop()

	batch := make([]models.SipMessage, 0, s.bulk)

	flush := func() {
		if len(batch) == 0 {
			return
		}

		if err := s.writeBatch(batch); err != nil {
			log.Printf("hep3 ndjson: write batch of %d failed: %v", len(batch), err)
		}

		batch = batch[:0]
	}

	for {
		select {
		case m, ok := <-s.in:
			if !ok {
				flush()
				s.closeFile()

				return
			}

			batch = append(batch, m)

			if len(batch) >= s.bulk {
				flush()
			}

		case <-ticker.C:
			flush()
		}
	}
}

// writeBatch appends every message in the batch as a line, then flushes +
// fsyncs so the reader sees a durable, complete tail.
func (s *ndjsonStore) writeBatch(batch []models.SipMessage) error {
	if err := s.rotateIfNeeded(s.now()); err != nil {
		return err
	}

	for _, m := range batch {
		line, err := m.MarshalRecord()
		if err != nil {
			// A single un-marshalable record shouldn't sink the batch.
			log.Printf("hep3 ndjson: marshal record: %v", err)

			continue
		}

		if _, err := s.buf.Write(line); err != nil {
			return err
		}

		if err := s.buf.WriteByte('\n'); err != nil {
			return err
		}
	}

	if err := s.buf.Flush(); err != nil {
		return err
	}

	return s.cur.Sync()
}

// rotateIfNeeded opens the file for t's hour bucket, closing the previous
// one when the hour rolls over.
func (s *ndjsonStore) rotateIfNeeded(t time.Time) error {
	bucket := t.UTC().Format(bucketLayout)

	if s.cur != nil && bucket == s.curBucket {
		return nil
	}

	s.closeFile()

	name := filepath.Join(s.dir, ndjsonPrefix+bucket+ndjsonSuffix)

	// #nosec G304 -- name is dir (operator config) + a timestamp bucket; no
	// external/untrusted input reaches the path.
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open ndjson file: %w", err)
	}

	s.mu.Lock()
	s.cur = f
	s.buf = bufio.NewWriter(f)
	s.curBucket = bucket
	s.mu.Unlock()

	return nil
}

func (s *ndjsonStore) closeFile() {
	if s.cur == nil {
		return
	}

	if s.buf != nil {
		_ = s.buf.Flush()
	}

	_ = s.cur.Sync()
	_ = s.cur.Close()

	s.mu.Lock()
	s.cur = nil
	s.buf = nil
	s.curBucket = ""
	s.mu.Unlock()
}

// Purge deletes NDJSON files whose whole hour bucket is older than cutoff.
// A file at exactly the cutoff hour may still hold fresh lines, so it is
// kept; the file currently being written is never removed. Returns the
// number of files deleted.
func (s *ndjsonStore) Purge(_ context.Context, cutoff time.Time) (int64, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, err
	}

	cutBucket := cutoff.UTC().Format(bucketLayout)

	s.mu.Lock()
	active := s.curBucket
	s.mu.Unlock()

	var (
		removed int64
		errs    []error
	)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		name := e.Name()

		if !strings.HasPrefix(name, ndjsonPrefix) || !strings.HasSuffix(name, ndjsonSuffix) {
			continue
		}

		bucket := strings.TrimSuffix(strings.TrimPrefix(name, ndjsonPrefix), ndjsonSuffix)

		if bucket >= cutBucket || bucket == active {
			continue
		}

		if err := os.Remove(filepath.Join(s.dir, name)); err != nil {
			errs = append(errs, err)

			continue
		}

		removed++
	}

	return removed, errors.Join(errs...)
}
