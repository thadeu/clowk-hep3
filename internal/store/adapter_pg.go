package store

import (
	"context"
	"log/slog"

	"github.com/thadeu/clowk-hep3/internal/config"
	"github.com/thadeu/clowk-hep3/internal/models"
)

// openPG returns the Postgres-backed Store. The implementation lives in
// internal/models (the original writer that owns the schema via
// golang-migrate and batches inserts); it already satisfies Store, so this
// adapter is just the constructor wired to config. Postgres is the opt-in
// fallback behind HEP_STORE — kept intact so it can be flipped back on, or
// removed once NDJSON proves out.
func openPG(ctx context.Context, cfg config.Config, logger *slog.Logger) (Store, error) {
	return models.NewSipMessages(ctx, cfg.DatabaseURL, cfg.DBBulk, cfg.DBTimer, logger)
}
