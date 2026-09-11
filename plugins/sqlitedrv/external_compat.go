package sqlitedrv

import (
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
)

// This file implements the optional dbdriver.ExternalCompat extension: the
// SQLite half of the DBeaver `-con` command-line compatibility layer
// (docs/CLI_CON_DESIGN.md §3.2).

// JDBCPrefix is the sqlite-jdbc URL scheme. Unlike the client/server drivers
// it is an OPAQUE URL — everything after the scheme is a file path, not an
// authority.
const JDBCPrefix = "jdbc:sqlite:"

// ExternalDriverAliases lists the DBeaver driver ids that mean "SQLite".
func (driver) ExternalDriverAliases() []string { return []string{"sqlite", "sqlite_jdbc"} }

// ParseExternalURL parses `jdbc:sqlite:<path>`. The path is taken verbatim —
// no percent-decoding, no separator rewriting — so a Windows path keeps its
// backslashes and `:memory:` keeps its colons.
func (driver) ParseExternalURL(externalURL string) (dbdriver.ExternalURLParts, error) {
	s := strings.TrimSpace(externalURL)
	if len(s) < len(JDBCPrefix) || !strings.EqualFold(s[:len(JDBCPrefix)], JDBCPrefix) {
		return dbdriver.ExternalURLParts{}, fmt.Errorf("sqlitedrv: %q is not a %s URL", externalURL, JDBCPrefix)
	}
	path := s[len(JDBCPrefix):]
	if path == "" {
		return dbdriver.ExternalURLParts{}, fmt.Errorf("sqlitedrv: %q has no database file path", externalURL)
	}
	return dbdriver.ExternalURLParts{Database: path, Props: map[string]string{}}, nil
}

// TranslateExternalProp has nothing to translate: sqlite-jdbc's pragma
// properties do not line up with catdb's open-mode/busy-timeout/foreign-keys
// triple closely enough to map mechanically, so every property is reported as
// untranslatable and the caller warns and drops it.
func (driver) TranslateExternalProp(_, _ string) (string, string, bool) {
	return "", "", false
}
