package postgresdrv

import (
	"strings"
	"testing"

	"catdb/internal/core/scanner"
	"catdb/internal/dbdriver"
	"catdb/internal/dbdriver/contract"
)

// TestUIDialectDescriptor runs the shared static validation (no live DB) so
// descriptor mistakes surface in plain unit tests, not just integration runs.
func TestUIDialectDescriptor(t *testing.T) {
	contract.TestUIDialect(t, driver{})
}

func TestQuoteIdentifier(t *testing.T) {
	d := dialect{}
	cases := []struct{ in, want string }{
		{"users", `"users"`},
		{`weird"name`, `"weird""name"`},
		{"", `""`},
	}
	for _, c := range cases {
		if got := d.QuoteIdentifier(c.in); got != c.want {
			t.Errorf("QuoteIdentifier(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPaginate(t *testing.T) {
	d := dialect{}
	if got := d.Paginate("SELECT * FROM t", 10, 20); got != "SELECT * FROM t LIMIT 10 OFFSET 20" {
		t.Errorf("Paginate: got %q", got)
	}
	if got := d.Paginate("SELECT 1", 0, 0); got != "SELECT 1" {
		t.Errorf("zero limit should leave SQL untouched: got %q", got)
	}
	if got := d.Paginate("SELECT 1", 5, -10); got != "SELECT 1 LIMIT 5 OFFSET 0" {
		t.Errorf("negative offset should clamp to 0: got %q", got)
	}
}

func TestDefaultNamespaceSQL(t *testing.T) {
	d := dialect{}
	if got := d.DefaultNamespaceSQL("app"); got != `SET search_path TO "app"` {
		t.Errorf("DefaultNamespaceSQL: got %q", got)
	}
	if got := d.DefaultNamespaceSQL("  "); got != "" {
		t.Errorf("blank name should yield empty statement, got %q", got)
	}
}

func TestScriptRules(t *testing.T) {
	r := dialect{}.ScriptRules()
	if !r.DollarQuoting {
		t.Error("Postgres scripts need DollarQuoting")
	}
	if r.BacktickIdentifiers || r.BackslashEscapes || r.HashComments || r.ClientDelimiter {
		t.Errorf("MySQL-only lexical rules must be off: %+v", r)
	}
}

func TestNormalizeType(t *testing.T) {
	d := dialect{}
	cases := map[string]string{
		"character varying(64)":       "VARCHAR(64)",
		"varchar(64)":                 "VARCHAR(64)",
		"VARCHAR(64)":                 "VARCHAR(64)",
		"character(3)":                "CHAR(3)",
		"integer":                     "INTEGER",
		"int":                         "INTEGER",
		"int4":                        "INTEGER",
		"serial":                      "INTEGER",
		"bigserial":                   "BIGINT",
		"int8":                        "BIGINT",
		"numeric(10, 2)":              "NUMERIC(10,2)",
		"decimal(10,2)":               "NUMERIC(10,2)",
		"double precision":            "DOUBLE PRECISION",
		"float8":                      "DOUBLE PRECISION",
		"real":                        "REAL",
		"boolean":                     "BOOLEAN",
		"bool":                        "BOOLEAN",
		"timestamp without time zone": "TIMESTAMP",
		"timestamp with time zone":    "TIMESTAMPTZ",
		"timestamp(3) with time zone": "TIMESTAMPTZ(3)",
		"timestamptz":                 "TIMESTAMPTZ",
		"time with time zone":         "TIMETZ",
		"time without time zone":      "TIME",
		"text":                        "TEXT",
		"integer[]":                   "INTEGER[]",
		"character varying(64)[]":     "VARCHAR(64)[]",
		"uuid":                        "UUID",
		"":                            "",
	}
	for in, want := range cases {
		got := d.NormalizeType(in)
		if got != want {
			t.Errorf("NormalizeType(%q) = %q, want %q", in, got, want)
		}
		if again := d.NormalizeType(got); again != got {
			t.Errorf("NormalizeType not idempotent: %q → %q → %q", in, got, again)
		}
	}
}

func TestMapType(t *testing.T) {
	d := dialect{}
	cases := map[string]dbdriver.LogicalType{
		"int4":                        dbdriver.TypeInt,
		"integer":                     dbdriver.TypeInt,
		"int8":                        dbdriver.TypeBigInt,
		"varchar(64)":                 dbdriver.TypeString,
		"character varying":           dbdriver.TypeString,
		"text":                        dbdriver.TypeText,
		"numeric(10,2)":               dbdriver.TypeDecimal,
		"float8":                      dbdriver.TypeFloat,
		"double precision":            dbdriver.TypeFloat,
		"bool":                        dbdriver.TypeBool,
		"bytea":                       dbdriver.TypeBytes,
		"jsonb":                       dbdriver.TypeJSON,
		"date":                        dbdriver.TypeDate,
		"timestamp without time zone": dbdriver.TypeDateTime,
		"timestamptz":                 dbdriver.TypeTimestamp,
		"timestamp(3) with time zone": dbdriver.TypeTimestamp,
		"uuid":                        dbdriver.TypeUUID,
		"integer[]":                   dbdriver.TypeString,
		"someenum":                    dbdriver.TypeUnknown,
	}
	for in, want := range cases {
		if got := d.MapType(in); got != want {
			t.Errorf("MapType(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBuildPoolConfig(t *testing.T) {
	cfg := dbdriver.ConnConfig{
		Host:     "db.internal",
		Port:     5433,
		User:     "svc user", // space → must survive DSN quoting
		Password: "p'w\\d",   // quote + backslash → must survive DSN quoting
		Database: "app",
		Params:   map[string]string{"timeout": "10s"},
	}
	pc, err := buildPoolConfig(cfg)
	if err != nil {
		t.Fatalf("buildPoolConfig: %v", err)
	}
	cc := pc.ConnConfig
	if cc.Host != "db.internal" || cc.Port != 5433 {
		t.Errorf("host/port = %s:%d", cc.Host, cc.Port)
	}
	if cc.User != "svc user" {
		t.Errorf("user = %q", cc.User)
	}
	if cc.Password != "p'w\\d" {
		t.Errorf("password = %q", cc.Password)
	}
	if cc.Database != "app" {
		t.Errorf("database = %q", cc.Database)
	}
	if cc.ConnectTimeout.Seconds() != 10 {
		t.Errorf("connect timeout = %v", cc.ConnectTimeout)
	}
	if cc.TLSConfig != nil {
		t.Error("TLS must be off by default")
	}
	if pc.MaxConns != 10 {
		t.Errorf("MaxConns = %d", pc.MaxConns)
	}
}

func TestBuildPoolConfigDefaults(t *testing.T) {
	pc, err := buildPoolConfig(dbdriver.ConnConfig{Host: "h", User: "u"})
	if err != nil {
		t.Fatalf("buildPoolConfig: %v", err)
	}
	if pc.ConnConfig.Port != 5432 {
		t.Errorf("default port = %d", pc.ConnConfig.Port)
	}
	if pc.ConnConfig.Database != "postgres" {
		t.Errorf("default database = %q", pc.ConnConfig.Database)
	}
}

func TestBuildPoolConfigTLS(t *testing.T) {
	pc, err := buildPoolConfig(dbdriver.ConnConfig{
		Host: "h", User: "u",
		SSL: &dbdriver.SSLConfig{Mode: "require"},
	})
	if err != nil {
		t.Fatalf("buildPoolConfig: %v", err)
	}
	if pc.ConnConfig.TLSConfig == nil || !pc.ConnConfig.TLSConfig.InsecureSkipVerify {
		t.Error("require must yield an unverified TLS config")
	}

	pc, err = buildPoolConfig(dbdriver.ConnConfig{
		Host: "h", User: "u",
		SSL: &dbdriver.SSLConfig{Mode: "prefer"},
	})
	if err != nil {
		t.Fatalf("buildPoolConfig: %v", err)
	}
	if pc.ConnConfig.TLSConfig == nil {
		t.Fatal("prefer must attempt TLS")
	}
	hasPlain := false
	for _, fb := range pc.ConnConfig.Fallbacks {
		if fb.TLSConfig == nil {
			hasPlain = true
		}
	}
	if !hasPlain {
		t.Error("prefer must keep a plaintext fallback")
	}

	pc, err = buildPoolConfig(dbdriver.ConnConfig{
		Host: "h", User: "u",
		SSL: &dbdriver.SSLConfig{Mode: "verify-full"},
	})
	if err != nil {
		t.Fatalf("buildPoolConfig: %v", err)
	}
	if got := pc.ConnConfig.TLSConfig.ServerName; got != "h" {
		t.Errorf("verify-full ServerName should default to host, got %q", got)
	}

	if _, err = buildPoolConfig(dbdriver.ConnConfig{
		Host: "h", User: "u",
		SSL: &dbdriver.SSLConfig{Mode: "bogus"},
	}); err == nil || !strings.Contains(err.Error(), "ssl.mode") {
		t.Errorf("unknown ssl.mode must error, got %v", err)
	}
}

func TestConvertText(t *testing.T) {
	cases := []struct {
		typ  string
		raw  string
		want any
	}{
		{"int4", "42", int64(42)},
		{"int8", "9007199254740993", nil}, // out of JS safe range → BigIntString (checked below)
		{"float8", "1.5", 1.5},
		{"numeric", "12.340", "12.340"},
		{"bool", "t", true},
		{"bool", "f", false},
		{"text", "hello", "hello"},
		{"timestamp", "2026-01-02 03:04:05", "2026-01-02 03:04:05"},
	}
	for _, c := range cases {
		got := convertText([]byte(c.raw), c.typ)
		if c.want == nil {
			continue
		}
		if got != c.want {
			t.Errorf("convertText(%q, %s) = %#v, want %#v", c.raw, c.typ, got, c.want)
		}
	}
	if got := convertText(nil, "text"); got != nil {
		t.Errorf("NULL must convert to nil, got %#v", got)
	}
	big := convertText([]byte("9007199254740993"), "int8")
	if bs, ok := big.(scanner.BigIntString); !ok || bs.Value != "9007199254740993" {
		t.Errorf("int8 beyond the JS safe range must ride as BigIntString, got %#v", big)
	}
	small := convertText([]byte("7"), "int8")
	if v, ok := small.(int64); !ok || v != 7 {
		t.Errorf("small int8 must ride as int64, got %#v", small)
	}
	bytes := convertText([]byte(`\x68656c6c6f`), "bytea")
	bv, ok := bytes.(scanner.BytesValue)
	if !ok || bv.Length != 5 {
		t.Errorf("bytea must decode to a 5-byte BytesValue, got %#v", bytes)
	}
}

func TestPlaceholder(t *testing.T) {
	d := dialect{}
	if got := d.Placeholder(1); got != "$1" {
		t.Errorf("Placeholder(1) = %q, want $1", got)
	}
	if got := d.Placeholder(37); got != "$37" {
		t.Errorf("Placeholder(37) = %q, want $37", got)
	}
}

func TestTruncateTableSQL(t *testing.T) {
	d := dialect{}
	if got := d.TruncateTableSQL(`"t"`); got != `TRUNCATE TABLE "t"` {
		t.Errorf("TruncateTableSQL = %q", got)
	}
}

func TestReplaceViewSQL(t *testing.T) {
	d := dialect{}
	got := d.ReplaceViewSQL(`"v"`, "SELECT 1")
	want := []string{`DROP VIEW IF EXISTS "v";`, `CREATE VIEW "v" AS SELECT 1;`}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ReplaceViewSQL = %v, want %v", got, want)
	}
}
