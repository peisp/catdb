package postgresdrv

import (
	"strconv"
	"strings"
	"time"

	"catdb/internal/dbdriver"
)

// This file implements the optional dbdriver.ExternalCompat extension: the
// PostgreSQL half of the DBeaver `-con` command-line compatibility layer
// (docs/CLI_CON_DESIGN.md §3.2).

// JDBCPrefix is the pgJDBC URL scheme.
const JDBCPrefix = "jdbc:postgresql://"

// ExternalDriverAliases lists the DBeaver driver ids that mean "PostgreSQL".
func (driver) ExternalDriverAliases() []string {
	return []string{"postgresql", "postgres", "postgres-jdbc"}
}

// ParseExternalURL parses `jdbc:postgresql://host[:port]/[db][?k=v&…]`.
func (driver) ParseExternalURL(externalURL string) (dbdriver.ExternalURLParts, error) {
	return dbdriver.ParseJDBCURL(externalURL, JDBCPrefix, dbdriver.ExternalURLParts{
		Host:     "127.0.0.1",
		Port:     "5432",
		Database: "postgres",
	})
}

// TranslateExternalProp maps pgJDBC connection properties onto catdb keys.
// Property names are matched case-insensitively; unknown ones return ok=false
// so the caller warns and drops them.
//
// Note on the ssl* file properties: pgJDBC takes FILE PATHS while catdb's
// ssl.caCert / ssl.clientCert / ssl.clientKey hold PEM CONTENT. The translated
// value is therefore the path, and the caller is responsible for loading it
// (this layer does no I/O) — see internal/cli's FileRefs.
func (driver) TranslateExternalProp(key, value string) (string, string, bool) {
	v := strings.TrimSpace(value)
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "connecttimeout":
		// pgJDBC: SECONDS (unlike Connector/J's milliseconds).
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return "params.timeout", (time.Duration(n) * time.Second).String(), true
		}
	case "sslmode":
		if m, ok := pgSSLMode(v); ok {
			return "ssl.mode", m, true
		}
	case "ssl":
		// The legacy boolean switch: ssl=true is pgJDBC's "require".
		if b, ok := pgBool(v); ok && b {
			return "ssl.mode", "require", true
		}
	case "sslrootcert":
		if v != "" {
			return "ssl.caCert", v, true
		}
	case "sslcert":
		if v != "" {
			return "ssl.clientCert", v, true
		}
	case "sslkey":
		if v != "" {
			return "ssl.clientKey", v, true
		}
	}
	return "", "", false
}

// pgSSLMode passes through the modes catdb also has. pgJDBC's "allow" (plain
// text first, TLS only if the server insists) has no catdb equivalent; it is
// folded onto "prefer", the closest opportunistic mode.
func pgSSLMode(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "disable", "prefer", "require", "verify-ca", "verify-full":
		return strings.ToLower(strings.TrimSpace(value)), true
	case "allow":
		return "prefer", true
	}
	return "", false
}

func pgBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true, true
	case "false", "0", "no", "off":
		return false, true
	}
	return false, false
}
