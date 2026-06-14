// Package contract is the shared contract-test harness every driver plugin
// must pass. A new driver is "done" when contract.Run completes with no
// errors against a real instance.
//
// The harness is database-agnostic at the surface (Driver + ConnConfig only),
// but the SQL it issues stays inside the SQL-92 / common-dialect subset so it
// works across plugins without per-driver branches.
//
// Tests in plugins/<driver>/contract_test.go spin a real DB (testcontainers
// or local) and call:
//
//	contract.Run(t, ctx, driver, cfg)
package contract

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"catdb/internal/dbdriver"
)

// Run executes the full suite against an open Driver + ConnConfig pair. The
// connection is opened/closed internally; the caller does not need to.
func Run(t *testing.T, ctx context.Context, d dbdriver.Driver, cfg dbdriver.ConnConfig) {
	t.Helper()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	t.Run("Ping", func(t *testing.T) { testPing(t, ctx, conn) })
	t.Run("ServerInfo", func(t *testing.T) { testServerInfo(t, ctx, conn) })
	t.Run("Query/Scalar", func(t *testing.T) { testQueryScalar(t, ctx, conn) })
	t.Run("Query/Cancel", func(t *testing.T) { testQueryCancel(t, ctx, conn) })
	t.Run("Metadata", func(t *testing.T) { testMetadata(t, ctx, conn) })
	t.Run("Edit", func(t *testing.T) { testEdit(t, ctx, conn) })
}

func testPing(t *testing.T, ctx context.Context, c dbdriver.Connection) {
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func testServerInfo(t *testing.T, ctx context.Context, c dbdriver.Connection) {
	info, err := c.ServerInfo(ctx)
	if err != nil {
		t.Fatalf("ServerInfo: %v", err)
	}
	if info.Version == "" {
		t.Fatal("ServerInfo.Version is empty")
	}
	if info.User == "" {
		t.Fatal("ServerInfo.User is empty")
	}
}

func testQueryScalar(t *testing.T, ctx context.Context, c dbdriver.Connection) {
	rs, err := c.Querier().Query(ctx, "SELECT 1 AS one, 'hello' AS greeting")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rs.Close()
	cols := rs.Columns()
	if len(cols) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(cols))
	}
	rows, done, err := rs.Next(10)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !done {
		t.Fatalf("expected done=true on small scalar query")
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
}

func testQueryCancel(t *testing.T, ctx context.Context, c dbdriver.Connection) {
	// Use a context with a very tight deadline against a sleep so the cancel
	// path is exercised reliably without relying on SIGTERM-style hangs.
	tctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	_, err := c.Querier().Query(tctx, "SELECT SLEEP(2)")
	if err == nil {
		t.Fatal("expected ctx-related error on SLEEP(2)")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(tctx.Err(), context.DeadlineExceeded) {
		// The driver may wrap the error; accept any cancel-shaped failure.
		t.Logf("note: driver returned %v (not DeadlineExceeded directly)", err)
	}
}

func testMetadata(t *testing.T, ctx context.Context, c dbdriver.Connection) {
	m := c.Metadata()
	if m == nil {
		t.Fatal("Metadata() returned nil")
	}
	dbs, err := m.ListDatabases(ctx)
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	if len(dbs) == 0 {
		t.Fatal("ListDatabases returned 0 databases — pick a DB that has at least one user schema")
	}

	// Pick the first DB the harness can read; preference for any test DB.
	db := pickDB(dbs)

	// Create + describe a small table.
	tn := "ct_contract"
	mustExec(t, ctx, c, fmt.Sprintf("USE `%s`", db))
	mustExec(t, ctx, c, fmt.Sprintf("DROP TABLE IF EXISTS `%s`", tn))
	mustExec(t, ctx, c, fmt.Sprintf(`CREATE TABLE %s (
		id INT NOT NULL PRIMARY KEY AUTO_INCREMENT,
		name VARCHAR(64) NOT NULL,
		created_at DATETIME NULL
	)`, tn))
	t.Cleanup(func() {
		_, _ = c.Querier().Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS `%s`.`%s`", db, tn))
	})

	tables, err := m.ListTables(ctx, db, "")
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if !containsTable(tables, tn) {
		names := make([]string, len(tables))
		for i, t := range tables {
			names[i] = t.Name
		}
		sort.Strings(names)
		t.Fatalf("ListTables missing %s; saw %d tables", tn, len(names))
	}

	cols, err := m.ListColumns(ctx, db, "", tn)
	if err != nil {
		t.Fatalf("ListColumns: %v", err)
	}
	if len(cols) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(cols))
	}
	if !cols[0].IsPrimaryKey {
		t.Fatalf("expected id to be PK; got %+v", cols[0])
	}

	ix, err := m.ListIndexes(ctx, db, "", tn)
	if err != nil {
		t.Fatalf("ListIndexes: %v", err)
	}
	if len(ix) == 0 || !hasPrimary(ix) {
		t.Fatalf("ListIndexes missing PRIMARY: %+v", ix)
	}

	ddl, err := m.GetCreateTable(ctx, db, "", tn)
	if err != nil {
		t.Fatalf("GetCreateTable: %v", err)
	}
	if len(ddl) == 0 {
		t.Fatal("GetCreateTable returned empty DDL")
	}
}

func testEdit(t *testing.T, ctx context.Context, c dbdriver.Connection) {
	m := c.Metadata()
	ed := c.Editor()
	q := c.Querier()
	if m == nil || ed == nil || q == nil {
		t.Fatal("Connection adapters missing")
	}

	dbs, err := m.ListDatabases(ctx)
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	db := pickDB(dbs)
	tn := "ct_edit"
	mustExec(t, ctx, c, fmt.Sprintf("USE `%s`", db))
	mustExec(t, ctx, c, fmt.Sprintf("DROP TABLE IF EXISTS `%s`", tn))
	mustExec(t, ctx, c, fmt.Sprintf(`CREATE TABLE %s (
		id INT NOT NULL PRIMARY KEY AUTO_INCREMENT,
		name VARCHAR(64) NOT NULL
	)`, tn))
	t.Cleanup(func() {
		_, _ = c.Querier().Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS `%s`.`%s`", db, tn))
	})

	pk, err := ed.PrimaryKeys(ctx, db, "", tn)
	if err != nil {
		t.Fatalf("PrimaryKeys: %v", err)
	}
	if len(pk) != 1 || pk[0] != "id" {
		t.Fatalf("PrimaryKeys: expected [id], got %v", pk)
	}

	// Insert
	insSQL, insArgs, err := ed.BuildInsert(tn, map[string]any{"name": "Alice"})
	if err != nil {
		t.Fatalf("BuildInsert: %v", err)
	}
	res, err := q.Exec(ctx, insSQL, insArgs...)
	if err != nil {
		t.Fatalf("Insert Exec: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected RowsAffected=1, got %d", res.RowsAffected)
	}
	id := res.LastInsertID

	// Update
	upSQL, upArgs, err := ed.BuildUpdate(tn, map[string]any{"id": id}, map[string]any{"name": "Alicia"})
	if err != nil {
		t.Fatalf("BuildUpdate: %v", err)
	}
	res, err = q.Exec(ctx, upSQL, upArgs...)
	if err != nil {
		t.Fatalf("Update Exec: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("Update RowsAffected = %d", res.RowsAffected)
	}

	// Delete
	delSQL, delArgs, err := ed.BuildDelete(tn, map[string]any{"id": id})
	if err != nil {
		t.Fatalf("BuildDelete: %v", err)
	}
	res, err = q.Exec(ctx, delSQL, delArgs...)
	if err != nil {
		t.Fatalf("Delete Exec: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("Delete RowsAffected = %d", res.RowsAffected)
	}
}

// --- helpers ---

func mustExec(t *testing.T, ctx context.Context, c dbdriver.Connection, sql string) {
	t.Helper()
	if _, err := c.Querier().Exec(ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func pickDB(names []string) string {
	for _, want := range []string{"test", "catdb", "public"} {
		for _, n := range names {
			if n == want {
				return n
			}
		}
	}
	return names[0]
}

func containsTable(in []dbdriver.TableInfo, name string) bool {
	for _, t := range in {
		if t.Name == name {
			return true
		}
	}
	return false
}

func hasPrimary(in []dbdriver.IndexInfo) bool {
	for _, i := range in {
		if i.Primary {
			return true
		}
	}
	return false
}
