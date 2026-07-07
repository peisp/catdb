// Contract test: SQLite is embedded, so unlike MySQL/Postgres this runs the
// shared dbdriver contract suite against a temp-file database with no Docker
// — it is part of the plain `task test` run.
package sqlitedrv

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"catdb/internal/dbdriver"
	"catdb/internal/dbdriver/contract"
)

func TestSQLiteContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg := dbdriver.ConnConfig{
		Database: filepath.Join(t.TempDir(), "contract.db"),
	}

	contract.Run(t, ctx, driver{}, cfg, contract.Fixtures{
		// SQLite has no sleep() — a bounded recursive CTE burns CPU long
		// enough for the 200ms-deadline cancel test to fire the interrupt.
		SleepSQL: "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c LIMIT 500000000) SELECT count(*) FROM c",
		CreateTableSQL: func(qualified string) string {
			return "CREATE TABLE " + qualified + ` (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name VARCHAR(64) NOT NULL,
				created_at TIMESTAMP NULL
			)`
		},
	})
}
