package config

import (
	"strings"
	"testing"
)

func eqStores(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// Default: no HEP_STORE → ndjson, and DATABASE_URL is NOT required.
func TestConfig_DefaultStoreNDJSON(t *testing.T) {
	t.Setenv("HEP_STORE", "")
	t.Setenv("DATABASE_URL", "")

	c, err := Load()
	if err != nil {
		t.Fatalf("default config should load without DATABASE_URL: %v", err)
	}

	if !eqStores(c.Stores, []string{"ndjson"}) {
		t.Errorf("default stores = %v, want [ndjson]", c.Stores)
	}
}

// pg selected but no DATABASE_URL → hard error (the pg path needs it).
func TestConfig_PGRequiresDatabaseURL(t *testing.T) {
	t.Setenv("HEP_STORE", "pg")
	t.Setenv("DATABASE_URL", "")

	if _, err := Load(); err == nil {
		t.Fatal("HEP_STORE=pg without DATABASE_URL should error")
	}
}

func TestConfig_PGWithURLOK(t *testing.T) {
	t.Setenv("HEP_STORE", "pg")
	t.Setenv("DATABASE_URL", "postgres://u:p@h:5432/hep")

	c, err := Load()
	if err != nil {
		t.Fatalf("pg + url should load: %v", err)
	}

	if !eqStores(c.Stores, []string{"pg"}) {
		t.Errorf("stores = %v, want [pg]", c.Stores)
	}
}

// Dual-write keeps order and requires DATABASE_URL (pg is present).
func TestConfig_DualWrite(t *testing.T) {
	t.Setenv("HEP_STORE", "pg,ndjson")
	t.Setenv("DATABASE_URL", "postgres://u:p@h:5432/hep")

	c, err := Load()
	if err != nil {
		t.Fatalf("dual-write should load: %v", err)
	}

	if !eqStores(c.Stores, []string{"pg", "ndjson"}) {
		t.Errorf("stores = %v, want [pg ndjson]", c.Stores)
	}
}

func TestConfig_InvalidStore(t *testing.T) {
	t.Setenv("HEP_STORE", "foo")

	if _, err := Load(); err == nil {
		t.Fatal("HEP_STORE=foo should error")
	}
}

// Whitespace, case, and duplicates are normalized away.
func TestConfig_StoreNormalization(t *testing.T) {
	t.Setenv("HEP_STORE", " NDJSON , ndjson ,PG ")
	t.Setenv("DATABASE_URL", "postgres://u:p@h:5432/hep")

	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !eqStores(c.Stores, []string{"ndjson", "pg"}) {
		t.Errorf("normalized stores = %v, want [ndjson pg]", c.Stores)
	}

	if !c.HasStore("pg") || !c.HasStore("ndjson") {
		t.Errorf("HasStore broken: %v", c.Stores)
	}
}

// HasStore is false for an unselected backend.
func TestConfig_HasStoreNegative(t *testing.T) {
	t.Setenv("HEP_STORE", "ndjson")
	t.Setenv("DATABASE_URL", "")

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if c.HasStore("pg") {
		t.Error("HasStore(pg) should be false when only ndjson selected")
	}

	if strings.Join(c.Stores, ",") != "ndjson" {
		t.Errorf("stores = %v", c.Stores)
	}
}
