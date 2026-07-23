package postgresdrv

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"catdb/internal/dbdriver"
)

// dialect implements dbdriver.Dialect for PostgreSQL.
type dialect struct{}

func (dialect) QuoteIdentifier(name string) string {
	// Postgres identifiers use double quotes; escape embedded quotes by doubling.
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (d dialect) DefaultNamespaceSQL(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	return "SET search_path TO " + d.QuoteIdentifier(name)
}

func (dialect) ScriptRules() dbdriver.ScriptRules {
	return dbdriver.ScriptRules{
		DollarQuoting: true,
	}
}

func (dialect) Placeholder(i int) string { return "$" + strconv.Itoa(i) }

func (dialect) Paginate(baseSQL string, limit, offset int) string {
	if limit <= 0 {
		return baseSQL
	}
	if offset < 0 {
		offset = 0
	}
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", baseSQL, limit, offset)
}

// MapType accepts both pg_type names ("int4", "timestamptz") and verbose
// format_type/information_schema names ("integer", "timestamp with time
// zone") — the resultset feeds it OID names, the metadata layer feeds it
// pg_type names too, and NormalizeType output may reach it as well.
func (dialect) MapType(nativeType string) dbdriver.LogicalType {
	s := strings.ToLower(strings.TrimSpace(nativeType))
	for strings.HasSuffix(s, "[]") {
		return dbdriver.TypeString // arrays render as their text form
	}
	if i := strings.IndexByte(s, '('); i >= 0 {
		// "varchar(255)" → "varchar"; keep any trailing words ("(3) with time zone").
		tail := s[i:]
		if j := strings.IndexByte(tail, ')'); j >= 0 {
			s = strings.TrimSpace(s[:i] + tail[j+1:])
		} else {
			s = strings.TrimSpace(s[:i])
		}
	}
	switch s {
	case "int2", "smallint", "int4", "int", "integer", "serial", "smallserial", "serial2", "serial4":
		return dbdriver.TypeInt
	case "int8", "bigint", "bigserial", "serial8":
		return dbdriver.TypeBigInt
	case "float4", "real", "float8", "double precision", "float":
		return dbdriver.TypeFloat
	case "numeric", "decimal", "money":
		return dbdriver.TypeDecimal
	case "bool", "boolean":
		return dbdriver.TypeBool
	case "varchar", "character varying", "char", "character", "bpchar", "name", "citext":
		return dbdriver.TypeString
	case "text":
		return dbdriver.TypeText
	case "bytea":
		return dbdriver.TypeBytes
	case "json", "jsonb":
		return dbdriver.TypeJSON
	case "date":
		return dbdriver.TypeDate
	case "time", "timetz", "time without time zone", "time with time zone":
		return dbdriver.TypeTime
	case "timestamp", "timestamp without time zone":
		return dbdriver.TypeDateTime
	case "timestamptz", "timestamp with time zone":
		return dbdriver.TypeTimestamp
	case "uuid":
		return dbdriver.TypeUUID
	default:
		return dbdriver.TypeUnknown
	}
}

var (
	reSpaces    = regexp.MustCompile(`\s+`)
	reTypeParam = regexp.MustCompile(`^([^()]+?)\s*\((.+?)\)\s*(.*)$`)
)

// typeAliases folds Postgres type spellings onto one canonical (uppercase)
// name so schemadiff compares "character varying(64)" (read back from the
// catalog) equal to "VARCHAR(64)" (as written in the structure editor).
var typeAliases = map[string]string{
	"character varying": "VARCHAR",
	"varchar":           "VARCHAR",
	"character":         "CHAR",
	"bpchar":            "CHAR",
	"char":              "CHAR",
	"int":               "INTEGER",
	"int4":              "INTEGER",
	"integer":           "INTEGER",
	"serial":            "INTEGER",
	"serial4":           "INTEGER",
	"int8":              "BIGINT",
	"bigint":            "BIGINT",
	"bigserial":         "BIGINT",
	"serial8":           "BIGINT",
	"int2":              "SMALLINT",
	"smallint":          "SMALLINT",
	"smallserial":       "SMALLINT",
	"serial2":           "SMALLINT",
	"bool":              "BOOLEAN",
	"boolean":           "BOOLEAN",
	"float4":            "REAL",
	"real":              "REAL",
	"float":             "DOUBLE PRECISION",
	"float8":            "DOUBLE PRECISION",
	"double precision":  "DOUBLE PRECISION",
	"decimal":           "NUMERIC",
	"numeric":           "NUMERIC",
	"timestamptz":       "TIMESTAMPTZ",
	"timetz":            "TIMETZ",
	"timestamp":         "TIMESTAMP",
	"time":              "TIME",
}

// NormalizeType canonicalizes a Postgres type string for equality comparison:
// alias folding (varchar/character varying, int4/integer, serial/integer, …),
// "with(out) time zone" folded into TIMESTAMPTZ/TIMESTAMP (TIMETZ/TIME),
// uppercase base, no whitespace inside parens, array suffix preserved.
// Idempotent — normalizing its own output is a no-op (contract-tested).
func (dialect) NormalizeType(nativeType string) string {
	s := reSpaces.ReplaceAllString(strings.TrimSpace(nativeType), " ")
	if s == "" {
		return ""
	}
	// Peel array suffixes ("integer[]", "text[][]").
	arr := ""
	for strings.HasSuffix(s, "[]") {
		arr += "[]"
		s = strings.TrimSuffix(s, "[]")
	}
	low := strings.ToLower(strings.TrimSpace(s))

	// Fold the time-zone tail before splitting off params:
	// "timestamp(3) with time zone" → base "timestamp"+tz, params "3".
	wtz := false
	if strings.HasSuffix(low, " with time zone") {
		wtz = true
		low = strings.TrimSuffix(low, " with time zone")
	} else {
		low = strings.TrimSuffix(low, " without time zone")
	}
	low = strings.TrimSpace(low)

	base, params := low, ""
	if m := reTypeParam.FindStringSubmatch(low); m != nil && strings.TrimSpace(m[3]) == "" {
		base = strings.TrimSpace(m[1])
		parts := strings.Split(m[2], ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		params = strings.Join(parts, ",")
	}
	if wtz {
		switch base {
		case "timestamp":
			base = "timestamptz"
		case "time":
			base = "timetz"
		}
	}

	out, ok := typeAliases[base]
	if !ok {
		out = strings.ToUpper(base)
	}
	if params != "" {
		out += "(" + params + ")"
	}
	return out + arr
}

func (dialect) TruncateTableSQL(qualified string) string {
	return "TRUNCATE TABLE " + qualified
}

// ReplaceViewSQL: Postgres' CREATE OR REPLACE VIEW rejects redefinitions that
// change the output column set, so a plain redefinition needs DROP + CREATE.
func (dialect) ReplaceViewSQL(qualified, definition string) []string {
	return []string{
		"DROP VIEW IF EXISTS " + qualified + ";",
		"CREATE VIEW " + qualified + " AS " + definition + ";",
	}
}
