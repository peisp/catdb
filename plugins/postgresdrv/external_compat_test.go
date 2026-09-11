package postgresdrv

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
	want := map[string]bool{"postgresql": true, "postgres": true, "postgres-jdbc": true}
	got := driver{}.ExternalDriverAliases()
	if len(got) != len(want) {
		t.Fatalf("aliases: got %v", got)
	}
	for _, a := range got {
		if !want[a] {
			t.Errorf("unexpected alias %q", a)
		}
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
		{name: "full", url: "jdbc:postgresql://db.example.com:5433/shop", host: "db.example.com", port: "5433", db: "shop"},
		{name: "default port", url: "jdbc:postgresql://db.example.com/shop", host: "db.example.com", port: "5432", db: "shop"},
		// No database in the URL → the driver's own default database.
		{name: "default database", url: "jdbc:postgresql://h1:5432", host: "h1", port: "5432", db: "postgres"},
		{name: "all defaults", url: "jdbc:postgresql://", host: "127.0.0.1", port: "5432", db: "postgres"},
		{
			name: "query", url: "jdbc:postgresql://h1/shop?connectTimeout=10&sslmode=require",
			host: "h1", port: "5432", db: "shop",
			props: map[string]string{"connectTimeout": "10", "sslmode": "require"},
		},
		{name: "ipv6", url: "jdbc:postgresql://[fe80::1]:5433/shop", host: "fe80::1", port: "5433", db: "shop"},
		{name: "wrong scheme", url: "jdbc:mysql://h1/shop", wantErr: true},
		{name: "non-numeric port", url: "jdbc:postgresql://h1:pg/shop", wantErr: true},
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
			if len(got.Props) != len(c.props) {
				t.Fatalf("props: got %v want %v", got.Props, c.props)
			}
			for k, v := range c.props {
				if got.Props[k] != v {
					t.Errorf("prop %q: got %q want %q", k, got.Props[k], v)
				}
			}
		})
	}
}

func TestTranslateExternalProp(t *testing.T) {
	cases := []struct {
		key, value string
		dotKey     string
		val        string
		ok         bool
	}{
		// pgJDBC's connectTimeout is in SECONDS — the unit trap versus MySQL.
		{key: "connectTimeout", value: "10", dotKey: "params.timeout", val: "10s", ok: true},
		{key: "connectTimeout", value: "0", dotKey: "params.timeout", val: "0s", ok: true},
		{key: "connectTimeout", value: "90", dotKey: "params.timeout", val: "1m30s", ok: true},
		{key: "connectTimeout", value: "ten"},
		{key: "connectTimeout", value: "-5"},
		{key: "sslmode", value: "require", dotKey: "ssl.mode", val: "require", ok: true},
		{key: "sslMode", value: "VERIFY-FULL", dotKey: "ssl.mode", val: "verify-full", ok: true},
		{key: "sslmode", value: "allow", dotKey: "ssl.mode", val: "prefer", ok: true},
		{key: "sslmode", value: "maybe"},
		{key: "ssl", value: "true", dotKey: "ssl.mode", val: "require", ok: true},
		{key: "ssl", value: "false"},
		{key: "sslrootcert", value: "/etc/ssl/root.crt", dotKey: "ssl.caCert", val: "/etc/ssl/root.crt", ok: true},
		{key: "sslcert", value: "/etc/ssl/client.crt", dotKey: "ssl.clientCert", val: "/etc/ssl/client.crt", ok: true},
		{key: "sslkey", value: "/etc/ssl/client.key", dotKey: "ssl.clientKey", val: "/etc/ssl/client.key", ok: true},
		{key: "ApplicationName", value: "catdb"},
	}
	for _, c := range cases {
		t.Run(c.key+"="+c.value, func(t *testing.T) {
			dotKey, val, ok := contract.CheckExternalProp(t, driver{}, c.key, c.value)
			if ok != c.ok {
				t.Fatalf("ok: got %v want %v (%q/%q)", ok, c.ok, dotKey, val)
			}
			if !ok {
				return
			}
			if dotKey != c.dotKey || val != c.val {
				t.Errorf("got %q=%q, want %q=%q", dotKey, val, c.dotKey, c.val)
			}
		})
	}
}
