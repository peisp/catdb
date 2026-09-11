package sqlitedrv

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
	want := map[string]bool{"sqlite": true, "sqlite_jdbc": true}
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
		name    string
		url     string
		db      string
		wantErr bool
	}{
		{name: "posix path", url: "jdbc:sqlite:/var/data/shop.db", db: "/var/data/shop.db"},
		{name: "relative path", url: "jdbc:sqlite:data/shop.db", db: "data/shop.db"},
		// A Windows path must survive verbatim — backslashes are not escapes
		// and the drive colon is not a separator.
		{name: "windows path", url: `jdbc:sqlite:C:\Temp\test.db`, db: `C:\Temp\test.db`},
		{name: "memory", url: "jdbc:sqlite::memory:", db: ":memory:"},
		{name: "case insensitive scheme", url: `JDBC:SQLite:C:\Temp\test.db`, db: `C:\Temp\test.db`},
		{name: "no path", url: "jdbc:sqlite:", wantErr: true},
		{name: "wrong scheme", url: "jdbc:mysql://h1/shop", wantErr: true},
		{name: "empty", url: "", wantErr: true},
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
			if got.Database != c.db {
				t.Errorf("database: got %q want %q", got.Database, c.db)
			}
			if got.Host != "" || got.Port != "" {
				t.Errorf("embedded database must have no host/port, got %q:%q", got.Host, got.Port)
			}
			if len(got.Props) != 0 {
				t.Errorf("props: got %v want empty", got.Props)
			}
		})
	}
}

func TestTranslateExternalProp(t *testing.T) {
	for _, k := range []string{"journal_mode", "busy_timeout", "open_mode", "foreign_keys", ""} {
		if dotKey, val, ok := (driver{}).TranslateExternalProp(k, "x"); ok {
			t.Errorf("%q: expected no translation, got %q=%q", k, dotKey, val)
		}
	}
}
