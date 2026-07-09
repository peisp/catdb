//go:build integration

// Integration test: runs the shared dbdriver contract suite against a real
// DM instance. There is no official DM docker registry image (Dameng ships
// docker tarballs; community arm64 builds exist, e.g. qinchz/dm8-arm64), so
// unlike mysql/postgres this test does not spin a container itself — point
// it at an instance via environment variables:
//
//	CATDB_DM_HOST=127.0.0.1 \
//	CATDB_DM_PORT=5236 \
//	CATDB_DM_USER=SYSDBA \
//	CATDB_DM_PASSWORD=SYSDBA001 \
//	go test -tags=integration ./plugins/dmdrv/...
//
// The test is skipped when CATDB_DM_HOST is unset.
//
// IMPORTANT: the contract harness addresses fixture columns both quoted
// ("id") and unquoted (SELECT id …), so the instance must be initialized
// with CASE_SENSITIVE=0 (e.g. docker … -e CASE_SENSITIVE=0). On a default
// CASE_SENSITIVE=1 instance unquoted identifiers are uppercased and the
// suite cannot pass — the driver itself works on both.
package dmdrv

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"catdb/internal/dbdriver"
	"catdb/internal/dbdriver/contract"
)

func TestDMContract(t *testing.T) {
	host := os.Getenv("CATDB_DM_HOST")
	if host == "" {
		t.Skip("CATDB_DM_HOST not set — skipping DM contract suite")
	}
	port := 5236
	if v := os.Getenv("CATDB_DM_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}
	user := os.Getenv("CATDB_DM_USER")
	if user == "" {
		user = "SYSDBA"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg := dbdriver.ConnConfig{
		Host:     host,
		Port:     port,
		User:     user,
		Password: os.Getenv("CATDB_DM_PASSWORD"),
		Database: os.Getenv("CATDB_DM_SCHEMA"), // optional default schema
	}

	contract.Run(t, ctx, driver{}, cfg, contract.Fixtures{
		// No SLEEP() scalar function in DM; a cartesian self-join over the
		// dictionary blocks server-side long enough for the cancel test.
		SleepSQL: "SELECT COUNT(*) FROM SYSOBJECTS A, SYSOBJECTS B, SYSOBJECTS C",
		CreateTableSQL: func(qualified string) string {
			return fmt.Sprintf(`CREATE TABLE %s (
				id INT IDENTITY(1,1) PRIMARY KEY,
				name VARCHAR(64) NOT NULL,
				created_at TIMESTAMP NULL
			)`, qualified)
		},
	})
}
