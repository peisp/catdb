package mysqldrv

import (
	"database/sql"
	"testing"
)

// Expectations verified empirically against mariadb:11 and mysql:8.0
// information_schema.COLUMNS output for the same CREATE TABLE.
func TestNormalizeColumnDefault(t *testing.T) {
	null := sql.NullString{}
	val := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

	cases := []struct {
		name    string
		raw     sql.NullString
		mariadb bool
		want    *string
	}{
		// SQL NULL = no default, both flavors.
		{"sql-null mysql", null, false, nil},
		{"sql-null mariadb", null, true, nil},

		// MySQL reports bare values — passthrough, including the ambiguous
		// bare NULL string (a genuine user default of the string "NULL").
		{"mysql bare string", val("abc"), false, strp("abc")},
		{"mysql string NULL", val("NULL"), false, strp("NULL")},
		{"mysql keyword", val("CURRENT_TIMESTAMP"), false, strp("CURRENT_TIMESTAMP")},
		{"mysql fractional ts", val("CURRENT_TIMESTAMP(6)"), false, strp("CURRENT_TIMESTAMP(6)")},

		// MariaDB expression text → canonical bare form.
		{"mariadb no default", val("NULL"), true, nil},
		{"mariadb quoted", val("'abc'"), true, strp("abc")},
		{"mariadb doubled quote", val("'it''s'"), true, strp("it's")},
		{"mariadb escaped backslash", val(`'a\\b'`), true, strp(`a\b`)},
		{"mariadb escaped newline", val(`'per\ncent'`), true, strp("per\ncent")},
		{"mariadb string NULL", val("'NULL'"), true, strp("NULL")},
		{"mariadb numeric", val("5"), true, strp("5")},
		{"mariadb current_timestamp", val("current_timestamp()"), true, strp("CURRENT_TIMESTAMP")},
		{"mariadb fractional ts", val("current_timestamp(6)"), true, strp("CURRENT_TIMESTAMP(6)")},
		{"mariadb other expression", val("uuid()"), true, strp("uuid()")},
	}
	for _, c := range cases {
		got := normalizeColumnDefault(c.raw, c.mariadb)
		switch {
		case (got == nil) != (c.want == nil):
			t.Errorf("%s: got %v, want %v", c.name, deref(got), deref(c.want))
		case got != nil && *got != *c.want:
			t.Errorf("%s: got %q, want %q", c.name, *got, *c.want)
		}
	}
}

func deref(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
