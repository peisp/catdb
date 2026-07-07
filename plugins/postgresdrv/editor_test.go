package postgresdrv

import (
	"reflect"
	"testing"
)

func TestBuildInsert(t *testing.T) {
	e := editor{dialect: dialect{}}
	sqlText, args, err := e.BuildInsert("app", "public", "users", map[string]any{
		"name": "alice",
		"age":  30,
	})
	if err != nil {
		t.Fatalf("BuildInsert: %v", err)
	}
	want := `INSERT INTO "public"."users" ("age", "name") VALUES ($1, $2)`
	if sqlText != want {
		t.Errorf("sql = %q, want %q", sqlText, want)
	}
	if !reflect.DeepEqual(args, []any{30, "alice"}) {
		t.Errorf("args = %#v", args)
	}
	if _, _, err := e.BuildInsert("app", "public", "users", nil); err == nil {
		t.Error("empty row must error")
	}
	if _, _, err := e.BuildInsert("app", "public", "", map[string]any{"a": 1}); err == nil {
		t.Error("empty table must error")
	}
}

func TestBuildUpdate(t *testing.T) {
	e := editor{dialect: dialect{}}
	sqlText, args, err := e.BuildUpdate("app", "public", "users",
		map[string]any{"id": 7},
		map[string]any{"name": "bob", "age": 31},
	)
	if err != nil {
		t.Fatalf("BuildUpdate: %v", err)
	}
	want := `UPDATE "public"."users" SET "age" = $1, "name" = $2 WHERE "id" = $3`
	if sqlText != want {
		t.Errorf("sql = %q, want %q", sqlText, want)
	}
	if !reflect.DeepEqual(args, []any{31, "bob", 7}) {
		t.Errorf("args = %#v", args)
	}
	if _, _, err := e.BuildUpdate("app", "public", "users", nil, map[string]any{"a": 1}); err == nil {
		t.Error("keyless UPDATE must be refused")
	}
	if _, _, err := e.BuildUpdate("app", "public", "users", map[string]any{"id": 1}, nil); err == nil {
		t.Error("empty changes must error")
	}
}

func TestBuildUpdateNullPK(t *testing.T) {
	e := editor{dialect: dialect{}}
	sqlText, args, err := e.BuildUpdate("app", "public", "t",
		map[string]any{"a": nil, "b": 2},
		map[string]any{"x": "v"},
	)
	if err != nil {
		t.Fatalf("BuildUpdate: %v", err)
	}
	want := `UPDATE "public"."t" SET "x" = $1 WHERE "a" IS NULL AND "b" = $2`
	if sqlText != want {
		t.Errorf("sql = %q, want %q", sqlText, want)
	}
	if !reflect.DeepEqual(args, []any{"v", 2}) {
		t.Errorf("args = %#v", args)
	}
}

func TestBuildDelete(t *testing.T) {
	e := editor{dialect: dialect{}}
	sqlText, args, err := e.BuildDelete("app", "public", "users", map[string]any{"id": 9})
	if err != nil {
		t.Fatalf("BuildDelete: %v", err)
	}
	want := `DELETE FROM "public"."users" WHERE "id" = $1`
	if sqlText != want {
		t.Errorf("sql = %q, want %q", sqlText, want)
	}
	if !reflect.DeepEqual(args, []any{9}) {
		t.Errorf("args = %#v", args)
	}
	if _, _, err := e.BuildDelete("app", "public", "users", nil); err == nil {
		t.Error("keyless DELETE must be refused")
	}
}

func TestQualifyIgnoresDB(t *testing.T) {
	e := editor{dialect: dialect{}}
	if got := e.qualify("appdb", "public", "t"); got != `"public"."t"` {
		t.Errorf("qualify = %q — Postgres must not include the db level", got)
	}
	if got := e.qualify("appdb", "", "t"); got != `"public"."t"` {
		t.Errorf("qualify with empty schema must fall back to public, got %q", got)
	}
}
