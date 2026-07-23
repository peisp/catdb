package services

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"

	"catdb/internal/core/scanner"
	"catdb/internal/dbdriver"
)

// fakeDialect covers the two placeholder families without importing plugins.
type fakeDialect struct {
	numbered  bool // $1…$n (Postgres) vs ? (MySQL)
	backslash bool // ScriptRules.BackslashEscapes
}

func (fakeDialect) QuoteIdentifier(name string) string { return `"` + name + `"` }
func (fakeDialect) DefaultNamespaceSQL(string) string  { return "" }
func (d fakeDialect) ScriptRules() dbdriver.ScriptRules {
	return dbdriver.ScriptRules{BackslashEscapes: d.backslash}
}
func (d fakeDialect) Placeholder(i int) string {
	if d.numbered {
		return "$" + strconv.Itoa(i)
	}
	return "?"
}
func (fakeDialect) Paginate(baseSQL string, _, _ int) string { return baseSQL }
func (fakeDialect) MapType(string) dbdriver.LogicalType      { return dbdriver.TypeUnknown }
func (fakeDialect) NormalizeType(nativeType string) string   { return nativeType }
func (fakeDialect) GenerateCreateTable(dbdriver.TableSchema) (string, error) {
	return "", nil
}
func (fakeDialect) GenerateAlterTable(_, _, _ string, _ dbdriver.ChangeSet) ([]string, error) {
	return nil, nil
}
func (fakeDialect) TruncateTableSQL(qualified string) string { return "TRUNCATE TABLE " + qualified }
func (fakeDialect) ReplaceViewSQL(qualified, definition string) []string {
	return []string{"CREATE OR REPLACE VIEW " + qualified + " AS " + definition + ";"}
}

func TestBuildBatchInsert(t *testing.T) {
	rows := [][]any{{1, "a"}, {2, "b"}}

	sqlText, args := buildBatchInsert(fakeDialect{}, `"db"."t"`, []string{"id", "name"}, rows)
	want := `INSERT INTO "db"."t" ("id", "name") VALUES (?, ?), (?, ?)`
	if sqlText != want {
		t.Errorf("mysql-style SQL = %q, want %q", sqlText, want)
	}
	if len(args) != 4 || args[0] != 1 || args[3] != "b" {
		t.Errorf("args = %v", args)
	}

	sqlText, _ = buildBatchInsert(fakeDialect{numbered: true}, `"t"`, []string{"id", "name"}, rows)
	want = `INSERT INTO "t" ("id", "name") VALUES ($1, $2), ($3, $4)`
	if sqlText != want {
		t.Errorf("numbered SQL = %q, want %q", sqlText, want)
	}
}

func TestBindArgUnwrapsMarkers(t *testing.T) {
	raw := []byte{0x01, 0xff}
	bv := scanner.BytesValue{Type: "bytes", Base64: base64.StdEncoding.EncodeToString(raw), Length: 2}
	got, ok := bindArg(bv).([]byte)
	if !ok || len(got) != 2 || got[1] != 0xff {
		t.Errorf("bindArg(BytesValue) = %v", got)
	}
	if v := bindArg(scanner.BigIntString{Type: "bigint", Value: "9223372036854775807"}); v != "9223372036854775807" {
		t.Errorf("bindArg(BigIntString) = %v", v)
	}
	if v := bindArg("plain"); v != "plain" {
		t.Errorf("bindArg passthrough = %v", v)
	}
}

func TestSQLLiteral(t *testing.T) {
	my := dbdriver.ScriptRules{BackslashEscapes: true}
	pg := dbdriver.ScriptRules{DollarQuoting: true}

	if got := sqlLiteral(nil, my); got != "NULL" {
		t.Errorf("nil = %q", got)
	}
	if got := sqlLiteral(true, pg); got != "TRUE" {
		t.Errorf("bool = %q (must be TRUE, not 1 — Postgres boolean rejects integers)", got)
	}
	// MySQL interprets backslash escapes inside strings: a trailing backslash
	// must be doubled or it eats the closing quote (injection vector).
	if got := sqlLiteral(`a\`, my); got != `'a\\'` {
		t.Errorf("mysql backslash = %q, want 'a\\\\'", got)
	}
	if got := sqlLiteral(`a\`, pg); got != `'a\'` {
		t.Errorf("pg backslash = %q, want 'a\\' (standard-conforming strings)", got)
	}
	if got := sqlLiteral("it's", pg); got != "'it''s'" {
		t.Errorf("quote doubling = %q", got)
	}
	if got := sqlLiteral(scanner.BigIntString{Value: "42"}, my); got != "42" {
		t.Errorf("bigint marker = %q", got)
	}
	bv := scanner.BytesValue{Base64: base64.StdEncoding.EncodeToString([]byte{0xab, 0xcd})}
	if got := sqlLiteral(bv, my); got != "X'abcd'" {
		t.Errorf("mysql bytes = %q, want X'abcd'", got)
	}
	if got := sqlLiteral(bv, pg); got != `'\xabcd'` {
		t.Errorf("pg bytes = %q, want '\\xabcd'", got)
	}
}

type fakeQuerier struct {
	execs []string
	args  [][]any
}

func (f *fakeQuerier) Exec(_ context.Context, sqlText string, args ...any) (dbdriver.ExecResult, error) {
	f.execs = append(f.execs, sqlText)
	f.args = append(f.args, args)
	return dbdriver.ExecResult{}, nil
}
func (f *fakeQuerier) Query(context.Context, string, ...any) (dbdriver.ResultSet, error) {
	return nil, nil
}
func (f *fakeQuerier) Explain(context.Context, string) (dbdriver.ResultSet, error) {
	return nil, nil
}

func TestTransferBatchChunking(t *testing.T) {
	// 3 columns → maxInsertParams/3 = 20000 rows per statement; one row over
	// the boundary must produce a second INSERT, with numbering restarted.
	perStmt := maxInsertParams / 3
	rows := make([][]any, perStmt+1)
	for i := range rows {
		rows[i] = []any{i, "n", true}
	}
	q := &fakeQuerier{}
	s := &TransferService{}
	if err := s.transferBatch(context.Background(), q, `"t"`, []string{"a", "b", "c"}, rows, fakeDialect{numbered: true}); err != nil {
		t.Fatal(err)
	}
	if len(q.execs) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(q.execs))
	}
	if !strings.HasSuffix(q.execs[1], "($1, $2, $3)") {
		t.Errorf("placeholder numbering must restart per statement, tail statement: %q", q.execs[1][:80])
	}
	if len(q.args[0]) != perStmt*3 || len(q.args[1]) != 3 {
		t.Errorf("arg counts = %d, %d", len(q.args[0]), len(q.args[1]))
	}
}
