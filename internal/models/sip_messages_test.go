package models

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

// writerModel opens a writer against TEST_DATABASE_URL, skipping when it
// is unset (no Postgres available, e.g. local dev without Docker). CI
// sets it to a throwaway Postgres. Each test starts from a clean table.
func writerModel(t *testing.T) *SipMessages {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run Postgres-backed writer tests")
	}

	m, err := NewSipMessages(context.Background(), url, 200, time.Second, slog.Default())
	if err != nil {
		t.Fatalf("NewSipMessages: %v", err)
	}

	if _, err := m.DB().Exec("TRUNCATE sip_messages"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	t.Cleanup(func() { _ = m.Close() })

	return m
}

var base = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

// A write must populate the STORED generated columns from the JSONB body.
// This pins the migration contract: data->>'x' → column x.
func TestInsert_PopulatesGeneratedColumns(t *testing.T) {
	m := writerModel(t)

	if err := m.Insert([]SipMessage{{
		TS: base, CallID: "c1", XCID: "corr", Method: "INVITE",
		ResponseCode: 0, FromUser: "alice", ToUser: "555", CSeq: "1 INVITE",
	}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	var (
		callID, xcid, method, fromU, toU, cseq, ts string
		code                                       int
	)

	err := m.DB().QueryRow(
		`SELECT call_id, x_cid, method, response_code, from_user, to_user, cseq, ts
		   FROM sip_messages LIMIT 1`,
	).Scan(&callID, &xcid, &method, &code, &fromU, &toU, &cseq, &ts)
	if err != nil {
		t.Fatalf("scan generated columns: %v", err)
	}

	if callID != "c1" || xcid != "corr" || method != "INVITE" || code != 0 ||
		fromU != "alice" || toU != "555" || cseq != "1 INVITE" {
		t.Errorf("generated columns wrong: callid=%q xcid=%q method=%q code=%d from=%q to=%q cseq=%q",
			callID, xcid, method, code, fromU, toU, cseq)
	}

	if ts != FormatTS(base) {
		t.Errorf("ts column = %q, want %q", ts, FormatTS(base))
	}
}

func TestInsert_ResponseCodeFromJSON(t *testing.T) {
	m := writerModel(t)

	if err := m.Insert([]SipMessage{{TS: base, CallID: "c", ResponseCode: 486}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	var code int
	if err := m.DB().QueryRow(`SELECT response_code FROM sip_messages LIMIT 1`).Scan(&code); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if code != 486 {
		t.Errorf("response_code = %d, want 486 (cast from jsonb)", code)
	}
}

func TestPurge_DeletesOnlyOld(t *testing.T) {
	m := writerModel(t)

	if err := m.Insert([]SipMessage{
		{TS: base.Add(-48 * time.Hour), CallID: "old", Method: "INVITE"},
		{TS: base, CallID: "new", Method: "INVITE"},
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	n, err := m.Purge(context.Background(), base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}

	if n != 1 {
		t.Errorf("purged %d, want 1", n)
	}

	var remaining int
	_ = m.DB().QueryRow(`SELECT count(*) FROM sip_messages`).Scan(&remaining)

	if remaining != 1 {
		t.Errorf("remaining = %d, want 1 (only the new row)", remaining)
	}
}

// Enqueued messages must survive Close — the batcher flushes on shutdown.
func TestEnqueueFlushesOnClose(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run Postgres-backed writer tests")
	}

	m, err := NewSipMessages(context.Background(), url, 200, time.Hour, slog.Default()) // only Close flushes
	if err != nil {
		t.Fatalf("NewSipMessages: %v", err)
	}

	if _, err := m.DB().Exec("TRUNCATE sip_messages"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	for i := range 10 {
		m.Enqueue(SipMessage{TS: base.Add(time.Duration(i) * time.Second), CallID: "flushme", Method: "INVITE"})
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewSipMessages(context.Background(), url, 200, time.Second, slog.Default())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	var n int
	_ = reopened.DB().QueryRow(`SELECT count(*) FROM sip_messages WHERE call_id = 'flushme'`).Scan(&n)

	if n != 10 {
		t.Errorf("got %d messages after flush-on-close, want 10", n)
	}
}
