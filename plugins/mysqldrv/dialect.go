package mysqldrv

import (
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
)

// dialect implements dbdriver.Dialect for MySQL.
type dialect struct{}

func (dialect) QuoteIdentifier(name string) string {
	// MySQL identifiers use backticks; escape embedded backticks by doubling.
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
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
