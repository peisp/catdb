package sqlitedrv

import (
	"context"
	"strings"
	"testing"

	"catdb/internal/dbdriver"
)

func TestBuildDSN(t *testing.T) {
	// File path with defaults.
	dsn, memory, err := buildDSN(dbdriver.ConnConfig{Database: "/tmp/app.db"})
	if err != nil || memory {
		t.Fatalf("buildDSN: %v memory=%v", err, memory)
	}
	for _, want := range []string{"file:/tmp/app.db?", "_pragma=busy_timeout(5000)", "_pragma=foreign_keys(1)"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("dsn missing %q: %s", want, dsn)
		}
	}

	// Read-only mode, custom busy timeout, FKs off.
	dsn, _, err = buildDSN(dbdriver.ConnConfig{
		Database: "/tmp/app.db",
		Params:   map[string]string{"mode": "ro", "busyTimeout": "100", "foreignKeys": "false"},
	})
	if err != nil {
		t.Fatalf("buildDSN ro: %v", err)
	}
	if !strings.Contains(dsn, "mode=ro") || !strings.Contains(dsn, "busy_timeout(100)") || strings.Contains(dsn, "foreign_keys") {
		t.Errorf("ro dsn wrong: %s", dsn)
	}

	// In-memory.
	dsn, memory, err = buildDSN(dbdriver.ConnConfig{Database: ":memory:"})
	if err != nil || !memory || !strings.Contains(dsn, "file::memory:?cache=shared") {
		t.Fatalf("memory dsn wrong: %s (memory=%v, err=%v)", dsn, memory, err)
	}

	// Missing path and unknown mode are errors.
	if _, _, err := buildDSN(dbdriver.ConnConfig{}); err == nil {
		t.Fatal("expected error for empty path")
	}
	if _, _, err := buildDSN(dbdriver.ConnConfig{Database: "x.db", Params: map[string]string{"mode": "wal"}}); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestOpenMemory(t *testing.T) {
	ctx := context.Background()
	conn, err := driver{}.Open(ctx, dbdriver.ConnConfig{Database: ":memory:"})
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	defer conn.Close()
	if _, err := conn.Querier().Exec(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// A second pooled operation must still see the same in-memory database.
	if _, err := conn.Querier().Exec(ctx, "INSERT INTO t (v) VALUES ('a')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	dbs, err := conn.Metadata().ListDatabases(ctx)
	if err != nil || len(dbs) == 0 || dbs[0] != "main" {
		t.Fatalf("ListDatabases = %v, %v", dbs, err)
	}
}
