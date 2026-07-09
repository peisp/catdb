// Package contract is the shared contract-test harness every driver plugin
// must pass. A new driver is "done" when contract.Run completes with no
// errors against a real instance.
//
// The harness itself is dialect-agnostic: identifiers are quoted through the
// driver's Dialect, tables are addressed via dbdriver.QualifyTable, and the
// few statements that cannot be written portably (CREATE TABLE with an
// auto-increment key, a server-side sleep) are injected per driver through
// Fixtures.
//
// Tests in plugins/<driver>/contract_test.go spin a real DB (testcontainers
// or local) and call:
//
//	contract.Run(t, ctx, driver, cfg, contract.Fixtures{...})
package contract

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"catdb/internal/core/schemadiff"
	"catdb/internal/dbdriver"
)

// Fixtures carries the per-driver SQL the harness cannot express portably.
type Fixtures struct {
	// SleepSQL is a SELECT that blocks server-side for ≥2 seconds, used to
	// exercise ctx cancellation (MySQL "SELECT SLEEP(2)", PG "SELECT pg_sleep(2)").
	SleepSQL string

	// CreateTableSQL returns DDL creating the standard contract table under
	// the given qualified (already-quoted) name. Required shape, in order:
	//   id         integer, PRIMARY KEY, auto-increment
	//   name       varchar(64) NOT NULL
	//   created_at timestamp-ish, NULLable
	CreateTableSQL func(qualifiedName string) string
}

// Run executes the full suite against an open Driver + ConnConfig pair. The
// connection is opened/closed internally; the caller does not need to.
func Run(t *testing.T, ctx context.Context, d dbdriver.Driver, cfg dbdriver.ConnConfig, fx Fixtures) {
	t.Helper()
	if fx.SleepSQL == "" || fx.CreateTableSQL == nil {
		t.Fatal("contract: Fixtures.SleepSQL and Fixtures.CreateTableSQL are required")
	}
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Driver.Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	t.Run("Ping", func(t *testing.T) { testPing(t, ctx, conn) })
	t.Run("ServerInfo", func(t *testing.T) { testServerInfo(t, ctx, conn) })
	t.Run("Query/Scalar", func(t *testing.T) { testQueryScalar(t, ctx, conn) })
	t.Run("Query/Cancel", func(t *testing.T) { testQueryCancel(t, ctx, conn, fx) })
	t.Run("Metadata", func(t *testing.T) { testMetadata(t, ctx, d, conn, fx) })
	t.Run("DatabaseEditor", func(t *testing.T) { testDatabaseEditor(t, ctx, d, conn) })
	t.Run("Edit", func(t *testing.T) { testEdit(t, ctx, d, conn, fx) })
	t.Run("DDL", func(t *testing.T) { testDDL(t, ctx, d, conn) })
	t.Run("Dialect", func(t *testing.T) { testDialect(t, ctx, d, conn, fx) })
	t.Run("Views", func(t *testing.T) { testViewDefinitions(t, ctx, d, conn, fx) })
	t.Run("Tx", func(t *testing.T) { testTxLifecycle(t, ctx, conn) })
	t.Run("UIDialect", func(t *testing.T) { TestUIDialect(t, d) })
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

func testQueryCancel(t *testing.T, ctx context.Context, c dbdriver.Connection, fx Fixtures) {
	// Use a context with a very tight deadline against a sleep so the cancel
	// path is exercised reliably without relying on SIGTERM-style hangs.
	tctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	_, err := c.Querier().Query(tctx, fx.SleepSQL)
	if err == nil {
		t.Fatalf("expected ctx-related error on %q", fx.SleepSQL)
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(tctx.Err(), context.DeadlineExceeded) {
		// The driver may wrap the error; accept any cancel-shaped failure.
		t.Logf("note: driver returned %v (not DeadlineExceeded directly)", err)
	}
}

// testDatabaseEditor asserts the driver's Capabilities.DatabaseEditor flag
// matches whether its Metadata implements the optional DatabaseEditor
// extension. A mismatch means the UI would either offer a 「新建数据库」 form
// that fails at call time (capability true, interface missing) or hide one
// that would work (capability false, interface present).
func testDatabaseEditor(t *testing.T, ctx context.Context, d dbdriver.Driver, c dbdriver.Connection) {
	m := c.Metadata()
	if m == nil {
		t.Fatal("Metadata() returned nil")
	}
	ed, ok := m.(dbdriver.DatabaseEditor)
	if want := d.Capabilities().DatabaseEditor; ok != want {
		t.Fatalf("Capabilities.DatabaseEditor = %v but Metadata implements DatabaseEditor = %v — they must agree", want, ok)
	}
	// When advertised, the option-fields descriptor must actually load.
	if ok {
		if _, err := ed.DatabaseOptionFields(ctx); err != nil {
			t.Fatalf("DatabaseOptionFields: %v", err)
		}
	}
}

// pickTarget selects the database (and schema, when the driver has that
// level) contract tables are created in.
func pickTarget(t *testing.T, ctx context.Context, d dbdriver.Driver, c dbdriver.Connection) (db, schema string) {
	t.Helper()
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
	db = pickFirst(dbs, "test", "catdb")

	if d.Capabilities().Schemas {
		schemas, err := m.ListSchemas(ctx, db)
		if err != nil {
			t.Fatalf("ListSchemas: %v", err)
		}
		if len(schemas) == 0 {
			t.Fatal("Capabilities.Schemas is true but ListSchemas returned nothing")
		}
		schema = pickFirst(schemas, "public")
	}
	return db, schema
}

// makeContractTable creates the standard fixture table and registers cleanup.
// Returns the (db, schema, qualified-name) triple the caller should use.
func makeContractTable(t *testing.T, ctx context.Context, d dbdriver.Driver, c dbdriver.Connection, fx Fixtures, tn string) (db, schema, qualified string) {
	t.Helper()
	db, schema = pickTarget(t, ctx, d, c)
	qualified = dbdriver.QualifyTable(d.Dialect(), db, schema, tn)
	mustExec(t, ctx, c, "DROP TABLE IF EXISTS "+qualified)
	mustExec(t, ctx, c, fx.CreateTableSQL(qualified))
	t.Cleanup(func() {
		_, _ = c.Querier().Exec(ctx, "DROP TABLE IF EXISTS "+qualified)
	})
	return db, schema, qualified
}

func testMetadata(t *testing.T, ctx context.Context, d dbdriver.Driver, c dbdriver.Connection, fx Fixtures) {
	m := c.Metadata()
	tn := "ct_contract"
	db, schema, _ := makeContractTable(t, ctx, d, c, fx, tn)

	tables, err := m.ListTables(ctx, db, schema)
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if !containsTable(tables, tn) {
		t.Fatalf("ListTables missing %s; saw %d tables", tn, len(tables))
	}

	cols, err := m.ListColumns(ctx, db, schema, tn)
	if err != nil {
		t.Fatalf("ListColumns: %v", err)
	}
	if len(cols) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(cols))
	}
	if !cols[0].IsPrimaryKey {
		t.Fatalf("expected id to be PK; got %+v", cols[0])
	}

	ix, err := m.ListIndexes(ctx, db, schema, tn)
	if err != nil {
		t.Fatalf("ListIndexes: %v", err)
	}
	if len(ix) == 0 || !hasPrimary(ix) {
		t.Fatalf("ListIndexes missing PRIMARY: %+v", ix)
	}

	ddl, err := m.GetCreateTable(ctx, db, schema, tn)
	if err != nil {
		t.Fatalf("GetCreateTable: %v", err)
	}
	if len(ddl) == 0 {
		t.Fatal("GetCreateTable returned empty DDL")
	}

	// Optional BulkMetadata extension: whole-schema reads must agree with the
	// per-table reads for the fixture table. nil vs empty slice is not a
	// meaningful difference (a table absent from a bulk map = no entries).
	if bm, ok := m.(dbdriver.BulkMetadata); ok {
		allCols, err := bm.ListAllColumns(ctx, db, schema)
		if err != nil {
			t.Fatalf("ListAllColumns: %v", err)
		}
		if !slicesAgree(allCols[tn], cols) {
			t.Fatalf("ListAllColumns[%s] disagrees with ListColumns:\nbulk %+v\nper-table %+v", tn, allCols[tn], cols)
		}
		allIx, err := bm.ListAllIndexes(ctx, db, schema)
		if err != nil {
			t.Fatalf("ListAllIndexes: %v", err)
		}
		if !slicesAgree(allIx[tn], ix) {
			t.Fatalf("ListAllIndexes[%s] disagrees with ListIndexes:\nbulk %+v\nper-table %+v", tn, allIx[tn], ix)
		}
		fks, err := m.ListForeignKeys(ctx, db, schema, tn)
		if err != nil {
			t.Fatalf("ListForeignKeys: %v", err)
		}
		allFK, err := bm.ListAllForeignKeys(ctx, db, schema)
		if err != nil {
			t.Fatalf("ListAllForeignKeys: %v", err)
		}
		if !slicesAgree(allFK[tn], fks) {
			t.Fatalf("ListAllForeignKeys[%s] disagrees with ListForeignKeys:\nbulk %+v\nper-table %+v", tn, allFK[tn], fks)
		}
	}
}

// slicesAgree is DeepEqual that treats nil and empty slices as equal.
func slicesAgree[T any](a, b []T) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func testEdit(t *testing.T, ctx context.Context, d dbdriver.Driver, c dbdriver.Connection, fx Fixtures) {
	ed := c.Editor()
	q := c.Querier()
	if ed == nil || q == nil {
		t.Fatal("Connection adapters missing")
	}
	tn := "ct_edit"
	db, schema, qualified := makeContractTable(t, ctx, d, c, fx, tn)

	pk, err := ed.PrimaryKeys(ctx, db, schema, tn)
	if err != nil {
		t.Fatalf("PrimaryKeys: %v", err)
	}
	if len(pk) != 1 || pk[0] != "id" {
		t.Fatalf("PrimaryKeys: expected [id], got %v", pk)
	}

	// Insert
	insSQL, insArgs, err := ed.BuildInsert(db, schema, tn, map[string]any{"name": "Alice"})
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

	// Fetch the generated id back with a literal predicate — LastInsertID is
	// not portable across databases, placeholder syntax isn't either.
	id := fetchScalar(t, ctx, c, "SELECT id FROM "+qualified+" WHERE name = 'Alice'")

	// Update
	upSQL, upArgs, err := ed.BuildUpdate(db, schema, tn, map[string]any{"id": id}, map[string]any{"name": "Alicia"})
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
	delSQL, delArgs, err := ed.BuildDelete(db, schema, tn, map[string]any{"id": id})
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

// testDDL exercises the Dialect DDL generators end-to-end: GenerateCreateTable
// executes cleanly and round-trips through metadata; a schemadiff ChangeSet
// rendered by GenerateAlterTable executes and converges (re-diff is empty).
func testDDL(t *testing.T, ctx context.Context, d dbdriver.Driver, c dbdriver.Connection) {
	m := c.Metadata()
	dia := d.Dialect()
	tn := "ct_ddl"
	db, schema := pickTarget(t, ctx, d, c)
	qualified := dbdriver.QualifyTable(dia, db, schema, tn)

	target := dbdriver.TableSchema{
		Name:   tn,
		Schema: firstNonEmpty(schema, db),
		Columns: []dbdriver.ColumnMeta{
			{Name: "id", NativeType: "INT", Nullable: false, IsPrimaryKey: true},
			{Name: "name", NativeType: "VARCHAR(64)", Nullable: false},
			{Name: "note", NativeType: "VARCHAR(255)", Nullable: true},
		},
	}
	createSQL, err := dia.GenerateCreateTable(target)
	if err != nil {
		t.Fatalf("GenerateCreateTable: %v", err)
	}
	mustExec(t, ctx, c, "DROP TABLE IF EXISTS "+qualified)
	mustExec(t, ctx, c, trimTrailingSemicolon(createSQL))
	t.Cleanup(func() {
		_, _ = c.Querier().Exec(ctx, "DROP TABLE IF EXISTS "+qualified)
	})

	// Round-trip: the created table must read back with the same shape.
	current := readTableSchema(t, ctx, m, db, schema, tn)
	if len(current.Columns) != 3 || !current.Columns[0].IsPrimaryKey {
		t.Fatalf("created table read back wrong: %+v", current.Columns)
	}
	if cs := schemadiff.Diff(current, schemadiff.FromTableSchema(current, current), schemadiff.Options{NormalizeType: dia.NormalizeType}); !cs.Empty() {
		t.Fatalf("self-diff of created table must be empty, got %+v", cs)
	}

	// Mutate: widen name, add a column, add an index — then reconcile.
	desiredSchema := current
	desiredSchema.Columns = append([]dbdriver.ColumnMeta{}, current.Columns...)
	desiredSchema.Columns[1].NativeType = "VARCHAR(128)"
	desiredSchema.Columns = append(desiredSchema.Columns, dbdriver.ColumnMeta{Name: "age", NativeType: "INT", Nullable: true})
	desiredSchema.Indexes = append(append([]dbdriver.IndexInfo{}, current.Indexes...),
		dbdriver.IndexInfo{Name: "ix_note", Columns: []dbdriver.IndexColumn{{Name: "note"}}, Type: "BTREE"})

	cs := schemadiff.Diff(current, schemadiff.FromTableSchema(desiredSchema, current), schemadiff.Options{NormalizeType: dia.NormalizeType})
	if cs.Empty() {
		t.Fatal("expected a non-empty ChangeSet")
	}
	stmts, err := dia.GenerateAlterTable(db, schema, tn, cs)
	if err != nil {
		t.Fatalf("GenerateAlterTable: %v", err)
	}
	for _, s := range stmts {
		mustExec(t, ctx, c, trimTrailingSemicolon(s))
	}

	// Convergence: after applying, re-diff must be empty.
	after := readTableSchema(t, ctx, m, db, schema, tn)
	if cs := schemadiff.Diff(after, schemadiff.FromTableSchema(desiredSchema, after), schemadiff.Options{NormalizeType: dia.NormalizeType}); !cs.Empty() {
		t.Fatalf("re-diff after apply must be empty, got %+v", cs)
	}
}

// readTableSchema assembles a TableSchema from the driver's metadata reads.
func readTableSchema(t *testing.T, ctx context.Context, m dbdriver.Metadata, db, schema, tn string) dbdriver.TableSchema {
	t.Helper()
	cols, err := m.ListColumns(ctx, db, schema, tn)
	if err != nil {
		t.Fatalf("ListColumns: %v", err)
	}
	ix, err := m.ListIndexes(ctx, db, schema, tn)
	if err != nil {
		t.Fatalf("ListIndexes: %v", err)
	}
	fks, err := m.ListForeignKeys(ctx, db, schema, tn)
	if err != nil {
		t.Fatalf("ListForeignKeys: %v", err)
	}
	return dbdriver.TableSchema{Name: tn, Schema: schema, Columns: cols, Indexes: ix, ForeignKeys: fks}
}

func trimTrailingSemicolon(s string) string {
	return strings.TrimSuffix(strings.TrimSpace(s), ";")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// --- helpers ---

func mustExec(t *testing.T, ctx context.Context, c dbdriver.Connection, sql string) {
	t.Helper()
	if _, err := c.Querier().Exec(ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func fetchScalar(t *testing.T, ctx context.Context, c dbdriver.Connection, sql string) any {
	t.Helper()
	rs, err := c.Querier().Query(ctx, sql)
	if err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	defer rs.Close()
	rows, _, err := rs.Next(1)
	if err != nil {
		t.Fatalf("next %q: %v", sql, err)
	}
	if len(rows) != 1 || len(rows[0]) == 0 {
		t.Fatalf("expected 1 scalar row from %q, got %d rows", sql, len(rows))
	}
	return rows[0][0]
}

// pickFirst returns the first preferred name present in names, else names[0].
func pickFirst(names []string, preferred ...string) string {
	for _, want := range preferred {
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
