package mysqldrv

import (
	"testing"

	"catdb/internal/dbdriver"
	"catdb/internal/dbdriver/contract"
)

var _ dbdriver.ExternalCompat = Driver{}

// TestExternalCompatContract runs the shared static validation (no live DB).
func TestExternalCompatContract(t *testing.T) {
	contract.TestExternalCompat(t, Driver{})
}

func TestExternalDriverAliases(t *testing.T) {
	want := map[string]bool{"mysql": true, "mysql5": true, "mysql8": true}
	got := Driver{}.ExternalDriverAliases()
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
		{name: "full", url: "jdbc:mysql://db.example.com:3307/shop", host: "db.example.com", port: "3307", db: "shop"},
		{name: "default port", url: "jdbc:mysql://db.example.com/shop", host: "db.example.com", port: "3306", db: "shop"},
		{name: "default host", url: "jdbc:mysql:///shop", host: "127.0.0.1", port: "3306", db: "shop"},
		{name: "bare", url: "jdbc:mysql://", host: "127.0.0.1", port: "3306", db: ""},
		{name: "no database", url: "jdbc:mysql://h1:3306", host: "h1", port: "3306", db: ""},
		{name: "trailing slash", url: "jdbc:mysql://h1:3306/", host: "h1", port: "3306", db: ""},
		{
			name: "query", url: "jdbc:mysql://h1:3306/shop?connectTimeout=30000&useSSL=false",
			host: "h1", port: "3306", db: "shop",
			props: map[string]string{"connectTimeout": "30000", "useSSL": "false"},
		},
		{name: "userinfo dropped", url: "jdbc:mysql://root:s3cr3t@h1:3306/shop", host: "h1", port: "3306", db: "shop"},
		{name: "failover first host", url: "jdbc:mysql://h1:3306,h2:3306/shop", host: "h1", port: "3306", db: "shop"},
		{name: "ipv6", url: "jdbc:mysql://[::1]:3307/shop", host: "::1", port: "3307", db: "shop"},
		{name: "percent decoded db", url: "jdbc:mysql://h1/my%20shop", host: "h1", port: "3306", db: "my shop"},
		{name: "case insensitive scheme", url: "JDBC:MySQL://h1/shop", host: "h1", port: "3306", db: "shop"},
		{name: "wrong scheme", url: "jdbc:postgresql://h1/shop", wantErr: true},
		{name: "not a jdbc url", url: "mysql://h1/shop", wantErr: true},
		{name: "non-numeric port", url: "jdbc:mysql://h1:abc/shop", wantErr: true},
		{name: "empty", url: "", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Driver{}.ParseExternalURL(c.url)
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
		{key: "connectTimeout", value: "30000", dotKey: "params.timeout", val: "30s", ok: true},
		{key: "connectTimeout", value: "1500", dotKey: "params.timeout", val: "1.5s", ok: true},
		{key: "connectTimeout", value: "0", dotKey: "params.timeout", val: "0s", ok: true},
		{key: "connecttimeout", value: "5000", dotKey: "params.timeout", val: "5s", ok: true},
		{key: "connectTimeout", value: "abc"},
		{key: "connectTimeout", value: "-1"},
		{key: "socketTimeout", value: "60000", dotKey: "params.readTimeout", val: "1m0s", ok: true},
		{key: "sslMode", value: "DISABLED", dotKey: "ssl.mode", val: "disable", ok: true},
		{key: "sslMode", value: "preferred", dotKey: "ssl.mode", val: "prefer", ok: true},
		{key: "sslMode", value: "REQUIRED", dotKey: "ssl.mode", val: "require", ok: true},
		{key: "sslMode", value: "VERIFY_CA", dotKey: "ssl.mode", val: "verify-ca", ok: true},
		{key: "sslMode", value: "VERIFY_IDENTITY", dotKey: "ssl.mode", val: "verify-full", ok: true},
		{key: "sslMode", value: "NONSENSE"},
		{key: "useSSL", value: "false", dotKey: "ssl.mode", val: "disable", ok: true},
		{key: "useSSL", value: "true", dotKey: "ssl.mode", val: "prefer", ok: true},
		{key: "requireSSL", value: "true", dotKey: "ssl.mode", val: "require", ok: true},
		{key: "requireSSL", value: "false"},
		{key: "maxAllowedPacket", value: "16777216", dotKey: "params.maxAllowedPacket", val: "16777216", ok: true},
		{key: "maxAllowedPacket", value: "big"},
		{key: "connectionCollation", value: "utf8mb4_bin", dotKey: "params.collation", val: "utf8mb4_bin", ok: true},
		{key: "allowMultiQueries", value: "true"},
		{key: "", value: ""},
	}
	for _, c := range cases {
		t.Run(c.key+"="+c.value, func(t *testing.T) {
			dotKey, val, ok := contract.CheckExternalProp(t, Driver{}, c.key, c.value)
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
