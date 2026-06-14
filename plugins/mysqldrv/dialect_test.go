package mysqldrv

import (
	"testing"

	"catdb/internal/dbdriver"
)

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
	if !contains(dsn, "parseTime=true") {
		t.Errorf("dsn missing parseTime=true: %s", dsn)
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
