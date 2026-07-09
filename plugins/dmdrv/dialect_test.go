package dmdrv

import (
	"strings"
	"testing"

	"catdb/internal/dbdriver"
)

func TestQuoteIdentifier(t *testing.T) {
	d := dialect{}
	cases := map[string]string{
		"users":     `"users"`,
		`we"ird`:    `"we""ird"`,
		"UPPER":     `"UPPER"`,
		"mixedCase": `"mixedCase"`,
	}
	for in, want := range cases {
		if got := d.QuoteIdentifier(in); got != want {
			t.Errorf("QuoteIdentifier(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultNamespaceSQL(t *testing.T) {
	d := dialect{}
	if got := d.DefaultNamespaceSQL("SALES"); got != `SET SCHEMA "SALES"` {
		t.Errorf("DefaultNamespaceSQL = %q", got)
	}
	if got := d.DefaultNamespaceSQL("  "); got != "" {
		t.Errorf("DefaultNamespaceSQL(blank) = %q, want empty", got)
	}
}

func TestPaginate(t *testing.T) {
	d := dialect{}
	if got := d.Paginate("SELECT * FROM t", 100, 200); got != "SELECT * FROM t LIMIT 100 OFFSET 200" {
		t.Errorf("Paginate = %q", got)
	}
	if got := d.Paginate("SELECT * FROM t", 0, 10); got != "SELECT * FROM t" {
		t.Errorf("Paginate(limit=0) = %q, want passthrough", got)
	}
	if got := d.Paginate("SELECT * FROM t", 10, -5); got != "SELECT * FROM t LIMIT 10 OFFSET 0" {
		t.Errorf("Paginate(negative offset) = %q", got)
	}
}

func TestPlaceholder(t *testing.T) {
	d := dialect{}
	if got := d.Placeholder(3); got != "?" {
		t.Errorf("Placeholder(3) = %q, want ?", got)
	}
}

func TestMapType(t *testing.T) {
	d := dialect{}
	cases := map[string]dbdriver.LogicalType{
		"INT":                      dbdriver.TypeInt,
		"integer":                  dbdriver.TypeInt,
		"BIGINT":                   dbdriver.TypeBigInt,
		"NUMBER":                   dbdriver.TypeDecimal,
		"NUMERIC(10,2)":            dbdriver.TypeDecimal,
		"DOUBLE":                   dbdriver.TypeFloat,
		"BIT":                      dbdriver.TypeBool,
		"VARCHAR(64)":              dbdriver.TypeString,
		"VARCHAR2":                 dbdriver.TypeString,
		"TEXT":                     dbdriver.TypeText,
		"CLOB":                     dbdriver.TypeText,
		"BLOB":                     dbdriver.TypeBytes,
		"DATE":                     dbdriver.TypeDate,
		"TIME":                     dbdriver.TypeTime,
		"DATETIME":                 dbdriver.TypeDateTime,
		"TIMESTAMP":                dbdriver.TypeTimestamp,
		"TIMESTAMP(6)":             dbdriver.TypeTimestamp,
		"TIME WITH TIME ZONE":      dbdriver.TypeTime,
		"TIMESTAMP WITH TIME ZONE": dbdriver.TypeTimestamp,
		"GEOMETRY":                 dbdriver.TypeUnknown,
	}
	for in, want := range cases {
		if got := d.MapType(in); got != want {
			t.Errorf("MapType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeType(t *testing.T) {
	d := dialect{}
	cases := map[string]string{
		"varchar(64)":            "VARCHAR(64)",
		"VARCHAR2(64)":           "VARCHAR(64)",
		"NUMBER(10, 2)":          "NUMERIC(10,2)",
		"DECIMAL(10,2)":          "NUMERIC(10,2)",
		"dec(5)":                 "NUMERIC(5)",
		"INTEGER":                "INT",
		"int":                    "INT",
		"DOUBLE PRECISION":       "DOUBLE",
		"BOOLEAN":                "BIT",
		"CLOB":                   "TEXT",
		"TIMESTAMP(6)":           "TIMESTAMP(6)",
		"  timestamp(3)  ":       "TIMESTAMP(3)",
		"CHARACTER(2)":           "CHAR(2)",
		"TIME(3) WITH TIME ZONE": "TIME(3) WITH TIME ZONE",
	}
	for in, want := range cases {
		got := d.NormalizeType(in)
		if got != want {
			t.Errorf("NormalizeType(%q) = %q, want %q", in, got, want)
		}
		if twice := d.NormalizeType(got); twice != got {
			t.Errorf("NormalizeType not idempotent: %q → %q → %q", in, got, twice)
		}
	}
}

func TestComposeType(t *testing.T) {
	cases := []struct {
		dataType                 string
		length, precision, scale int64
		want                     string
	}{
		{"VARCHAR", 64, 0, 0, "VARCHAR(64)"},
		{"CHAR", 10, 0, 0, "CHAR(10)"},
		{"NUMBER", 0, 10, 2, "NUMBER(10,2)"},
		{"NUMERIC", 0, 5, 0, "NUMERIC(5)"},
		{"INT", 4, 10, 0, "INT"},
		{"TIMESTAMP(6)", 0, 0, 6, "TIMESTAMP(6)"},
		{"TEXT", 2147483647, 0, 0, "TEXT"},
	}
	for _, c := range cases {
		if got := composeType(c.dataType, c.length, c.precision, c.scale); got != c.want {
			t.Errorf("composeType(%q,%d,%d,%d) = %q, want %q", c.dataType, c.length, c.precision, c.scale, got, c.want)
		}
	}
}

func TestScriptRules(t *testing.T) {
	r := dialect{}.ScriptRules()
	if r.BacktickIdentifiers || r.BackslashEscapes || r.HashComments || r.ClientDelimiter || r.DollarQuoting {
		t.Errorf("ScriptRules should be all-ANSI (zero value), got %+v", r)
	}
}

func TestQualifyTable(t *testing.T) {
	got := dbdriver.QualifyTable(dialect{}, "SALES", "", "orders")
	if got != `"SALES"."orders"` {
		t.Errorf("QualifyTable = %q", got)
	}
}

func TestFormatDefaultExpr(t *testing.T) {
	cases := map[string]string{
		"":                  "''",
		"hello":             "'hello'",
		"it's":              "'it''s'",
		"42":                "42",
		"-3.14":             "-3.14",
		"CURRENT_TIMESTAMP": "CURRENT_TIMESTAMP",
		"SYSDATE":           "SYSDATE",
		"NOW()":             "NOW()",
		"'quoted'":          "'quoted'",
	}
	for in, want := range cases {
		if got := formatDefaultExpr(in); got != want {
			t.Errorf("formatDefaultExpr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeTypeEmpty(t *testing.T) {
	if got := (dialect{}).NormalizeType("   "); got != "" {
		t.Errorf("NormalizeType(blank) = %q, want empty", got)
	}
}

func TestMapTypeStripsParams(t *testing.T) {
	// The resultset feeds DatabaseTypeName values that may carry params.
	if got := (dialect{}).MapType("NUMBER(10,2)"); got != dbdriver.TypeDecimal {
		t.Errorf("MapType(NUMBER(10,2)) = %q", got)
	}
	if !strings.EqualFold(string((dialect{}).MapType("varchar2(32)")), string(dbdriver.TypeString)) {
		t.Error("MapType(varchar2(32)) should be string")
	}
}
