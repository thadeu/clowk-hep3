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
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/thadeu/clowk-hep3/internal/config"
	"github.com/thadeu/clowk-hep3/internal/ingest"
	"github.com/thadeu/clowk-hep3/internal/store"
)

// version is set via -ldflags at release time.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "clowk-hep3: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.SlogLevel()}))

	logger.Info("starting",
		"version", version, "hep", cfg.HEPAddr, "tcp", cfg.HEPTCPAddr,
		"store", strings.Join(cfg.Stores, ","), "log_level", cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg, logger)
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

	logger.Info("shutting down")

	return nil
}

// retentionLoop drops data older than retentionDays once an hour. The unit
// purged is backend-specific (Postgres rows / ndjson files).
func retentionLoop(ctx context.Context, st store.Store, retentionDays int, logger *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	purge := func() {
		cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)

		n, err := st.Purge(ctx, cutoff)
		if err != nil {
			logger.Error("retention purge", "err", err)

			return
		}

		if n > 0 {
			logger.Info("retention purged", "items", n, "older_than_days", retentionDays)
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
