package mariadbdrv

import (
	"testing"

	"catdb/internal/dbdriver"
	"catdb/internal/dbdriver/contract"
)

var _ dbdriver.ExternalCompat = driver{}

// TestExternalCompatContract runs the shared static validation (no live DB).
func TestExternalCompatContract(t *testing.T) {
	contract.TestExternalCompat(t, driver{})
}

func TestExternalDriverAliases(t *testing.T) {
	got := driver{}.ExternalDriverAliases()
	if len(got) != 1 || got[0] != "mariadb" {
		t.Fatalf("aliases: got %v want [mariadb]", got)
	}
}

func TestParseExternalURL(t *testing.T) {
	cases := []struct {
		name           string
		url            string
		host, port, db string
		props          map[string]string
		wantErr        bool
	}{
		{name: "full", url: "jdbc:mariadb://db.example.com:3307/shop", host: "db.example.com", port: "3307", db: "shop"},
		{name: "defaults", url: "jdbc:mariadb://db.example.com", host: "db.example.com", port: "3306"},
		{
			name: "query", url: "jdbc:mariadb://h1/shop?connectTimeout=30000",
			host: "h1", port: "3306", db: "shop",
			props: map[string]string{"connectTimeout": "30000"},
		},
		// The MySQL scheme is NOT accepted: DBeaver addresses each driver with
		// its own scheme, and mysqldrv already claims jdbc:mysql://.
		{name: "mysql scheme rejected", url: "jdbc:mysql://h1/shop", wantErr: true},
		{name: "non-numeric port", url: "jdbc:mariadb://h1:x/shop", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := driver{}.ParseExternalURL(c.url)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got.Host != c.host || got.Port != c.port || got.Database != c.db {
				t.Errorf("got host=%q port=%q db=%q, want %q/%q/%q", got.Host, got.Port, got.Database, c.host, c.port, c.db)
			}
			for k, v := range c.props {
				if got.Props[k] != v {
					t.Errorf("prop %q: got %q want %q", k, got.Props[k], v)
				}
			}
		})
	}
}

// TestTranslateExternalPropInherited pins the reuse: MariaDB must answer with
// the MySQL translation table, not with a divergent copy.
func TestTranslateExternalPropInherited(t *testing.T) {
	cases := []struct{ key, value, dotKey, val string }{
		{"connectTimeout", "30000", "params.timeout", "30s"},
		{"socketTimeout", "60000", "params.readTimeout", "1m0s"},
		{"sslMode", "REQUIRED", "ssl.mode", "require"},
		{"useSSL", "false", "ssl.mode", "disable"},
		{"connectionCollation", "utf8mb4_bin", "params.collation", "utf8mb4_bin"},
	}
	for _, c := range cases {
		dotKey, val, ok := contract.CheckExternalProp(t, driver{}, c.key, c.value)
		if !ok || dotKey != c.dotKey || val != c.val {
			t.Errorf("%s=%s: got %q=%q ok=%v, want %q=%q", c.key, c.value, dotKey, val, ok, c.dotKey, c.val)
		}
	}
	if _, _, ok := (driver{}).TranslateExternalProp("allowMultiQueries", "true"); ok {
		t.Error("allowMultiQueries should be untranslatable")
	}
}
