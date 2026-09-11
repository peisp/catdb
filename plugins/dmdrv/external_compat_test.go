package dmdrv

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
	want := map[string]bool{"dm": true, "dameng": true}
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
		{name: "full", url: "jdbc:dm://db.example.com:5237", host: "db.example.com", port: "5237"},
		{name: "default port", url: "jdbc:dm://db.example.com", host: "db.example.com", port: "5236"},
		{name: "all defaults", url: "jdbc:dm://", host: "127.0.0.1", port: "5236"},
		{name: "schema path", url: "jdbc:dm://h1:5236/SYSDBA", host: "h1", port: "5236", db: "SYSDBA"},
		{
			name: "query", url: "jdbc:dm://h1:5236?connectTimeout=15000",
			host: "h1", port: "5236",
			props: map[string]string{"connectTimeout": "15000"},
		},
		{name: "wrong scheme", url: "jdbc:mysql://h1", wantErr: true},
		{name: "non-numeric port", url: "jdbc:dm://h1:dm", wantErr: true},
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
		{key: "connectTimeout", value: "15000", dotKey: "params.timeout", val: "15s", ok: true},
		{key: "loginTimeout", value: "500", dotKey: "params.timeout", val: "500ms", ok: true},
		{key: "connectTimeout", value: "soon"},
		{key: "schema", value: "SHOP", dotKey: "database", val: "SHOP", ok: true},
		{key: "user", value: "SYSDBA", dotKey: "user", val: "SYSDBA", ok: true},
		{key: "userName", value: "SYSDBA", dotKey: "user", val: "SYSDBA", ok: true},
		{key: "password", value: "p@ss", dotKey: "password", val: "p@ss", ok: true},
		{key: "compatibleMode", value: "oracle"},
		{key: "sslMode", value: "REQUIRED"},
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
