package mysqldrv

import (
	"reflect"
	"testing"
)

func TestBuildInsert(t *testing.T) {
	e := editor{dialect: dialect{}}
	sqlText, args, err := e.BuildInsert("users", map[string]any{
		"name":  "Alice",
		"email": "a@example.com",
	})
	if err != nil {
		t.Fatalf("BuildInsert: %v", err)
	}
	want := "INSERT INTO `users` (`email`, `name`) VALUES (?, ?)"
	if sqlText != want {
		t.Errorf("sql: got %q want %q", sqlText, want)
	}
	if !reflect.DeepEqual(args, []any{"a@example.com", "Alice"}) {
		t.Errorf("args: %v", args)
	}
}

func TestBuildInsert_DbTable(t *testing.T) {
	e := editor{dialect: dialect{}}
	sqlText, _, err := e.BuildInsert("app.users", map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("BuildInsert: %v", err)
	}
	if sqlText != "INSERT INTO `app`.`users` (`id`) VALUES (?)" {
		t.Errorf("got %q", sqlText)
	}
}

func TestBuildInsert_EmptyRefused(t *testing.T) {
	e := editor{dialect: dialect{}}
	if _, _, err := e.BuildInsert("users", nil); err == nil {
		t.Fatal("expected error for empty row")
	}
	if _, _, err := e.BuildInsert("", map[string]any{"a": 1}); err == nil {
		t.Fatal("expected error for empty table")
	}
}

func TestBuildUpdate(t *testing.T) {
	e := editor{dialect: dialect{}}
	sqlText, args, err := e.BuildUpdate("users",
		map[string]any{"id": 7},
		map[string]any{"name": "Alice", "email": "a@example.com"},
	)
	if err != nil {
		t.Fatalf("BuildUpdate: %v", err)
	}
	want := "UPDATE `users` SET `email` = ?, `name` = ? WHERE `id` = ?"
	if sqlText != want {
		t.Errorf("sql: got %q want %q", sqlText, want)
	}
	if !reflect.DeepEqual(args, []any{"a@example.com", "Alice", 7}) {
		t.Errorf("args: %v", args)
	}
}

func TestBuildUpdate_RefusesEmptyPK(t *testing.T) {
	e := editor{dialect: dialect{}}
	if _, _, err := e.BuildUpdate("users", nil, map[string]any{"a": 1}); err == nil {
		t.Fatal("expected error: keyless UPDATE forbidden")
	}
}

func TestBuildUpdate_RefusesEmptyChanges(t *testing.T) {
	e := editor{dialect: dialect{}}
	if _, _, err := e.BuildUpdate("users", map[string]any{"id": 1}, nil); err == nil {
		t.Fatal("expected error: no changes")
	}
}

func TestBuildUpdate_NullablePK(t *testing.T) {
	// pk value nil → IS NULL, not = ?
	e := editor{dialect: dialect{}}
	sqlText, args, err := e.BuildUpdate("t",
		map[string]any{"k": nil},
		map[string]any{"v": 1},
	)
	if err != nil {
		t.Fatalf("BuildUpdate: %v", err)
	}
	want := "UPDATE `t` SET `v` = ? WHERE `k` IS NULL"
	if sqlText != want {
		t.Errorf("sql: got %q want %q", sqlText, want)
	}
	if !reflect.DeepEqual(args, []any{1}) {
		t.Errorf("args: %v", args)
	}
}

func TestBuildDelete(t *testing.T) {
	e := editor{dialect: dialect{}}
	sqlText, args, err := e.BuildDelete("users", map[string]any{"id": 9})
	if err != nil {
		t.Fatalf("BuildDelete: %v", err)
	}
	if sqlText != "DELETE FROM `users` WHERE `id` = ?" {
		t.Errorf("sql: got %q", sqlText)
	}
	if !reflect.DeepEqual(args, []any{9}) {
		t.Errorf("args: %v", args)
	}
}

func TestBuildDelete_RefusesEmptyPK(t *testing.T) {
	e := editor{dialect: dialect{}}
	if _, _, err := e.BuildDelete("users", nil); err == nil {
		t.Fatal("expected error: keyless DELETE forbidden")
	}
}
