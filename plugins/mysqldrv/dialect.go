package mysqldrv

import (
	"fmt"
	"regexp"
	"strings"

	"catdb/internal/dbdriver"
)

// dialect implements dbdriver.Dialect for MySQL.
type dialect struct{}

func (dialect) QuoteIdentifier(name string) string {
	// MySQL identifiers use backticks; escape embedded backticks by doubling.
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

var (
	reZerofill = regexp.MustCompile(`(?i)\s+ZEROFILL\b`)
	reUnsigned = regexp.MustCompile(`(?i)\s+UNSIGNED\b`)
	reTypeParm = regexp.MustCompile(`^([^()]+?)\s*\((.+)\)\s*$`)
)

// NormalizeType canonicalizes a MySQL COLUMN_TYPE for equality comparison:
// uppercase base, no whitespace inside parens, UNSIGNED suffix in a fixed
// position, ZEROFILL dropped.
func (dialect) NormalizeType(nativeType string) string {
	s := strings.TrimSpace(nativeType)
	if s == "" {
		return ""
	}
	s = reZerofill.ReplaceAllString(s, "")
	unsigned := false
	if reUnsigned.MatchString(s) {
		unsigned = true
		s = reUnsigned.ReplaceAllString(s, "")
	}
	s = strings.TrimSpace(s)
	base, params := strings.ToUpper(s), ""
	if m := reTypeParm.FindStringSubmatch(s); m != nil {
		base = strings.ToUpper(strings.TrimSpace(m[1]))
		parts := strings.Split(strings.TrimSpace(m[2]), ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		params = strings.Join(parts, ",")
	}
	// Integer display width (int(11), bigint(20), tinyint(4)…) is cosmetic and
	// deprecated: MySQL 8.0 drops it from COLUMN_TYPE, MariaDB still reports it.
	// Strip it so both introspect to the same normalized type and schema-diff
	// converges. TINYINT(1) is kept — it's the conventional BOOLEAN marker.
	if baseTypeIsInteger(base) && !(base == "TINYINT" && params == "1") {
		params = ""
	}
	out := base
	if params != "" {
		out += "(" + params + ")"
	}
	if unsigned && baseTypeSupportsUnsigned(base) {
		out += " UNSIGNED"
	}
	return out
}

func baseTypeIsInteger(base string) bool {
	switch strings.ToUpper(base) {
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BIGINT":
		return true
	}
	return false
}

func baseTypeSupportsUnsigned(base string) bool {
	switch strings.ToUpper(base) {
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BIGINT",
		"DECIMAL", "NUMERIC", "FLOAT", "DOUBLE", "REAL":
		return true
	}
	return false
}

func (dialect) ScriptRules() dbdriver.ScriptRules {
	return dbdriver.ScriptRules{
		BacktickIdentifiers: true,
		BackslashEscapes:    true,
		HashComments:        true,
		ClientDelimiter:     true,
	}
}

func (dialect) Placeholder(int) string { return "?" }

func (d dialect) DefaultNamespaceSQL(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	return "USE " + d.QuoteIdentifier(name)
}

func (dialect) Paginate(baseSQL string, limit, offset int) string {
	if limit <= 0 {
		return baseSQL
	}
	if offset < 0 {
		offset = 0
	}
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", baseSQL, limit, offset)
}

// MapType is the M1 starter mapping. The metadata layer (M3) will add the
// precise width / unsigned / charset distinctions.
func (dialect) MapType(nativeType string) dbdriver.LogicalType {
	upper := strings.ToUpper(nativeType)
	// Strip "(N)" or "(N,M)" so "VARCHAR(255)" matches "VARCHAR".
	if i := strings.IndexByte(upper, '('); i >= 0 {
		upper = strings.TrimSpace(upper[:i])
	}
	// "UNSIGNED" suffixes etc. don't change the logical class.
	upper = strings.TrimSpace(strings.Split(upper, " ")[0])

	switch upper {
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER":
		return dbdriver.TypeInt
	case "BIGINT":
		return dbdriver.TypeBigInt
	case "FLOAT", "DOUBLE", "REAL":
		return dbdriver.TypeFloat
	case "DECIMAL", "NUMERIC":
		return dbdriver.TypeDecimal
	case "BOOL", "BOOLEAN", "BIT":
		return dbdriver.TypeBool
	case "CHAR", "VARCHAR":
		return dbdriver.TypeString
	case "TEXT", "TINYTEXT", "MEDIUMTEXT", "LONGTEXT":
		return dbdriver.TypeText
	case "BINARY", "VARBINARY", "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB":
		return dbdriver.TypeBytes
	case "JSON":
		return dbdriver.TypeJSON
	case "DATE":
		return dbdriver.TypeDate
	case "TIME":
		return dbdriver.TypeTime
	case "DATETIME":
		return dbdriver.TypeDateTime
	case "TIMESTAMP":
		return dbdriver.TypeTimestamp
	case "ENUM":
		return dbdriver.TypeEnum
	case "SET":
		return dbdriver.TypeString
	default:
		return dbdriver.TypeUnknown
	}
}

func (dialect) TruncateTableSQL(qualified string) string {
	return "TRUNCATE TABLE " + qualified
}

func (dialect) ReplaceViewSQL(qualified, definition string) []string {
	return []string{"CREATE OR REPLACE VIEW " + qualified + " AS " + definition + ";"}
}
