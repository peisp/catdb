package mysqldrv

import (
	"strconv"
	"strings"
	"time"

	"catdb/internal/dbdriver"
)

// This file implements the optional dbdriver.ExternalCompat extension: the
// MySQL half of the DBeaver `-con` command-line compatibility layer
// (docs/CLI_CON_DESIGN.md §3.2). MariaDB speaks the same vocabulary and
// reuses everything here except the URL prefix and the alias list.

// JDBCPrefix is the Connector/J URL scheme.
const JDBCPrefix = "jdbc:mysql://"

// ExternalDriverAliases lists the DBeaver driver ids that mean "MySQL".
func (Driver) ExternalDriverAliases() []string { return []string{"mysql", "mysql5", "mysql8"} }

// ParseExternalURL parses `jdbc:mysql://host[:port]/[db][?k=v&…]`.
func (Driver) ParseExternalURL(externalURL string) (dbdriver.ExternalURLParts, error) {
	return ParseExternalURLWithPrefix(externalURL, JDBCPrefix)
}

// ParseExternalURLWithPrefix parses a MySQL-family JDBC URL under the given
// scheme. Exported so mariadbdrv can reuse it with `jdbc:mariadb://` instead
// of carrying a second copy of the syntax.
func ParseExternalURLWithPrefix(externalURL, prefix string) (dbdriver.ExternalURLParts, error) {
	return dbdriver.ParseJDBCURL(externalURL, prefix, dbdriver.ExternalURLParts{
		Host: "127.0.0.1",
		Port: "3306",
		// No database default: connecting at server level is legal.
	})
}

// TranslateExternalProp maps Connector/J connection properties onto catdb
// keys. Property names are matched case-insensitively (DBeaver specs are
// hand-written and JDBC docs are inconsistent about casing); unknown
// properties return ok=false so the caller can warn and drop them.
func (Driver) TranslateExternalProp(key, value string) (string, string, bool) {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "connecttimeout":
		// Connector/J: milliseconds. go-sql-driver: Go duration string.
		if d, ok := millisAsDuration(value); ok {
			return "params.timeout", d, true
		}
	case "sockettimeout":
		if d, ok := millisAsDuration(value); ok {
			return "params.readTimeout", d, true
		}
	case "sslmode":
		if m, ok := mysqlSSLMode(value); ok {
			return "ssl.mode", m, true
		}
	case "usessl":
		// Pre-8.0 switch: useSSL=false disables TLS outright, true asks for it
		// opportunistically (Connector/J's PREFERRED).
		if b, ok := parseBool(value); ok {
			if b {
				return "ssl.mode", "prefer", true
			}
			return "ssl.mode", "disable", true
		}
	case "requiressl":
		// Only the "on" side carries information: requireSSL=false just means
		// "don't upgrade useSSL", which useSSL already said.
		if b, ok := parseBool(value); ok && b {
			return "ssl.mode", "require", true
		}
	case "maxallowedpacket":
		if _, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
			return "params.maxAllowedPacket", strings.TrimSpace(value), true
		}
	case "connectioncollation":
		if v := strings.TrimSpace(value); v != "" {
			return "params.collation", v, true
		}
	}
	return "", "", false
}

// mysqlSSLMode maps Connector/J's sslMode vocabulary onto catdb's.
func mysqlSSLMode(value string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "DISABLED":
		return "disable", true
	case "PREFERRED":
		return "prefer", true
	case "REQUIRED":
		return "require", true
	case "VERIFY_CA":
		return "verify-ca", true
	case "VERIFY_IDENTITY":
		return "verify-full", true
	}
	return "", false
}

// millisAsDuration renders a millisecond count as a Go duration string
// ("30000" → "30s"). A negative or non-numeric value is rejected.
func millisAsDuration(value string) (string, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || n < 0 {
		return "", false
	}
	return (time.Duration(n) * time.Millisecond).String(), true
}

// parseBool accepts the spellings JDBC properties use for booleans.
func parseBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true, true
	case "false", "0", "no", "off":
		return false, true
	}
	return false, false
}
