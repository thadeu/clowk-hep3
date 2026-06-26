// Package config reads clowk-hep3's runtime configuration from the
// environment via struct tags (caarlos0/env), loading an optional .env
// first (godotenv). clowk-hep3 is the WRITER: it receives HEP3, parses
// SIP, and persists it. HEP_STORE selects the backend: "ndjson" (default,
// appends to a shared volume) and/or "pg" (Postgres). The reader
// (voodu-hep3) consumes the same backend.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Config is the fully-resolved runtime configuration.
type Config struct {
	// HEPAddr is the UDP listen address for HEP3 datagrams.
	HEPAddr string `env:"HEP_ADDR" envDefault:"0.0.0.0:9060"`
	// HEPTCPAddr, when set, also accepts HEP3 over TCP.
	HEPTCPAddr string `env:"HEP_TCP_ADDR"`

	// Stores selects the write backend(s): "ndjson" (default), "pg", or a
	// comma list for dual-write ("pg,ndjson"). Normalized in Load.
	Stores []string `env:"HEP_STORE" envSeparator:"," envDefault:"ndjson"`
	// DataDir is where the ndjson store appends its files (shared volume).
	DataDir string `env:"HEP_DATA_DIR" envDefault:"/data"`
	// DatabaseURL is the shared Postgres connection string — required only
	// when the pg store is selected. clowk-hep3 owns the schema (migrates
	// on boot) on the pg path.
	DatabaseURL string `env:"DATABASE_URL"`

	// DBBulk is the write batch size (both backends).
	DBBulk int `env:"HEP_DB_BULK" envDefault:"200"`
	// DBTimer is the max time a partial batch waits before flushing.
	DBTimer time.Duration `env:"HEP_DB_TIMER" envDefault:"4s"`
	// DBWorkers is the number of parallel decode workers.
	DBWorkers int `env:"HEP_DB_WORKERS" envDefault:"4"`

	// RetentionDays drops data older than this. Zero disables.
	RetentionDays int `env:"HEP_RETENTION_DAYS" envDefault:"30"`

	// CorrelationHeader is the SIP header used to stitch B2BUA leg pairs.
	CorrelationHeader string `env:"HEP_CID" envDefault:"X-CID"`
	// DiscardMethods are SIP request methods dropped before storage.
	DiscardMethods []string `env:"HEP_EXCEPT_METHODS" envSeparator:","`
}

const discardMethodsEnv = "HEP_EXCEPT_METHODS"

// HasStore reports whether the named backend is selected.
func (c Config) HasStore(name string) bool {
	for _, s := range c.Stores {
		if s == name {
			return true
		}
	}

	return false
}

// Load resolves configuration: optional .env, then environment overlay.
func Load() (Config, error) {
	_ = godotenv.Load()

	var c Config

	if err := env.Parse(&c); err != nil {
		return Config{}, err
	}

	// Normalize + validate the store list.
	seen := map[string]bool{}
	norm := make([]string, 0, len(c.Stores))

	for _, s := range c.Stores {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}

		if s != "ndjson" && s != "pg" {
			return Config{}, fmt.Errorf("invalid HEP_STORE %q (want ndjson and/or pg)", s)
		}

		seen[s] = true
		norm = append(norm, s)
	}

	if len(norm) == 0 {
		norm = []string{"ndjson"}
	}

	c.Stores = norm

	// DATABASE_URL is required only on the pg path.
	if c.HasStore("pg") && strings.TrimSpace(c.DatabaseURL) == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required when HEP_STORE includes pg")
	}

	// Default applies only when the var is absent. Present-but-empty
	// (HEP_EXCEPT_METHODS="") means "discard nothing".
	if _, set := os.LookupEnv(discardMethodsEnv); !set {
		c.DiscardMethods = []string{"OPTIONS"}
	}

	cleaned := c.DiscardMethods[:0]

	for _, m := range c.DiscardMethods {
		if m = strings.ToUpper(strings.TrimSpace(m)); m != "" {
			cleaned = append(cleaned, m)
		}
	}

	c.DiscardMethods = cleaned

	return c, nil
}

// DiscardSet returns the discard methods as a lookup set.
func (c Config) DiscardSet() map[string]struct{} {
	set := make(map[string]struct{}, len(c.DiscardMethods))

	for _, m := range c.DiscardMethods {
		set[m] = struct{}{}
	}

	return set
}
