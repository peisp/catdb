package contract

import (
	"context"
	"errors"
	"strings"
	"testing"

	"catdb/internal/dbdriver"
)

// The tests in this file guard the abstraction points that used to be
// hard-coded MySQL behavior in the generic layers (services/core). A new
// driver that passes them is safe to run through query_service (default
// namespace switching), structure sync (view definitions, type
// normalization), the table browser (precise column types) and the
// transaction lifecycle (ErrTxDone folding).

func testDialect(t *testing.T, ctx context.Context, d dbdriver.Driver, c dbdriver.Connection, fx Fixtures) {
	dia := d.Dialect()
	tn := "ct_dialect"
	db, schema, qualified := makeContractTable(t, ctx, d, c, fx, tn)

	// ListColumns must return the precise native type — the table browser
	// merges it over the scanner's bare DatabaseTypeName. The fixture's name
	// column is varchar(64): the length must survive.
	cols, err := c.Metadata().ListColumns(ctx, db, schema, tn)
	if err != nil {
		t.Fatalf("ListColumns: %v", err)
	}
	var nameType string
	for _, col := range cols {
		if strings.EqualFold(col.Name, "name") {
			nameType = col.NativeType
		}
	}
	if !strings.Contains(nameType, "64") {
		t.Fatalf("ListColumns must return the full native type (varchar(64)), got %q", nameType)
	}

	// NormalizeType must be idempotent and stable over every type the driver
	// itself reports — schemadiff compares through it.
	for _, col := range cols {
		once := dia.NormalizeType(col.NativeType)
		if once == "" {
			t.Fatalf("NormalizeType(%q) returned empty", col.NativeType)
		}
		if twice := dia.NormalizeType(once); twice != once {
			t.Fatalf("NormalizeType not idempotent: %q → %q → %q", col.NativeType, once, twice)
		}
	}

	// DefaultNamespaceSQL — when the dialect has one, it must execute cleanly
	// against a namespace the server reported (query_service runs it verbatim
	// to pin the default schema).
	ns := db
	if d.Capabilities().Schemas {
		ns = schema
	}
	if stmt := dia.DefaultNamespaceSQL(ns); stmt != "" {
		if _, err := c.Querier().Exec(ctx, stmt); err != nil {
			t.Fatalf("DefaultNamespaceSQL(%q) = %q failed: %v", ns, stmt, err)
		}
	}
	if stmt := dia.DefaultNamespaceSQL("  "); stmt != "" {
		t.Fatalf("DefaultNamespaceSQL(blank) must be empty, got %q", stmt)
	}

	// TruncateTableSQL is used by data-transfer overwrite mode — it must
	// address the given qualified table verbatim.
	if stmt := dia.TruncateTableSQL(qualified); stmt == "" || !strings.Contains(stmt, qualified) {
		t.Fatalf("TruncateTableSQL(%q) = %q, must be non-empty and reference the qualified table", qualified, stmt)
	}

	// ReplaceViewSQL is used by structure sync to (re)create a view — it must
	// return at least one statement, and the final one must define the view
	// with the given body (statements run in order, so a preceding DROP is
	// fine as long as the view exists with this definition afterwards).
	def := "SELECT 1"
	stmts := dia.ReplaceViewSQL(qualified, def)
	if len(stmts) == 0 {
		t.Fatalf("ReplaceViewSQL(%q, %q) returned no statements", qualified, def)
	}
	if last := stmts[len(stmts)-1]; !strings.Contains(last, qualified) || !strings.Contains(last, def) {
		t.Fatalf("ReplaceViewSQL(%q, %q) last statement = %q, must define the view with the given qualified name and body", qualified, def, last)
	}
}

func testViewDefinitions(t *testing.T, ctx context.Context, d dbdriver.Driver, c dbdriver.Connection, fx Fixtures) {
	if !d.Capabilities().Views {
		t.Skip("driver has no views")
	}
	dia := d.Dialect()
	tn := "ct_viewbase"
	vn := "ct_view"
	db, schema, qualified := makeContractTable(t, ctx, d, c, fx, tn)
	qv := dbdriver.QualifyTable(dia, db, schema, vn)
	mustExec(t, ctx, c, "DROP VIEW IF EXISTS "+qv)
	mustExec(t, ctx, c, "CREATE VIEW "+qv+" AS SELECT id, name FROM "+qualified)
	t.Cleanup(func() {
		_, _ = c.Querier().Exec(ctx, "DROP VIEW IF EXISTS "+qv)
	})

	views, err := c.Metadata().ListViews(ctx, db, schema)
	if err != nil {
		t.Fatalf("ListViews: %v", err)
	}
	found := false
	for _, v := range views {
		if v.Name == vn {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListViews missing %s", vn)
	}

	defs, err := c.Metadata().ListViewDefinitions(ctx, db, schema)
	if err != nil {
		t.Fatalf("ListViewDefinitions: %v", err)
	}
	if def := defs[vn]; !strings.Contains(strings.ToLower(def), "select") {
		t.Fatalf("ListViewDefinitions[%s] should contain the SELECT body, got %q", vn, def)
	}
}

func testTxLifecycle(t *testing.T, ctx context.Context, c dbdriver.Connection) {
	// Begin with explicit options must work (nil is the common path elsewhere).
	tx, err := c.Begin(ctx, &dbdriver.TxOptions{Isolation: dbdriver.IsolationReadCommitted})
	if err != nil {
		t.Fatalf("Begin(TxOptions): %v", err)
	}
	if _, err := tx.Query(ctx, "SELECT 1"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("Query inside tx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// A finished transaction must report the driver-neutral ErrTxDone —
	// releaseTx in the service layer folds it into "success" via errors.Is.
	tx2, err := c.Begin(ctx, nil)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if err := tx2.Rollback(); !errors.Is(err, dbdriver.ErrTxDone) {
		t.Fatalf("second Rollback must be dbdriver.ErrTxDone, got %v", err)
	}
	if err := tx2.Commit(); !errors.Is(err, dbdriver.ErrTxDone) {
		t.Fatalf("Commit after Rollback must be dbdriver.ErrTxDone, got %v", err)
	}
}

// TestUIDialect validates a driver's static UI descriptor without a live
// connection — exported so a driver's plain unit tests can call it too.
func TestUIDialect(t *testing.T, d dbdriver.Driver) {
	ui := d.UIDialect()
	if ui.EditorDialect == "" {
		t.Fatal("UIDialect.EditorDialect is empty")
	}
	if ui.IdentQuote == "" {
		t.Fatal("UIDialect.IdentQuote is empty")
	}
	switch ui.NamespaceTerm {
	case "", "database", "schema":
	default:
		t.Fatalf("UIDialect.NamespaceTerm %q is not a known term", ui.NamespaceTerm)
	}
	validKinds := map[string]bool{
		"length": true, "displayWidth": true, "precisionScale": true,
		"fractionalSeconds": true, "enumValues": true, "none": true,
	}
	for typ, f := range ui.TypeFormats {
		if typ != strings.ToUpper(typ) {
			t.Fatalf("TypeFormats key %q must be uppercase", typ)
		}
		if !validKinds[f.Kind] {
			t.Fatalf("TypeFormats[%s].Kind %q is not a known kind", typ, f.Kind)
		}
	}
	seen := map[string]bool{}
	for _, g := range ui.TypeGroups {
		if g.Key == "" {
			t.Fatal("UITypeGroup.Key is empty")
		}
		for _, typ := range g.Types {
			if typ != strings.ToUpper(typ) {
				t.Fatalf("type %q in group %s must be uppercase", typ, g.Key)
			}
			if seen[typ] {
				t.Fatalf("type %q appears in more than one group", typ)
			}
			seen[typ] = true
		}
	}
	if ui.AutoIncrement.Supported {
		for _, bt := range ui.AutoIncrement.BaseTypes {
			if bt != strings.ToUpper(bt) {
				t.Fatalf("AutoIncrement.BaseTypes entry %q must be uppercase", bt)
			}
		}
	}
	for _, f := range ui.Functions {
		if f.Name == "" {
			t.Fatal("UIFunction.Name is empty")
		}
	}
	for _, s := range ui.Snippets {
		if s.Label == "" || s.Body == "" {
			t.Fatalf("UISnippet must have label and body, got %+v", s)
		}
	}
}
