// Command clowk-hep3 is the SIP capture WRITER: it receives HEP3
// datagrams from a SIP proxy (Kamailio/FreeSWITCH), extracts the SIP
// messages, and writes them to a shared Postgres. It serves no API — the
// REST layer that queries this data is the voodu-hep3 reader, which
// connects to the same DATABASE_URL.
//
//	DATABASE_URL=postgres://… clowk-hep3
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thadeu/clowk-hep3/internal/config"
	"github.com/thadeu/clowk-hep3/internal/ingest"
	"github.com/thadeu/clowk-hep3/internal/models"
)

// version is set via -ldflags at release time.
var version = "dev"

func main() {
	logger := log.New(os.Stderr, "", log.LstdFlags|log.LUTC)

	if err := run(logger); err != nil {
		logger.Fatalf("clowk-hep3: %v", err)
	}
}

func run(logger *log.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger.Printf("clowk-hep3 %s starting (hep=%s tcp=%q, writing to postgres)",
		version, cfg.HEPAddr, cfg.HEPTCPAddr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := models.NewSipMessages(ctx, cfg.DatabaseURL, cfg.DBBulk, cfg.DBTimer)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	proc := ingest.NewProcessor(ingest.Options{
		Sink:              st,
		DedupWindow:       500 * time.Millisecond,
		CorrelationHeader: cfg.CorrelationHeader,
		DiscardMethods:    cfg.DiscardSet(),
	})

	receiver := ingest.NewReceiver(proc, cfg.DBWorkers, logger)

	if cfg.RetentionDays > 0 {
		go retentionLoop(ctx, st, cfg.RetentionDays, logger)
	}

	if err := receiver.Run(ctx, cfg.HEPAddr, cfg.HEPTCPAddr); err != nil {
		return err
	}

	logger.Printf("clowk-hep3: shutting down")

	return nil
}

// retentionLoop deletes rows older than retentionDays once an hour.
func retentionLoop(ctx context.Context, st *models.SipMessages, retentionDays int, logger *log.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	purge := func() {
		cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)

		n, err := st.Purge(ctx, cutoff)
		if err != nil {
			logger.Printf("hep3: retention purge: %v", err)

			return
		}

		if n > 0 {
			logger.Printf("hep3: retention purged %d messages older than %d days", n, retentionDays)
		}
	}

	purge()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			purge()
		}
	}
}
