package sqlitedrv

import (
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
)

// dialect implements dbdriver.Dialect for SQLite.
type dialect struct{}

func (dialect) QuoteIdentifier(name string) string {
	// SQLite standard identifier quoting: double quotes, escape by doubling.
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// DefaultNamespaceSQL: SQLite has no USE/search_path — unqualified names
// always resolve against "main".
func (dialect) DefaultNamespaceSQL(string) string { return "" }

func (dialect) ScriptRules() dbdriver.ScriptRules {
	return dbdriver.ScriptRules{
		// SQLite accepts MySQL-style backtick identifiers; the splitter must
		// not treat their content as statement text.
		BacktickIdentifiers: true,
	}
}

func (dialect) Placeholder(int) string { return "?" }

func (dialect) Paginate(baseSQL string, limit, offset int) string {
	if limit <= 0 {
		return baseSQL
	}
	if offset < 0 {
		offset = 0
	}
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", baseSQL, limit, offset)
}

// affinityOf implements SQLite's declared-type → affinity rules
// (https://sqlite.org/datatype3.html §3.1). SQLite ignores everything else
// about a declared type — length, precision, exact spelling — so the affinity
// IS the canonical type.
func affinityOf(nativeType string) string {
	u := strings.ToUpper(nativeType)
	switch {
	case strings.Contains(u, "INT"):
		return "INTEGER"
	case strings.Contains(u, "CHAR"), strings.Contains(u, "CLOB"), strings.Contains(u, "TEXT"):
		return "TEXT"
	case strings.TrimSpace(u) == "", strings.Contains(u, "BLOB"):
		return "BLOB"
	case strings.Contains(u, "REAL"), strings.Contains(u, "FLOA"), strings.Contains(u, "DOUB"):
		return "REAL"
	default:
		return "NUMERIC"
	}
}

// NormalizeType folds a declared type to its SQLite affinity. Two types with
// the same affinity are behaviorally identical in SQLite (VARCHAR(64) ==
// VARCHAR(128) == TEXT), so schema diffing must treat them as equal — SQLite
// cannot ALTER a column's type anyway.
func (dialect) NormalizeType(nativeType string) string {
	return affinityOf(nativeType)
}

// MapType maps a declared type onto the shared logical enum: well-known names
// first (DATE/BOOLEAN/… carry intent even though SQLite stores them loosely),
// then the affinity as fallback.
func (dialect) MapType(nativeType string) dbdriver.LogicalType {
	upper := strings.ToUpper(strings.TrimSpace(nativeType))
	if i := strings.IndexByte(upper, '('); i >= 0 {
		upper = strings.TrimSpace(upper[:i])
	}
	switch upper {
	case "BOOL", "BOOLEAN":
		return dbdriver.TypeBool
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "INT2", "INT8":
		return dbdriver.TypeInt
	case "BIGINT", "UNSIGNED BIG INT":
		return dbdriver.TypeBigInt
	case "REAL", "DOUBLE", "DOUBLE PRECISION", "FLOAT":
		return dbdriver.TypeFloat
	case "DECIMAL", "NUMERIC":
		return dbdriver.TypeDecimal
	case "CHAR", "VARCHAR", "NCHAR", "NVARCHAR", "VARYING CHARACTER", "NATIVE CHARACTER":
		return dbdriver.TypeString
	case "TEXT", "CLOB":
		return dbdriver.TypeText
	case "BLOB":
		return dbdriver.TypeBytes
	case "JSON", "JSONB":
		return dbdriver.TypeJSON
	case "DATE":
		return dbdriver.TypeDate
	case "TIME":
		return dbdriver.TypeTime
	case "DATETIME":
		return dbdriver.TypeDateTime
	case "TIMESTAMP":
		return dbdriver.TypeTimestamp
	}
	switch affinityOf(nativeType) {
	case "INTEGER":
		return dbdriver.TypeInt
	case "TEXT":
		return dbdriver.TypeText
	case "REAL":
		return dbdriver.TypeFloat
	case "NUMERIC":
		return dbdriver.TypeDecimal
	default:
		return dbdriver.TypeBytes
	}
}

// TruncateTableSQL: SQLite has no TRUNCATE statement.
func (dialect) TruncateTableSQL(qualified string) string {
	return "DELETE FROM " + qualified
}

// ReplaceViewSQL: SQLite has no CREATE OR REPLACE VIEW.
func (dialect) ReplaceViewSQL(qualified, definition string) []string {
	return []string{
		"DROP VIEW IF EXISTS " + qualified + ";",
		"CREATE VIEW " + qualified + " AS " + definition + ";",
	}
}
