// Package config reads clowk-hep3's runtime configuration from the
// environment via struct tags (caarlos0/env), loading an optional .env
// first (godotenv). clowk-hep3 is the WRITER: it receives HEP3, parses
// SIP, and writes to a shared Postgres. The reader (voodu-hep3) connects
// to the same DATABASE_URL — Postgres is created externally and the URL
// passed to both; clowk-hep3 never provisions it.
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

	// DatabaseURL is the shared Postgres connection string (required).
	// clowk-hep3 owns the schema and runs migrations on boot.
	DatabaseURL string `env:"DATABASE_URL,required"`

	// DBBulk is the insert batch size.
	DBBulk int `env:"HEP_DB_BULK" envDefault:"200"`
	// DBTimer is the max time a partial batch waits before flushing.
	DBTimer time.Duration `env:"HEP_DB_TIMER" envDefault:"4s"`
	// DBWorkers is the number of parallel decode workers.
	DBWorkers int `env:"HEP_DB_WORKERS" envDefault:"4"`

	// RetentionDays drops sip_messages older than this. Zero disables.
	RetentionDays int `env:"HEP_RETENTION_DAYS" envDefault:"30"`

	// CorrelationHeader is the SIP header used to stitch B2BUA leg pairs.
	CorrelationHeader string `env:"HEP_CID" envDefault:"X-CID"`
	// DiscardMethods are SIP request methods dropped before storage.
	DiscardMethods []string `env:"HEP_EXCEPT_METHODS" envSeparator:","`
}

const discardMethodsEnv = "HEP_EXCEPT_METHODS"

// Load resolves configuration: optional .env, then environment overlay.
func Load() (Config, error) {
	_ = godotenv.Load()

	var c Config

	if err := env.Parse(&c); err != nil {
		return Config{}, err
	}

	// `,required` only catches an unset var; a present-but-empty value
	// (DATABASE_URL="") slips through, so reject it explicitly.
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
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
