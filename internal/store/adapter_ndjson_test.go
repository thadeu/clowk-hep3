package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thadeu/clowk-hep3/internal/models"
)

func mkMsg(ts time.Time, callID string) models.SipMessage {
	return models.SipMessage{TS: ts, CallID: callID, Method: "INVITE", FromUser: "alice"}
}

func listNDJSON(t *testing.T, dir string) []string {
	t.Helper()

	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var out []string

	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ndjsonPrefix) && strings.HasSuffix(e.Name(), ndjsonSuffix) {
			out = append(out, e.Name())
		}
	}

	return out
}

func readAllLines(t *testing.T, dir string) []string {
	t.Helper()

	var lines []string

	for _, name := range listNDJSON(t, dir) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}

		for _, ln := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			if ln != "" {
				lines = append(lines, ln)
			}
		}
	}

	return lines
}

// The full Enqueue → Close path must land every message as one parseable
// NDJSON line, with the record shape the reader will consume.
func TestNDJSON_WriteReadback(t *testing.T) {
	dir := t.TempDir()

	s, err := openNDJSON(dir, 2, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		s.Enqueue(mkMsg(time.Now().UTC(), "call-"+string(rune('a'+i))))
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	lines := readAllLines(t, dir)
	if len(lines) != 5 {
		t.Fatalf("want 5 NDJSON lines, got %d", len(lines))
	}

	var rec struct {
		TS     string `json:"ts"`
		CallID string `json:"call_id"`
		Method string `json:"method"`
	}

	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("line is not valid JSON: %v", err)
	}

	if rec.CallID == "" || rec.Method != "INVITE" {
		t.Errorf("record missing fields: %+v", rec)
	}

	if _, err := models.ParseTS(rec.TS); err != nil {
		t.Errorf("ts %q not in the shared layout: %v", rec.TS, err)
	}
}

// Crossing an hour boundary must roll to a new file (so retention can drop
// whole files). Synchronous white-box test: no writeLoop, controlled clock.
func TestNDJSON_RotatesHourly(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 6, 26, 14, 30, 0, 0, time.UTC)

	s := &ndjsonStore{dir: dir, bulk: 100, timer: time.Hour, now: func() time.Time { return clock }}

	if err := s.writeBatch([]models.SipMessage{mkMsg(clock, "a")}); err != nil {
		t.Fatal(err)
	}

	clock = clock.Add(time.Hour)

	if err := s.writeBatch([]models.SipMessage{mkMsg(clock, "b")}); err != nil {
		t.Fatal(err)
	}

	s.closeFile()

	if files := listNDJSON(t, dir); len(files) != 2 {
		t.Fatalf("want 2 hourly files, got %d: %v", len(files), files)
	}
}

// Within the same hour, all lines append to ONE file (no spurious rotation).
func TestNDJSON_SameHourSingleFile(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 6, 26, 14, 0, 0, 0, time.UTC)
	clock := base

	s := &ndjsonStore{dir: dir, bulk: 100, timer: time.Hour, now: func() time.Time { return clock }}

	for i := 0; i < 3; i++ {
		clock = base.Add(time.Duration(i*10) * time.Minute)

		if err := s.writeBatch([]models.SipMessage{mkMsg(clock, "c")}); err != nil {
			t.Fatal(err)
		}
	}

	s.closeFile()

	if files := listNDJSON(t, dir); len(files) != 1 {
		t.Fatalf("want 1 file for the same hour, got %d: %v", len(files), files)
	}
}

// Purge deletes whole buckets older than the cutoff and keeps the rest.
func TestNDJSON_PurgeDeletesOldBuckets(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 6, 26, 14, 0, 0, 0, time.UTC)

	s := &ndjsonStore{dir: dir, bulk: 100, timer: time.Hour, now: func() time.Time { return clock }}

	if err := s.writeBatch([]models.SipMessage{mkMsg(clock, "old")}); err != nil {
		t.Fatal(err)
	}

	clock = clock.Add(48 * time.Hour)

	if err := s.writeBatch([]models.SipMessage{mkMsg(clock, "new")}); err != nil {
		t.Fatal(err)
	}

	s.closeFile()

	if got := listNDJSON(t, dir); len(got) != 2 {
		t.Fatalf("precondition: want 2 files, got %v", got)
	}

	cutoff := clock.Add(-24 * time.Hour)

	n, err := s.Purge(context.Background(), cutoff)
	if err != nil {
		t.Fatal(err)
	}

	if n != 1 {
		t.Errorf("want 1 file purged, got %d", n)
	}

	if got := listNDJSON(t, dir); len(got) != 1 {
		t.Fatalf("want 1 surviving file, got %v", got)
	}
}

// Purge running concurrently with the writer must not race on the file
// state — guarded by the mutex. Validated under `go test -race`.
func TestNDJSON_PurgeConcurrentWithWrites(t *testing.T) {
	dir := t.TempDir()

	s, err := openNDJSON(dir, 4, 2*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		for i := 0; i < 2000; i++ {
			s.Enqueue(mkMsg(time.Now().UTC(), "x"))
		}
	}()

	for i := 0; i < 30; i++ {
		if _, err := s.Purge(context.Background(), time.Now().Add(-time.Hour)); err != nil {
			t.Errorf("purge: %v", err)
		}
	}

	<-done

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
