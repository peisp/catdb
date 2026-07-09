package dmdrv

import (
	"fmt"
	"regexp"
	"strings"

	"catdb/internal/dbdriver"
)

// dialect implements dbdriver.Dialect for DM (达梦).
type dialect struct{}

func (dialect) QuoteIdentifier(name string) string {
	// DM identifiers use double quotes; escape embedded quotes by doubling.
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// DefaultNamespaceSQL pins the session's default schema. DM collapses the
// schema level into the database position (Capabilities.Schemas=false, like
// MySQL), so name arrives as the "database" and maps onto SET SCHEMA.
func (d dialect) DefaultNamespaceSQL(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	return "SET SCHEMA " + d.QuoteIdentifier(name)
}

func (dialect) ScriptRules() dbdriver.ScriptRules {
	// ANSI lexing: double-quoted identifiers, literal backslashes, -- and
	// /* */ comments only. No client-side DELIMITER, no dollar quoting.
	return dbdriver.ScriptRules{}
}

func (dialect) Placeholder(int) string { return "?" }

// Paginate uses DM's native LIMIT/OFFSET form (supported alongside TOP and
// OFFSET … FETCH regardless of compatibility mode).
func (dialect) Paginate(baseSQL string, limit, offset int) string {
	if limit <= 0 {
		return baseSQL
	}
	if offset < 0 {
		offset = 0
	}
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", baseSQL, limit, offset)
}

// MapType accepts both bare dictionary names ("VARCHAR2", "NUMBER") and
// parameterized spellings ("VARCHAR(64)", "NUMBER(10,2)") — the resultset
// feeds it the driver's DatabaseTypeName, the metadata layer feeds it
// composed native types.
func (dialect) MapType(nativeType string) dbdriver.LogicalType {
	upper := strings.ToUpper(strings.TrimSpace(nativeType))
	if i := strings.IndexByte(upper, '('); i >= 0 {
		upper = strings.TrimSpace(upper[:i])
	}
	switch upper {
	case "TINYINT", "SMALLINT", "INT", "INTEGER", "BYTE":
		return dbdriver.TypeInt
	case "BIGINT":
		return dbdriver.TypeBigInt
	case "FLOAT", "DOUBLE", "REAL", "DOUBLE PRECISION":
		return dbdriver.TypeFloat
	case "NUMBER", "NUMERIC", "DECIMAL", "DEC":
		return dbdriver.TypeDecimal
	case "BIT", "BOOL", "BOOLEAN":
		return dbdriver.TypeBool
	case "CHAR", "CHARACTER", "VARCHAR", "VARCHAR2", "NCHAR", "NVARCHAR", "NVARCHAR2":
		return dbdriver.TypeString
	case "TEXT", "CLOB", "LONG", "LONGVARCHAR", "NCLOB":
		return dbdriver.TypeText
	case "BINARY", "VARBINARY", "BLOB", "IMAGE", "LONGVARBINARY", "BFILE", "RAW":
		return dbdriver.TypeBytes
	case "DATE":
		return dbdriver.TypeDate
	case "TIME":
		return dbdriver.TypeTime
	case "DATETIME":
		return dbdriver.TypeDateTime
	case "TIMESTAMP":
		return dbdriver.TypeTimestamp
	default:
		// Composite spellings ("TIMESTAMP WITH TIME ZONE", "TIME WITH TIME ZONE").
		switch {
		case strings.HasPrefix(upper, "TIMESTAMP"), strings.HasPrefix(upper, "DATETIME"):
			return dbdriver.TypeTimestamp
		case strings.HasPrefix(upper, "TIME"):
			return dbdriver.TypeTime
		case strings.HasPrefix(upper, "INTERVAL"):
			return dbdriver.TypeString
		}
		return dbdriver.TypeUnknown
	}
}

var (
	reSpaces    = regexp.MustCompile(`\s+`)
	reTypeParam = regexp.MustCompile(`^([^()]+?)\s*\((.+?)\)\s*(.*)$`)
)

// typeAliases folds DM's type spellings onto one canonical (uppercase) name
// so schemadiff compares "VARCHAR2(64)" (Oracle spelling) equal to
// "VARCHAR(64)" (as written in the structure editor), NUMBER/DECIMAL/DEC to
// NUMERIC, INTEGER to INT, etc.
var typeAliases = map[string]string{
	"VARCHAR2":         "VARCHAR",
	"CHARACTER":        "CHAR",
	"NVARCHAR2":        "NVARCHAR",
	"INTEGER":          "INT",
	"NUMBER":           "NUMERIC",
	"DECIMAL":          "NUMERIC",
	"DEC":              "NUMERIC",
	"DOUBLE PRECISION": "DOUBLE",
	"BOOL":             "BIT",
	"BOOLEAN":          "BIT",
	"LONGVARCHAR":      "TEXT",
	"CLOB":             "TEXT",
	"LONGVARBINARY":    "IMAGE",
}

// NormalizeType canonicalizes a DM type string for equality comparison:
// alias folding, uppercase base, no whitespace inside parens. Idempotent —
// normalizing its own output is a no-op (contract-tested).
func (dialect) NormalizeType(nativeType string) string {
	s := reSpaces.ReplaceAllString(strings.TrimSpace(nativeType), " ")
	if s == "" {
		return ""
	}
	base, params, tail := strings.ToUpper(s), "", ""
	if m := reTypeParam.FindStringSubmatch(s); m != nil {
		base = strings.ToUpper(strings.TrimSpace(m[1]))
		parts := strings.Split(m[2], ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		params = strings.Join(parts, ",")
		tail = strings.ToUpper(strings.TrimSpace(m[3])) // "WITH TIME ZONE" etc.
	}
	if out, ok := typeAliases[base]; ok {
		base = out
	}
	out := base
	if params != "" {
		out += "(" + params + ")"
	}
	if tail != "" {
		out += " " + tail
	}
	return out
}
