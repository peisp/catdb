package sqlitedrv

import "catdb/internal/dbdriver"

// UIDialect is SQLite's declarative UI descriptor — the single home of SQLite
// knowledge the front-end needs (type catalog, completion functions, …).
func (driver) UIDialect() dbdriver.UIDialect {
	return dbdriver.UIDialect{
		EditorDialect: "sqlite",
		IdentQuote:    `"`,
		Keywords: []string{
			"PRAGMA", "VACUUM", "ANALYZE", "REINDEX", "ATTACH DATABASE", "DETACH DATABASE",
			"AUTOINCREMENT", "WITHOUT ROWID", "STRICT", "IF NOT EXISTS", "IF EXISTS",
			"INSERT OR REPLACE", "INSERT OR IGNORE", "ON CONFLICT", "RETURNING",
			"GLOB", "REGEXP", "EXPLAIN QUERY PLAN", "RENAME COLUMN", "ADD COLUMN", "DROP COLUMN",
		},
		Functions:   sqliteFunctions,
		Snippets:    sqliteSnippets,
		TypeGroups:  sqliteTypeGroups,
		TypeFormats: sqliteTypeFormats,

		DefaultColumnType: "TEXT",
		HasUnsigned:       false,
		AutoIncrement: dbdriver.UIAutoIncrement{
			Supported:   true,
			BaseTypes:   []string{"INTEGER"},
			MaxPerTable: 1,
		},
		PrimaryKeyForcesNotNull: true,
		// SQLite has a single index access method — hide the selector.
		IndexTypes: nil,
	}
}

var sqliteTypeGroups = []dbdriver.UITypeGroup{
	{Key: "string", Types: []string{"TEXT", "VARCHAR", "CHAR", "NCHAR", "NVARCHAR", "CLOB"}},
	{Key: "integer", Types: []string{"INTEGER", "INT", "BIGINT", "SMALLINT", "TINYINT"}},
	{Key: "decimal", Types: []string{"REAL", "DOUBLE", "FLOAT", "NUMERIC", "DECIMAL"}},
	{Key: "datetime", Types: []string{"DATETIME", "DATE", "TIMESTAMP"}},
	{Key: "binary", Types: []string{"BLOB"}},
	{Key: "boolean", Types: []string{"BOOLEAN"}},
	{Key: "other", Types: []string{"JSON"}},
}

var sqliteTypeFormats = map[string]dbdriver.UITypeFormat{
	"VARCHAR":  {Kind: "length"},
	"CHAR":     {Kind: "length"},
	"NCHAR":    {Kind: "length"},
	"NVARCHAR": {Kind: "length"},
	"NUMERIC":  {Kind: "precisionScale"},
	"DECIMAL":  {Kind: "precisionScale"},
}

var sqliteSnippets = []dbdriver.UISnippet{
	{
		Label:  "createtable",
		Detail: "CREATE TABLE …",
		Body: "CREATE TABLE ${name} (\n  id INTEGER PRIMARY KEY AUTOINCREMENT,\n" +
			"  ${cols}\n)${}",
	},
}

var sqliteFunctions = []dbdriver.UIFunction{
	{Name: "COUNT", Category: "aggregate", Info: "COUNT(expr) — number of non-NULL rows", Params: []string{"expr"}},
	{Name: "SUM", Category: "aggregate", Info: "SUM(expr) — sum of expr", Params: []string{"expr"}},
	{Name: "TOTAL", Category: "aggregate", Info: "TOTAL(expr) — SUM that returns 0.0 instead of NULL", Params: []string{"expr"}},
	{Name: "AVG", Category: "aggregate", Info: "AVG(expr) — average of expr", Params: []string{"expr"}},
	{Name: "MIN", Category: "aggregate", Info: "MIN(expr)", Params: []string{"expr"}},
	{Name: "MAX", Category: "aggregate", Info: "MAX(expr)", Params: []string{"expr"}},
	{Name: "GROUP_CONCAT", Category: "aggregate", Info: "GROUP_CONCAT(expr[, separator])", Params: []string{"expr", "separator"}},
	{Name: "LENGTH", Category: "string", Info: "LENGTH(str) — character length"},
	{Name: "SUBSTR", Category: "string", Info: "SUBSTR(str, start[, len])", Params: []string{"str", "start", "len"}},
	{Name: "INSTR", Category: "string", Info: "INSTR(str, substr) — 1-based position", Params: []string{"str", "substr"}},
	{Name: "REPLACE", Category: "string", Info: "REPLACE(str, from, to)", Params: []string{"str", "from", "to"}},
	{Name: "TRIM", Category: "string"},
	{Name: "LTRIM", Category: "string"},
	{Name: "RTRIM", Category: "string"},
	{Name: "LOWER", Category: "string"},
	{Name: "UPPER", Category: "string"},
	{Name: "PRINTF", Category: "string", Info: "PRINTF(format, …) — C-style formatting", Params: []string{"format", "…"}},
	{Name: "QUOTE", Category: "string", Info: "QUOTE(value) — SQL literal form of value", Params: []string{"value"}},
	{Name: "HEX", Category: "string", Info: "HEX(blob) — uppercase hex rendering", Params: []string{"blob"}},
	{Name: "ROUND", Category: "numeric", Info: "ROUND(x[, digits])", Params: []string{"x", "digits"}},
	{Name: "ABS", Category: "numeric"},
	{Name: "CEIL", Category: "numeric"},
	{Name: "FLOOR", Category: "numeric"},
	{Name: "POW", Category: "numeric", Info: "POW(base, exp)", Params: []string{"base", "exp"}},
	{Name: "MOD", Category: "numeric", Info: "MOD(n, m)", Params: []string{"n", "m"}},
	{Name: "RANDOM", Category: "numeric", Info: "RANDOM() — random 64-bit integer", NoArgs: true},
	{Name: "DATE", Category: "datetime", Info: "DATE(value[, modifier…]) — as YYYY-MM-DD", Params: []string{"value", "…"}},
	{Name: "TIME", Category: "datetime", Info: "TIME(value[, modifier…]) — as HH:MM:SS", Params: []string{"value", "…"}},
	{Name: "DATETIME", Category: "datetime", Info: "DATETIME(value[, modifier…])", Params: []string{"value", "…"}},
	{Name: "JULIANDAY", Category: "datetime", Info: "JULIANDAY(value[, modifier…])", Params: []string{"value", "…"}},
	{Name: "STRFTIME", Category: "datetime", Info: "STRFTIME(format, value[, modifier…])", Params: []string{"format", "value", "…"}},
	{Name: "UNIXEPOCH", Category: "datetime", Info: "UNIXEPOCH(value[, modifier…]) — Unix timestamp", Params: []string{"value", "…"}},
	{Name: "CURRENT_TIMESTAMP", Category: "datetime", NoArgs: true},
	{Name: "IFNULL", Category: "control", Info: "IFNULL(expr, alt)", Params: []string{"expr", "alt"}},
	{Name: "NULLIF", Category: "control", Info: "NULLIF(a, b) — NULL if a=b", Params: []string{"a", "b"}},
	{Name: "COALESCE", Category: "control", Info: "COALESCE(a, b, …) — first non-NULL", Params: []string{"a", "b", "…"}},
	{Name: "IIF", Category: "control", Info: "IIF(cond, then, else)", Params: []string{"cond", "then", "else"}},
	{Name: "JSON", Category: "json", Info: "JSON(text) — validate and minify", Params: []string{"text"}},
	{Name: "JSON_EXTRACT", Category: "json", Info: "JSON_EXTRACT(json, path, …)", Params: []string{"json", "path"}},
	{Name: "JSON_OBJECT", Category: "json"},
	{Name: "JSON_ARRAY", Category: "json"},
	{Name: "JSON_SET", Category: "json", Info: "JSON_SET(json, path, value, …)", Params: []string{"json", "path", "value"}},
	{Name: "CAST", Category: "cast", Info: "CAST(expr AS type)", Params: []string{"expr AS type"}},
	{Name: "SQLITE_VERSION", Category: "system", NoArgs: true},
	{Name: "LAST_INSERT_ROWID", Category: "system", NoArgs: true},
	{Name: "CHANGES", Category: "system", NoArgs: true},
	{Name: "TOTAL_CHANGES", Category: "system", NoArgs: true},
}
