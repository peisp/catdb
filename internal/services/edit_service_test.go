package services

import "testing"

func TestInterpolateSQLQuestionMark(t *testing.T) {
	got := interpolateSQL("UPDATE t SET a = ? WHERE id = ?", []any{"x'y", int64(7)})
	want := "UPDATE t SET a = 'x''y' WHERE id = 7"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInterpolateSQLDollar(t *testing.T) {
	got := interpolateSQL(`UPDATE "t" SET "a" = $1 WHERE "id" = $2`, []any{"v", 9})
	want := `UPDATE "t" SET "a" = 'v' WHERE "id" = 9`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// $n indices address args positionally, not in scan order.
	got = interpolateSQL("SELECT $2, $1", []any{"first", "second"})
	if want := "SELECT 'second', 'first'"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// Out-of-range or bare $ stays verbatim instead of panicking.
	got = interpolateSQL("SELECT $9, $", []any{"only"})
	if want := "SELECT $9, $"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInterpolateSQLNoArgs(t *testing.T) {
	if got := interpolateSQL("SELECT ?", nil); got != "SELECT ?" {
		t.Errorf("got %q", got)
	}
}
