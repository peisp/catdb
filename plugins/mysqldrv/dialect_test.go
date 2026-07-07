package mysqldrv

import (
	"testing"

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
		{"users", "`users`"},
		{"weird`name", "`weird``name`"},
		{"", "``"},
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

func TestNormalizeType(t *testing.T) {
	d := dialect{}
	cases := map[string]string{
		"varchar(255)":           "VARCHAR(255)",
		"decimal(10, 2)":         "DECIMAL(10,2)",
		"int(10) unsigned":       "INT(10) UNSIGNED",
		"int unsigned zerofill":  "INT UNSIGNED",
		"enum('a','b')":          "ENUM('a','b')",
		"datetime(6)":            "DATETIME(6)",
		"text":                   "TEXT",
		"":                       "",
		"DECIMAL(10,2) UNSIGNED": "DECIMAL(10,2) UNSIGNED",
	}
	for in, want := range cases {
		if got := d.NormalizeType(in); got != want {
			t.Errorf("NormalizeType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultNamespaceSQL(t *testing.T) {
	d := dialect{}
	if got := d.DefaultNamespaceSQL("app"); got != "USE `app`" {
		t.Errorf("DefaultNamespaceSQL: got %q", got)
	}
	if got := d.DefaultNamespaceSQL("  "); got != "" {
		t.Errorf("blank name should yield empty statement, got %q", got)
	}
}

func TestMapType(t *testing.T) {
	d := dialect{}
	cases := map[string]dbdriver.LogicalType{
		"INT":         dbdriver.TypeInt,
		"BIGINT":      dbdriver.TypeBigInt,
		"VARCHAR(64)": dbdriver.TypeString,
		"TEXT":        dbdriver.TypeText,
		"DATETIME(6)": dbdriver.TypeDateTime,
		"JSON":        dbdriver.TypeJSON,
		"BLOB":        dbdriver.TypeBytes,
		"DECIMAL(10,2)": dbdriver.TypeDecimal,
		"int unsigned":  dbdriver.TypeInt,
		"random":      dbdriver.TypeUnknown,
	}
	for in, want := range cases {
		if got := d.MapType(in); got != want {
			t.Errorf("MapType(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBuildDSNDefaults(t *testing.T) {
	cfg := dbdriver.ConnConfig{
		Host: "127.0.0.1",
		Port: 3306,
		User: "root",
		Password: "secret",
		Database: "mydb",
		Params: map[string]string{"timeout": "10s"},
	}
	dsn := buildDSN(cfg, "tcp", "")
	// Format: user:pass@tcp(host:port)/db?...
	if want := "root:secret@tcp(127.0.0.1:3306)/mydb"; !contains(dsn, want) {
		t.Errorf("dsn missing %q: %s", want, dsn)
	}
	// ParseTime 必须保持关闭：scanner 依赖原样字节串自行解析时间类型（见 dsn.go）。
	if contains(dsn, "parseTime=true") {
		t.Errorf("dsn must not enable parseTime (scanner parses raw time strings): %s", dsn)
	}
	if !contains(dsn, "collation=utf8mb4_general_ci") {
		t.Errorf("dsn missing default collation: %s", dsn)
	}
	if !contains(dsn, "timeout=10s") {
		t.Errorf("dsn missing custom timeout: %s", dsn)
	}
}

func TestBuildDSNWithTLS(t *testing.T) {
	cfg := dbdriver.ConnConfig{Host: "h", User: "u"}
	dsn := buildDSN(cfg, "tcp", "skip-verify")
	if !contains(dsn, "tls=skip-verify") {
		t.Errorf("dsn missing tls= reference: %s", dsn)
	}
}

func TestBuildDSNWithSSHNetwork(t *testing.T) {
	cfg := dbdriver.ConnConfig{Host: "h", User: "u"}
	dsn := buildDSN(cfg, "tcp+ssh-deadbeef", "")
	if !contains(dsn, "@tcp+ssh-deadbeef(") {
		t.Errorf("dsn missing custom network: %s", dsn)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
