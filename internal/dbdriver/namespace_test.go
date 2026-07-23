package dbdriver

import (
	"context"
	"testing"
)

// --- fakes -------------------------------------------------------------------

type nsFakeQuerier struct{ execs *[]string }

func (q nsFakeQuerier) Exec(ctx context.Context, sql string, args ...any) (ExecResult, error) {
	*q.execs = append(*q.execs, sql)
	return ExecResult{}, nil
}
func (q nsFakeQuerier) Query(ctx context.Context, sql string, args ...any) (ResultSet, error) {
	*q.execs = append(*q.execs, sql)
	return nil, nil
}
func (q nsFakeQuerier) Explain(ctx context.Context, sql string) (ResultSet, error) {
	return nil, nil
}

type nsFakeTx struct {
	nsFakeQuerier
	committed, rolled *bool
}

func (t nsFakeTx) Commit() error   { *t.committed = true; return nil }
func (t nsFakeTx) Rollback() error { *t.rolled = true; return nil }

type nsFakeConn struct {
	Connection
	execs             []string
	committed, rolled bool
}

func (c *nsFakeConn) Querier() Querier { return nsFakeQuerier{execs: &c.execs} }
func (c *nsFakeConn) Begin(ctx context.Context, opts *TxOptions) (Tx, error) {
	return nsFakeTx{nsFakeQuerier{&c.execs}, &c.committed, &c.rolled}, nil
}

type nsFakeDialect struct {
	Dialect
	stmt string // DefaultNamespaceSQL result prefix; "" = unsupported
}

func (d nsFakeDialect) DefaultNamespaceSQL(name string) string {
	if d.stmt == "" {
		return ""
	}
	return d.stmt + " " + name
}

// --- tests -------------------------------------------------------------------

func TestNamespaceName(t *testing.T) {
	if got := NamespaceName(Capabilities{Schemas: false}, "shop", "public"); got != "shop" {
		t.Fatalf("no-schema driver: %q", got)
	}
	if got := NamespaceName(Capabilities{Schemas: true}, "shop", "public"); got != "public" {
		t.Fatalf("schema driver: %q", got)
	}
}

// MySQL-style: no router, USE-style statement — a pinned tx applies it and
// release commits.
func TestNamespacedQuerierPinsSessionDefault(t *testing.T) {
	conn := &nsFakeConn{}
	q, release, err := NamespacedQuerier(context.Background(), conn, nsFakeDialect{stmt: "USE"}, Capabilities{}, "shop", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Exec(context.Background(), "INSERT 1"); err != nil {
		t.Fatal(err)
	}
	release()
	if len(conn.execs) != 2 || conn.execs[0] != "USE shop" || conn.execs[1] != "INSERT 1" {
		t.Fatalf("execs = %v", conn.execs)
	}
	if !conn.committed || conn.rolled {
		t.Fatalf("committed=%v rolled=%v", conn.committed, conn.rolled)
	}
}

// No session-default statement (dialect can't) — plain routed querier, no tx.
func TestNamespacedQuerierPlainFallback(t *testing.T) {
	conn := &nsFakeConn{}
	q, release, err := NamespacedQuerier(context.Background(), conn, nsFakeDialect{}, Capabilities{}, "shop", "")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := q.Exec(context.Background(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if len(conn.execs) != 1 || conn.execs[0] != "SELECT 1" {
		t.Fatalf("execs = %v", conn.execs)
	}
	if conn.committed || conn.rolled {
		t.Fatal("no tx must be opened without a namespace statement")
	}
}

// Schema driver with empty schema: nothing to pin — plain querier even though
// the dialect has a namespace statement.
func TestNamespacedQuerierSchemaDriverNoSchema(t *testing.T) {
	conn := &nsFakeConn{}
	_, release, err := NamespacedQuerier(context.Background(), conn, nsFakeDialect{stmt: "SET search_path TO"}, Capabilities{Schemas: true}, "shop", "")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if len(conn.execs) != 0 || conn.committed {
		t.Fatalf("execs = %v committed=%v", conn.execs, conn.committed)
	}
}
