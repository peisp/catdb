package scanner

import (
	"context"
	"database/sql"
	"testing"

	"catdb/internal/dbdriver"

	_ "modernc.org/sqlite"
)

// TestSQLResultSet_ContextTeardown pins the invariant behind the QueryService
// streaming fix: a SQLResultSet is bound to the context it was constructed
// with, so if that context is cancelled while the stream is still open, the
// next Next() fails. RunQuery must therefore hand a kept-open cursor a context
// decoupled from the per-call ctx (which Wails cancels on return) — otherwise
// the first FetchMore hits "context canceled" and the handle is torn down.
func TestSQLResultSet_ContextTeardown(t *testing.T) {
	db := seedDB(t, 5)
	defer db.Close()

	// Positive: a live context drains every row across batch boundaries.
	rs := openRS(t, db, context.Background())
	got := drain(t, rs, 2) // batch smaller than row count -> multiple Next calls
	if got != 5 {
		t.Fatalf("live ctx: drained %d rows, want 5", got)
	}
	_ = rs.Close()

	// Negative: cancel the construction context mid-stream -> Next must error.
	ctx, cancel := context.WithCancel(context.Background())
	rs = openRS(t, db, ctx)
	if _, _, err := rs.Next(2); err != nil {
		t.Fatalf("first batch before cancel: %v", err)
	}
	cancel()
	if _, _, err := rs.Next(2); err == nil {
		t.Fatal("expected error reading after context cancel, got nil")
	}
	_ = rs.Close()
}

func seedDB(t *testing.T, n int) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE t(id INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := db.Exec("INSERT INTO t(id) VALUES(?)", i); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return db
}

func openRS(t *testing.T, db *sql.DB, ctx context.Context) *SQLResultSet {
	t.Helper()
	rows, err := db.QueryContext(ctx, "SELECT id FROM t ORDER BY id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rs, err := NewSQLResultSet(ctx, rows, sqliteDialect{})
	if err != nil {
		t.Fatalf("new resultset: %v", err)
	}
	return rs
}

func drain(t *testing.T, rs *SQLResultSet, batch int) int {
	t.Helper()
	total := 0
	for {
		rows, done, err := rs.Next(batch)
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		total += len(rows)
		if done {
			return total
		}
	}
}

// sqliteDialect is a no-op dialect sufficient for the scanner's column-meta
// mapping in this test.
type sqliteDialect struct{}

func (sqliteDialect) QuoteIdentifier(s string) string          { return s }
func (sqliteDialect) DefaultNamespaceSQL(string) string        { return "" }
func (sqliteDialect) ScriptRules() dbdriver.ScriptRules        { return dbdriver.ScriptRules{} }
func (sqliteDialect) NormalizeType(s string) string            { return s }
func (sqliteDialect) Paginate(q string, limit, off int) string { return q }
func (sqliteDialect) MapType(string) dbdriver.LogicalType      { return dbdriver.TypeUnknown }
func (sqliteDialect) GenerateCreateTable(dbdriver.TableSchema) (string, error) {
	return "", nil
}
func (sqliteDialect) GenerateAlterTable(db, schema, table string, cs dbdriver.ChangeSet) ([]string, error) {
	return nil, nil
}
