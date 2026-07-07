package dmdrv

import (
	"strings"
	"testing"

	"catdb/internal/dbdriver"
	"catdb/internal/dbdriver/contract"
)

func TestDriverStatics(t *testing.T) {
	d := driver{}
	if d.Name() != "dm" {
		t.Errorf("Name = %q", d.Name())
	}
	caps := d.Capabilities()
	if caps.Schemas {
		t.Error("Schemas must be false — the schema level is collapsed into the database position")
	}
	if !caps.Transactions || !caps.Views || !caps.ExplainPlan {
		t.Errorf("unexpected capabilities: %+v", caps)
	}
}

func TestUIDialectStatic(t *testing.T) {
	contract.TestUIDialect(t, driver{})
}

func TestConnectionSchema(t *testing.T) {
	fields := driver{}.ConnectionSchema()
	byKey := map[string]dbdriver.ConnParamField{}
	for _, f := range fields {
		byKey[f.Key] = f
	}
	for _, key := range []string{"host", "port", "user", "password", "database", "params.timeout", "sshTunnel.host"} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("ConnectionSchema missing %q", key)
		}
	}
	if byKey["port"].Default != "5236" {
		t.Errorf("port default = %q, want 5236", byKey["port"].Default)
	}
	for _, f := range fields {
		if f.Group == "" {
			t.Errorf("field %q has no group", f.Key)
		}
	}
}

func TestBuildDSN(t *testing.T) {
	dsn, err := buildDSN(dbdriver.ConnConfig{
		Host: "db.internal", Port: 5237, User: "SYSDBA", Password: "secret",
		Database: "SALES", Params: map[string]string{"timeout": "30s"},
	}, "")
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	if !strings.HasPrefix(dsn, "dm://SYSDBA:secret@db.internal:5237?") {
		t.Errorf("dsn = %q", dsn)
	}
	for _, want := range []string{"connectTimeout=30000", "schema=SALES", "appName=catdb"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("dsn missing %q: %q", want, dsn)
		}
	}
}

func TestBuildDSNDefaults(t *testing.T) {
	dsn, err := buildDSN(dbdriver.ConnConfig{User: "SYSDBA"}, "tunnel1")
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	for _, want := range []string{"dm://SYSDBA@127.0.0.1:5236?", "connectTimeout=15000", "dialName=tunnel1"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("dsn missing %q: %q", want, dsn)
		}
	}
	if strings.Contains(dsn, "schema=") {
		t.Errorf("blank database must not emit schema prop: %q", dsn)
	}
}

// The dm driver's DSN parser does no unescaping — characters that would
// break its last-'?' / last-'@' / first-':' splits must be rejected with a
// clear error instead of a confusing connect failure.
func TestBuildDSNRejectsUnparsableCredentials(t *testing.T) {
	if _, err := buildDSN(dbdriver.ConnConfig{User: "SYSDBA", Password: "p@ss"}, ""); err == nil {
		t.Error("password with '@' must be rejected")
	}
	if _, err := buildDSN(dbdriver.ConnConfig{User: "SYSDBA", Password: "wh?y"}, ""); err == nil {
		t.Error("password with '?' must be rejected")
	}
	if _, err := buildDSN(dbdriver.ConnConfig{User: "a:b", Password: "x"}, ""); err == nil {
		t.Error("user with ':' must be rejected")
	}
	// ':' and '&' survive the split rules and are allowed in passwords.
	if _, err := buildDSN(dbdriver.ConnConfig{User: "SYSDBA", Password: "a:b&c"}, ""); err != nil {
		t.Errorf("password with ':'/'&' should pass: %v", err)
	}
}
