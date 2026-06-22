// Package models holds the sip_messages model — clowk-hep3's WRITE side.
//
// clowk-hep3 is the collector/writer: it owns the schema (runs the
// golang-migrate migrations on boot) and writes parsed SIP messages to a
// shared Postgres. It does NOT read — the REST API that queries this data
// lives in the voodu-hep3 reader, which connects to the same DATABASE_URL.
//
// Every message is stored as one JSONB document in `data`; the query-hot
// fields are STORED generated columns over data->>'...' (see
// infra/migrations). Writes funnel through a single batching goroutine so
// Postgres sees one writer path with bulk-friendly transactions.
package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"

	migfs "github.com/thadeu/clowk-hep3/infra/migrations"
)

// tsLayout is the timestamp format stored in the JSON `data` (and thus
// the generated `ts` column): ISO8601, microsecond precision, no timezone
// (always UTC). Fixed-width ISO8601 makes lexicographic comparison equal
// chronological order, so the reader's `ts BETWEEN ? AND ?` / writer's
// `ts < ?` retention work as plain text.
const tsLayout = "2006-01-02 15:04:05.000000"

// FormatTS renders a time for storage/queries in the on-disk layout.
func FormatTS(t time.Time) string {
	return t.UTC().Format(tsLayout)
}

// ParseTS reads an on-disk timestamp back into a time.Time (UTC).
func ParseTS(s string) (time.Time, error) {
	return time.Parse(tsLayout, s)
}

// SipMessage is one parsed SIP message — the Go-facing write model.
type SipMessage struct {
	TS           time.Time
	CallID       string
	XCID         string
	Method       string
	ResponseCode int
	FromUser     string
	ToUser       string
	RURI         string
	SrcIP        string
	DstIP        string
	SrcPort      int
	DstPort      int
	NodeID       int
	UserAgent    string
	CSeq         string
	RawSIP       string
}

// jsonRecord is the on-disk JSON document stored in `data`. The keys MUST
// match the data->>'...' paths in the migration's generated columns.
type jsonRecord struct {
	TS           string `json:"ts"`
	CallID       string `json:"call_id"`
	XCID         string `json:"x_cid"`
	Method       string `json:"method"`
	ResponseCode int    `json:"response_code"`
	FromUser     string `json:"from_user"`
	ToUser       string `json:"to_user"`
	RURI         string `json:"ruri"`
	SrcIP        string `json:"src_ip"`
	DstIP        string `json:"dst_ip"`
	SrcPort      int    `json:"src_port"`
	DstPort      int    `json:"dst_port"`
	NodeID       int    `json:"node_id"`
	UserAgent    string `json:"user_agent"`
	CSeq         string `json:"cseq"`
	RawSIP       string `json:"raw_sip"`
}

func (m SipMessage) toRecord() jsonRecord {
	return jsonRecord{
		TS: FormatTS(m.TS), CallID: m.CallID, XCID: m.XCID, Method: m.Method,
		ResponseCode: m.ResponseCode, FromUser: m.FromUser, ToUser: m.ToUser,
		RURI: m.RURI, SrcIP: m.SrcIP, DstIP: m.DstIP, SrcPort: m.SrcPort,
		DstPort: m.DstPort, NodeID: m.NodeID, UserAgent: m.UserAgent,
		CSeq: m.CSeq, RawSIP: m.RawSIP,
	}
}

// SipMessages is the write-side repository for the sip_messages table.
type SipMessages struct {
	db    *sql.DB
	in    chan SipMessage
	bulk  int
	timer time.Duration
	done  chan struct{}
}

// NewSipMessages connects to Postgres, runs migrations, and starts the
// batching writer. databaseURL is the shared connection string.
func NewSipMessages(ctx context.Context, databaseURL string, bulk int, flush time.Duration) (*SipMessages, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	if err := migrateUp(db); err != nil {
		_ = db.Close()

		return nil, err
	}

	if bulk <= 0 {
		bulk = 200
	}

	if flush <= 0 {
		flush = 4 * time.Second
	}

	m := &SipMessages{
		db:    db,
		in:    make(chan SipMessage, bulk*4),
		bulk:  bulk,
		timer: flush,
		done:  make(chan struct{}),
	}

	go m.writeLoop()

	return m, nil
}

// migrateUp applies the embedded golang-migrate migrations.
func migrateUp(db *sql.DB) error {
	src, err := iofs.New(migfs.FS, ".")
	if err != nil {
		return fmt.Errorf("migration source: %w", err)
	}

	drv, err := migratepgx.WithInstance(db, &migratepgx.Config{})
	if err != nil {
		return fmt.Errorf("migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "pgx5", drv)
	if err != nil {
		return fmt.Errorf("migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}

	return nil
}

// Enqueue hands a message to the batching writer. Blocks only when the
// buffer is full (back-pressure against a stalled disk).
func (m *SipMessages) Enqueue(msg SipMessage) {
	m.in <- msg
}

// Close stops the writer (flushing buffered messages) and closes the DB.
func (m *SipMessages) Close() error {
	close(m.in)
	<-m.done

	return m.db.Close()
}

// DB exposes the handle for tests.
func (m *SipMessages) DB() *sql.DB {
	return m.db
}

func (m *SipMessages) writeLoop() {
	defer close(m.done)

	ticker := time.NewTicker(m.timer)
	defer ticker.Stop()

	batch := make([]SipMessage, 0, m.bulk)

	flush := func() {
		if len(batch) == 0 {
			return
		}

		if err := m.Insert(batch); err != nil {
			log.Printf("hep3 sip_messages: insert batch of %d failed: %v", len(batch), err)
		}

		batch = batch[:0]
	}

	for {
		select {
		case msg, ok := <-m.in:
			if !ok {
				flush()

				return
			}

			batch = append(batch, msg)

			if len(batch) >= m.bulk {
				flush()
			}

		case <-ticker.C:
			flush()
		}
	}
}

// Insert writes a batch of messages in one transaction, synchronously.
// The batching writeLoop uses it; external callers (tests) can use it for
// confirmed writes. The hot capture path prefers Enqueue.
func (m *SipMessages) Insert(batch []SipMessage) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO sip_messages (data) VALUES ($1::jsonb)`)
	if err != nil {
		_ = tx.Rollback()

		return err
	}

	for _, msg := range batch {
		raw, err := json.Marshal(msg.toRecord())
		if err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()

			return err
		}

		if _, err := stmt.Exec(string(raw)); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()

			return err
		}
	}

	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()

		return err
	}

	return tx.Commit()
}

// Purge deletes (destroys) messages older than cutoff, returning the rows
// removed. ts is a text column in fixed ISO8601, so the comparison is a
// plain lexicographic one.
func (m *SipMessages) Purge(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := m.db.ExecContext(ctx, `DELETE FROM sip_messages WHERE ts < $1`, FormatTS(cutoff))
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}
