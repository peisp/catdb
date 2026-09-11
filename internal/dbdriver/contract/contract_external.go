package contract

import (
	"strings"
	"testing"

	"catdb/internal/dbdriver"
)

// The helpers in this file validate the OPTIONAL dbdriver.ExternalCompat
// extension without a live database, the same way TestUIDialect validates the
// UI descriptor. A driver that implements the extension calls them from its
// unit tests so the CLI compatibility layer can trust two invariants it has no
// way to check at runtime: aliases are matchable, and every translated key is
// a key this driver actually has.

// coreConnectionKeys are the dotted keys that exist for every driver — they
// come from ConnectionDraft itself, not from ConnectionSchema.
var coreConnectionKeys = map[string]bool{
	"host": true, "port": true, "user": true, "password": true, "database": true,
}

// TestExternalCompat runs the static checks for a driver's ExternalCompat
// implementation: alias hygiene, rejection of foreign URLs, and rejection of
// properties it does not know.
func TestExternalCompat(t *testing.T, d dbdriver.Driver) {
	t.Helper()
	ec, ok := d.(dbdriver.ExternalCompat)
	if !ok {
		t.Fatalf("driver %q does not implement dbdriver.ExternalCompat", d.Name())
	}

	aliases := ec.ExternalDriverAliases()
	if len(aliases) == 0 {
		t.Error("ExternalDriverAliases must not be empty — an alias table is the whole point of the extension")
	}
	seen := map[string]bool{}
	for _, a := range aliases {
		switch {
		case a == "":
			t.Error("empty alias")
		case a != strings.ToLower(a):
			t.Errorf("alias %q must be lowercase (callers lowercase the needle)", a)
		case strings.TrimSpace(a) != a:
			t.Errorf("alias %q has surrounding whitespace", a)
		case seen[a]:
			t.Errorf("duplicate alias %q", a)
		}
		seen[a] = true
	}

	// A URL that is not this driver's must be rejected: the CLI layer infers
	// the driver from the URL when the spec names none, and an over-eager
	// parser would steal another driver's connection.
	for _, bad := range []string{"", "   ", "not a url", "jdbc:no-such-database://h1:1/db", "http://example.com"} {
		if _, err := ec.ParseExternalURL(bad); err == nil {
			t.Errorf("ParseExternalURL(%q) must fail for driver %q", bad, d.Name())
		}
	}

	if dotKey, val, ok := ec.TranslateExternalProp("catdbNoSuchProperty", "1"); ok {
		t.Errorf("an unknown property must not translate, got %q=%q", dotKey, val)
	}
}

// CheckExternalProp runs one property through the driver's translation table
// and validates the shape of the result: the dotted key must be either a core
// connection key or a params./ssl./sshTunnel. key the driver's
// ConnectionSchema actually declares. Returns the translation so the caller
// can assert the converted value.
func CheckExternalProp(t *testing.T, d dbdriver.Driver, key, value string) (string, string, bool) {
	t.Helper()
	ec, ok := d.(dbdriver.ExternalCompat)
	if !ok {
		t.Fatalf("driver %q does not implement dbdriver.ExternalCompat", d.Name())
	}
	dotKey, val, ok := ec.TranslateExternalProp(key, value)
	if !ok {
		if dotKey != "" || val != "" {
			t.Errorf("%s: an untranslatable property must return empty results, got %q=%q", key, dotKey, val)
		}
		return "", "", false
	}
	if coreConnectionKeys[dotKey] {
		return dotKey, val, true
	}
	for _, f := range d.ConnectionSchema() {
		if f.Key == dotKey {
			return dotKey, val, true
		}
	}
	t.Errorf("%s translates to %q, which driver %q has no connection-schema field for", key, dotKey, d.Name())
	return dotKey, val, true
}
