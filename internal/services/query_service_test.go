package services

import "testing"

func TestLooksLikeRowsQuery(t *testing.T) {
	cases := map[string]bool{
		"SELECT * FROM t": true,
		"  select 1":      true,
		"WITH cte AS (SELECT 1) SELECT * FROM cte": true,
		"SHOW TABLES":      true,
		"DESCRIBE t":       true,
		"EXPLAIN SELECT 1": true,
		"TABLE t":          true,
		"VALUES (1)":       true,
		"(SELECT 1)":       true,
		"\n\tSELECT 1":     true,

		"INSERT INTO t VALUES (1)": false,
		"UPDATE t SET x=1":         false,
		"DELETE FROM t":            false,
		"CREATE TABLE t (x INT)":   false,
		"DROP TABLE t":             false,
		"BEGIN":                    false,
		"COMMIT":                   false,
	}
	for in, want := range cases {
		if got := looksLikeRowsQuery(in); got != want {
			t.Errorf("looksLikeRowsQuery(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestExtractTableRef(t *testing.T) {
	cases := []struct {
		sql           string
		defaultSchema string
		wantDB        string
		wantTable     string // "" = expect nil
	}{
		{"SELECT * FROM t", "mydb", "mydb", "t"},
		{"SELECT * FROM db1.t", "", "db1", "t"},
		{"SELECT * FROM `db1`.`t`", "", "db1", "t"},
		// ANSI double-quoted identifiers (DM/Postgres/Oracle style).
		{`SELECT * FROM "OPERATION_LOG"`, "SYSDBA", "SYSDBA", "OPERATION_LOG"},
		{`SELECT * FROM "S1"."T1"`, "", "S1", "T1"},
		// No default namespace for an unqualified name → nil.
		{"SELECT * FROM t", "", "", ""},
		// Multi-table / aggregate → nil.
		{"SELECT * FROM a JOIN b ON a.id=b.id", "d", "", ""},
		{"SELECT x, COUNT(*) FROM t GROUP BY x", "d", "", ""},
	}
	for _, c := range cases {
		got := extractTableRef(c.sql, c.defaultSchema)
		if c.wantTable == "" {
			if got != nil {
				t.Errorf("extractTableRef(%q, %q) = %+v, want nil", c.sql, c.defaultSchema, got)
			}
			continue
		}
		if got == nil || got.DB != c.wantDB || got.Table != c.wantTable {
			t.Errorf("extractTableRef(%q, %q) = %+v, want {%s %s}", c.sql, c.defaultSchema, got, c.wantDB, c.wantTable)
		}
	}
}

func TestIsCountableQuery(t *testing.T) {
	cases := map[string]bool{
		"SELECT * FROM t":                    true,
		"  select id from t limit 5":         true,
		"WITH x AS (SELECT 1) SELECT *":      true,
		"(SELECT 1)":                         true,
		"TABLE t":                            true,
		"VALUES ROW(1)":                      true,
		"-- note\nSELECT 1":                  true,
		"# note\nSELECT 1":                   true,
		"/* c */ SELECT 1":                   true,
		"/* a */\n-- b\nSELECT 1":            true,
		"SELECT for_update FROM t":           true,
		"SHOW TABLES":                        false,
		"EXPLAIN SELECT 1":                   false,
		"DESC t":                             false,
		"UPDATE t SET a=1":                   false,
		"INSERT INTO t VALUES (1)":           false,
		"-- only a comment":                  false,
		"SELECT * FROM t FOR UPDATE":         false,
		"SELECT * FROM t for share":          false,
		"SELECT * FROM t LOCK IN SHARE MODE": false,
		"SELECT 1 FROM t\nFOR\nUPDATE":       false,
	}
	for in, want := range cases {
		if got := isCountableQuery(in); got != want {
			t.Errorf("isCountableQuery(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestScalarToInt64(t *testing.T) {
	for _, c := range []struct {
		in   any
		want int64
		ok   bool
	}{
		{int64(42), 42, true},
		{uint64(7), 7, true},
		{[]byte("123"), 123, true},
		{"9", 9, true},
		{3.14, 0, false},
	} {
		got, err := scalarToInt64(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("scalarToInt64(%v) = %d, %v; want %d", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("scalarToInt64(%v) expected error", c.in)
		}
	}
}
