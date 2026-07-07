package dmdrv

import "catdb/internal/dbdriver"

// UIDialect is DM's declarative UI descriptor — the single home of DM
// knowledge the front-end needs (type catalog, completion functions,
// snippets, …). See plugins/mysqldrv/uidialect.go for the field contract.
func (driver) UIDialect() dbdriver.UIDialect {
	return dbdriver.UIDialect{
		// DM's SQL surface is Oracle-shaped; PLSQL is the closest CodeMirror
		// dialect (see frontend/src/editor/cmDialect.ts).
		EditorDialect:          "plsql",
		IdentQuote:             `"`,
		StringBackslashEscapes: false,
		SystemSchemas:          []string{"SYS", "SYSAUDITOR", "SYSSSO", "CTISYS"},
		// No fixed default schema — each user lands in their own.
		DefaultSchema: "",
		Keywords: []string{
			"IDENTITY", "TOP", "LIMIT", "OFFSET", "ROWNUM",
			"MERGE INTO", "CONNECT BY", "START WITH", "PRIOR",
			"MINUS", "INTERSECT", "SET SCHEMA", "COMMENT ON",
			"VARCHAR2", "NUMBER", "CLOB", "BLOB", "TEXT",
			"SET IDENTITY_INSERT", "TRUNCATE",
		},
		Functions:   dmFunctions,
		Snippets:    dmSnippets,
		TypeGroups:  dmTypeGroups,
		TypeFormats: dmTypeFormats,

		DefaultColumnType:   "VARCHAR",
		DefaultColumnParams: "255",
		HasUnsigned:         false,
		AutoIncrement: dbdriver.UIAutoIncrement{
			Supported:   true,
			BaseTypes:   []string{"TINYINT", "SMALLINT", "INT", "INTEGER", "BIGINT"},
			MaxPerTable: 1,
		},
		PrimaryKeyForcesNotNull: true,
		// BTREE is the only method the DDL renderer emits — hide the selector.
		IndexTypes: nil,
		// Charset is fixed at instance init time, not a table-level knob.
		DefaultCharset: "",
	}
}

var dmTypeGroups = []dbdriver.UITypeGroup{
	{Key: "string", Types: []string{"VARCHAR", "CHAR", "TEXT"}},
	{Key: "integer", Types: []string{"INT", "BIGINT", "SMALLINT", "TINYINT"}},
	{Key: "decimal", Types: []string{"NUMERIC", "DOUBLE", "REAL", "FLOAT"}},
	{Key: "datetime", Types: []string{"TIMESTAMP", "DATETIME", "DATE", "TIME"}},
	{Key: "binary", Types: []string{"BLOB", "BINARY", "VARBINARY", "IMAGE"}},
	{Key: "boolean", Types: []string{"BIT"}},
	{Key: "other", Types: []string{"CLOB", "NUMBER", "VARCHAR2"}},
}

var dmTypeFormats = map[string]dbdriver.UITypeFormat{
	"VARCHAR":   {Kind: "length", ParamsRequired: true},
	"VARCHAR2":  {Kind: "length", ParamsRequired: true},
	"CHAR":      {Kind: "length"},
	"BINARY":    {Kind: "length"},
	"VARBINARY": {Kind: "length"},
	"NUMERIC":   {Kind: "precisionScale"},
	"NUMBER":    {Kind: "precisionScale"},
	"TIMESTAMP": {Kind: "fractionalSeconds"},
	"TIME":      {Kind: "fractionalSeconds"},
	"DATETIME":  {Kind: "fractionalSeconds"},
}

var dmSnippets = []dbdriver.UISnippet{
	{
		Label:  "createtable",
		Detail: "CREATE TABLE …",
		Body: "CREATE TABLE ${name} (\n  \"id\" BIGINT IDENTITY(1,1) PRIMARY KEY,\n" +
			"  ${cols}\n)${}",
	},
	{
		Label:  "merge",
		Detail: "MERGE INTO … (upsert)",
		Body: "MERGE INTO ${target} t USING ${source} s ON (${cond})\n" +
			"WHEN MATCHED THEN UPDATE SET ${assignments}\n" +
			"WHEN NOT MATCHED THEN INSERT (${cols}) VALUES (${values})${}",
	},
}

var dmFunctions = []dbdriver.UIFunction{
	{Name: "COUNT", Category: "aggregate", Info: "COUNT(expr) — number of non-NULL rows", Params: []string{"expr"}},
	{Name: "SUM", Category: "aggregate", Info: "SUM(expr) — sum of expr", Params: []string{"expr"}},
	{Name: "AVG", Category: "aggregate", Info: "AVG(expr) — average of expr", Params: []string{"expr"}},
	{Name: "MIN", Category: "aggregate", Info: "MIN(expr)", Params: []string{"expr"}},
	{Name: "MAX", Category: "aggregate", Info: "MAX(expr)", Params: []string{"expr"}},
	{Name: "LISTAGG", Category: "aggregate", Info: "LISTAGG(expr, delimiter) WITHIN GROUP (ORDER BY …)", Params: []string{"expr", "delimiter"}},
	{Name: "WM_CONCAT", Category: "aggregate", Info: "WM_CONCAT(expr) — comma-joined values", Params: []string{"expr"}},
	{Name: "CONCAT", Category: "string", Info: "CONCAT(str1, str2, …)", Params: []string{"str", "…"}},
	{Name: "SUBSTR", Category: "string", Info: "SUBSTR(str, pos [, len])", Params: []string{"str", "pos", "len"}},
	{Name: "LENGTH", Category: "string", Info: "LENGTH(str) — character length"},
	{Name: "TRIM", Category: "string"},
	{Name: "LTRIM", Category: "string"},
	{Name: "RTRIM", Category: "string"},
	{Name: "LOWER", Category: "string"},
	{Name: "UPPER", Category: "string"},
	{Name: "INITCAP", Category: "string", Info: "INITCAP(str) — capitalize each word"},
	{Name: "REPLACE", Category: "string", Info: "REPLACE(str, from, to)", Params: []string{"str", "from", "to"}},
	{Name: "INSTR", Category: "string", Info: "INSTR(str, substr) — position of substr", Params: []string{"str", "substr"}},
	{Name: "LPAD", Category: "string", Params: []string{"str", "len", "pad"}},
	{Name: "RPAD", Category: "string", Params: []string{"str", "len", "pad"}},
	{Name: "TO_CHAR", Category: "string", Info: "TO_CHAR(value, format)", Params: []string{"value", "format"}},
	{Name: "ROUND", Category: "numeric", Info: "ROUND(x[, d])", Params: []string{"x", "decimals"}},
	{Name: "FLOOR", Category: "numeric"},
	{Name: "CEIL", Category: "numeric"},
	{Name: "ABS", Category: "numeric"},
	{Name: "MOD", Category: "numeric", Params: []string{"n", "m"}},
	{Name: "POWER", Category: "numeric", Params: []string{"base", "exp"}},
	{Name: "TRUNC", Category: "numeric", Info: "TRUNC(x[, d])", Params: []string{"x", "decimals"}},
	{Name: "GREATEST", Category: "numeric"},
	{Name: "LEAST", Category: "numeric"},
	{Name: "SYSDATE", Category: "datetime", Info: "SYSDATE — current date and time", NoArgs: true},
	{Name: "NOW", Category: "datetime", Info: "NOW() — current timestamp", NoArgs: true},
	{Name: "CURRENT_DATE", Category: "datetime", NoArgs: true},
	{Name: "CURRENT_TIMESTAMP", Category: "datetime", NoArgs: true},
	{Name: "ADD_DAYS", Category: "datetime", Info: "ADD_DAYS(date, n)", Params: []string{"date", "n"}},
	{Name: "ADD_MONTHS", Category: "datetime", Info: "ADD_MONTHS(date, n)", Params: []string{"date", "n"}},
	{Name: "MONTHS_BETWEEN", Category: "datetime", Params: []string{"date1", "date2"}},
	{Name: "DATEDIFF", Category: "datetime", Info: "DATEDIFF(part, date1, date2)", Params: []string{"part", "date1", "date2"}},
	{Name: "EXTRACT", Category: "datetime", Info: "EXTRACT(field FROM source)", Params: []string{"field FROM source"}},
	{Name: "TO_DATE", Category: "datetime", Info: "TO_DATE(str, format)", Params: []string{"str", "format"}},
	{Name: "TO_TIMESTAMP", Category: "datetime", Info: "TO_TIMESTAMP(str, format)", Params: []string{"str", "format"}},
	{Name: "NVL", Category: "control", Info: "NVL(expr, fallback) — fallback if NULL", Params: []string{"expr", "fallback"}},
	{Name: "NVL2", Category: "control", Info: "NVL2(expr, notNull, isNull)", Params: []string{"expr", "notNull", "isNull"}},
	{Name: "NULLIF", Category: "control", Info: "NULLIF(a, b) — NULL if a=b", Params: []string{"a", "b"}},
	{Name: "COALESCE", Category: "control", Info: "COALESCE(a, b, …) — first non-NULL", Params: []string{"a", "b", "…"}},
	{Name: "DECODE", Category: "control", Info: "DECODE(expr, search1, result1, …, default)", Params: []string{"expr", "search", "result", "…"}},
	{Name: "CAST", Category: "cast", Info: "CAST(expr AS type)", Params: []string{"expr AS type"}},
	{Name: "TO_NUMBER", Category: "cast", Params: []string{"str"}},
	{Name: "TABLEDEF", Category: "system", Info: "TABLEDEF(schema, table) — CREATE TABLE text", Params: []string{"schema", "table"}},
	{Name: "IDENT_CURRENT", Category: "system", Info: "IDENT_CURRENT('schema.table') — current identity value", Params: []string{"table"}},
	{Name: "USER", Category: "system", NoArgs: true},
	{Name: "UUID", Category: "system", NoArgs: true},
	{Name: "SYS_CONTEXT", Category: "system", Info: "SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA')", Params: []string{"namespace", "parameter"}},
}
