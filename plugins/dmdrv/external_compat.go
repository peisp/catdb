package dmdrv

import (
	"strconv"
	"strings"
	"time"

	"catdb/internal/dbdriver"
)

// This file implements the optional dbdriver.ExternalCompat extension: the
// DM (达梦) half of the DBeaver `-con` command-line compatibility layer
// (docs/CLI_CON_DESIGN.md §3.2).

// JDBCPrefix is the DM JDBC URL scheme.
const JDBCPrefix = "jdbc:dm://"

// ExternalDriverAliases lists the DBeaver driver ids that mean "DM".
func (driver) ExternalDriverAliases() []string { return []string{"dm", "dameng"} }

// ParseExternalURL parses `jdbc:dm://host[:port][/schema][?k=v&…]`. The
// defaults mirror ConnectionSchema (127.0.0.1:5236); the schema is left empty
// so the login user's default schema applies, same as the form's default.
func (driver) ParseExternalURL(externalURL string) (dbdriver.ExternalURLParts, error) {
	return dbdriver.ParseJDBCURL(externalURL, JDBCPrefix, dbdriver.ExternalURLParts{
		Host: "127.0.0.1",
		Port: "5236",
	})
}

// TranslateExternalProp maps the few DM JDBC properties that line up with this
// driver's connection schema (host/port/user/password/database +
// params.timeout). Everything else returns ok=false — the caller warns and
// drops it rather than inventing a params key the driver would ignore.
func (driver) TranslateExternalProp(key, value string) (string, string, bool) {
	v := strings.TrimSpace(value)
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "connecttimeout", "logintimeout":
		// DM JDBC: milliseconds. buildDSN wants a Go duration string.
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return "params.timeout", (time.Duration(n) * time.Millisecond).String(), true
		}
	case "schema":
		// DM addresses the initial schema the way other drivers address the
		// initial database, and so does catdb's "database" key here.
		if v != "" {
			return "database", v, true
		}
	case "user", "username":
		if v != "" {
			return "user", v, true
		}
	case "password":
		if v != "" {
			return "password", v, true
		}
	}
	return "", "", false
}
