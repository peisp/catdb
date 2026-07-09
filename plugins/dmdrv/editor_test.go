package dmdrv

import (
	"reflect"
	"testing"
)

func TestBuildInsert(t *testing.T) {
	e := editor{dialect: dialect{}}
	sqlText, args, err := e.BuildInsert("SALES", "", "orders", map[string]any{"name": "Alice", "amount": 3})
	if err != nil {
		t.Fatalf("BuildInsert: %v", err)
	}
	want := `INSERT INTO "SALES"."orders" ("amount", "name") VALUES (?, ?)`
	if sqlText != want {
		t.Errorf("sql = %q, want %q", sqlText, want)
	}
	if !reflect.DeepEqual(args, []any{3, "Alice"}) {
		t.Errorf("args = %v", args)
	}

	if _, _, err := e.BuildInsert("SALES", "", "", map[string]any{"a": 1}); err == nil {
		t.Error("empty table should error")
	}
	if _, _, err := e.BuildInsert("SALES", "", "orders", nil); err == nil {
		t.Error("empty row should error")
	}
}

func TestBuildUpdate(t *testing.T) {
	e := editor{dialect: dialect{}}
	sqlText, args, err := e.BuildUpdate("SALES", "", "orders",
		map[string]any{"id": 7}, map[string]any{"name": "Bob"})
	if err != nil {
		t.Fatalf("BuildUpdate: %v", err)
	}
	want := `UPDATE "SALES"."orders" SET "name" = ? WHERE "id" = ?`
	if sqlText != want {
		t.Errorf("sql = %q, want %q", sqlText, want)
	}
	if !reflect.DeepEqual(args, []any{"Bob", 7}) {
		t.Errorf("args = %v", args)
	}

	if _, _, err := e.BuildUpdate("SALES", "", "orders", nil, map[string]any{"a": 1}); err == nil {
		t.Error("keyless UPDATE must be refused")
	}
	if _, _, err := e.BuildUpdate("SALES", "", "orders", map[string]any{"id": 1}, nil); err == nil {
		t.Error("no-changes UPDATE should error")
	}
}

func TestBuildDelete(t *testing.T) {
	e := editor{dialect: dialect{}}
	sqlText, args, err := e.BuildDelete("SALES", "", "orders", map[string]any{"id": 7, "sub": nil})
	if err != nil {
		t.Fatalf("BuildDelete: %v", err)
	}
	want := `DELETE FROM "SALES"."orders" WHERE "id" = ? AND "sub" IS NULL`
	if sqlText != want {
		t.Errorf("sql = %q, want %q", sqlText, want)
	}
	if !reflect.DeepEqual(args, []any{7}) {
		t.Errorf("args = %v", args)
	}

	if _, _, err := e.BuildDelete("SALES", "", "orders", nil); err == nil {
		t.Error("keyless DELETE must be refused")
	}
}

// The schema may arrive in either position (db when the tree drives it,
// schema when a schema-ful caller does) — both must qualify identically.
func TestQualifyEitherPosition(t *testing.T) {
	e := editor{dialect: dialect{}}
	a, _, _ := e.BuildDelete("SALES", "", "orders", map[string]any{"id": 1})
	b, _, _ := e.BuildDelete("", "SALES", "orders", map[string]any{"id": 1})
	if a != b {
		t.Errorf("qualification differs by position: %q vs %q", a, b)
	}
}
