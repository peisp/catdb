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
