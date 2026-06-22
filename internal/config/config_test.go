package config

import (
	"testing"
	"time"
)

// DATABASE_URL is required; set a dummy so Load() reaches the rest.
func withDB(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/hep?sslmode=disable")
}

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	// No DATABASE_URL set in this test's environment.
	t.Setenv("DATABASE_URL", "")

	if _, err := Load(); err == nil {
		t.Error("Load() should fail when DATABASE_URL is unset/empty")
	}
}

func TestLoad_Defaults(t *testing.T) {
	withDB(t)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if c.HEPAddr != "0.0.0.0:9060" {
		t.Errorf("HEPAddr = %q, want 0.0.0.0:9060", c.HEPAddr)
	}

	if c.DBTimer != 4*time.Second {
		t.Errorf("DBTimer = %v, want 4s", c.DBTimer)
	}

	if c.RetentionDays != 30 {
		t.Errorf("RetentionDays = %d, want 30", c.RetentionDays)
	}

	if c.CorrelationHeader != "X-CID" {
		t.Errorf("CorrelationHeader = %q, want X-CID", c.CorrelationHeader)
	}

	if len(c.DiscardMethods) != 1 || c.DiscardMethods[0] != "OPTIONS" {
		t.Errorf("DiscardMethods = %v, want [OPTIONS]", c.DiscardMethods)
	}
}

func TestLoad_Overrides(t *testing.T) {
	withDB(t)
	t.Setenv("HEP_ADDR", "127.0.0.1:19060")
	t.Setenv("HEP_DB_TIMER", "500ms")
	t.Setenv("HEP_RETENTION_DAYS", "7")
	t.Setenv("HEP_EXCEPT_METHODS", "options, register ,NOTIFY")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if c.HEPAddr != "127.0.0.1:19060" {
		t.Errorf("HEPAddr = %q", c.HEPAddr)
	}

	if c.DBTimer != 500*time.Millisecond {
		t.Errorf("DBTimer = %v, want 500ms", c.DBTimer)
	}

	if c.RetentionDays != 7 {
		t.Errorf("RetentionDays = %d, want 7", c.RetentionDays)
	}

	want := []string{"OPTIONS", "REGISTER", "NOTIFY"}
	if len(c.DiscardMethods) != len(want) {
		t.Fatalf("DiscardMethods = %v, want %v", c.DiscardMethods, want)
	}

	for i, mth := range want {
		if c.DiscardMethods[i] != mth {
			t.Errorf("DiscardMethods[%d] = %q, want %q", i, c.DiscardMethods[i], mth)
		}
	}
}

// Explicit empty HEP_EXCEPT_METHODS means "discard nothing".
func TestLoad_EmptyDiscard(t *testing.T) {
	withDB(t)
	t.Setenv("HEP_EXCEPT_METHODS", "")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(c.DiscardMethods) != 0 {
		t.Errorf("DiscardMethods = %v, want empty", c.DiscardMethods)
	}

	if len(c.DiscardSet()) != 0 {
		t.Errorf("DiscardSet not empty: %v", c.DiscardSet())
	}
}
