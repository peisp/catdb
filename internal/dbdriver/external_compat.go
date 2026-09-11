package dbdriver

import (
	"fmt"
	"net/url"
	"strings"
)

// ExternalURLParts is the address information a driver extracts from a
// foreign connection URL (a JDBC string pasted from another tool's command
// line). Every field is a string: an empty one means "the URL did not say"
// after the driver applied its own defaults.
type ExternalURLParts struct {
	Host     string
	Port     string
	Database string
	// Props carries the URL's query parameters under their ORIGINAL foreign
	// property names (e.g. "connectTimeout"), so the caller can run them
	// through TranslateExternalProp exactly like an explicit `prop.<k>`.
	Props map[string]string
}

// ExternalCompat is an OPTIONAL driver extension (probed by type assertion —
// same pattern as BulkMetadata / DatabaseRouter). It lets one driver translate
// a foreign tool's connection vocabulary into catdb's own connection keys.
//
// Its only consumer today is the CLI compatibility layer for DBeaver's `-con`
// command line (see docs/CLI_CON_DESIGN.md): the generic layer knows nothing
// about JDBC URLs or Connector/J property names, it just asks whichever driver
// claims the alias to do the translation (CLAUDE.md #12 — database-specific
// knowledge lives in the interface primitives or inside the plugin, nowhere
// else).
//
// Drivers that do not implement this interface simply accept no foreign
// vocabulary: their connections must be described with catdb-native keys
// (host/port/user/database + params.* / ssl.* / sshTunnel.*), which the
// generic layer understands on its own.
type ExternalCompat interface {
	// ExternalDriverAliases returns the foreign driver ids this driver answers
	// to, lowercase. Matching is case-insensitive; callers lowercase the
	// needle, drivers return lowercase aliases.
	ExternalDriverAliases() []string

	// ParseExternalURL splits a foreign connection URL into address parts,
	// filling in the driver's own defaults for what the URL omits. It must
	// return an error for a URL that is not addressed at this driver.
	ParseExternalURL(externalURL string) (ExternalURLParts, error)

	// TranslateExternalProp maps one foreign connection property onto a catdb
	// connection key in dotted form — "host", "port", "user", "password",
	// "database", "params.<k>", "ssl.<k>" or "sshTunnel.<k>" — converting the
	// value's units and vocabulary on the way (JDBC milliseconds → Go duration
	// string, REQUIRED → require, …).
	//
	// ok=false means "this database has no catdb equivalent"; the caller warns
	// and drops the property rather than failing (wide-open principle: an
	// untranslatable property must never break a connection that would
	// otherwise work).
	TranslateExternalProp(key, value string) (dotKey, val string, ok bool)
}

// ParseJDBCURL is the shared syntax helper for the common JDBC form
//
//	<prefix>[user[:pass]@]host[:port][/database][?k=v&k2=v2]
//
// used by MySQL/MariaDB/PostgreSQL/DM. It is pure syntax — every
// database-specific decision (which prefix, which defaults) is supplied by the
// calling driver through prefix and def, so this helper carries no dialect
// knowledge itself.
//
// def supplies the fallbacks for the parts the URL omits (def.Props is
// ignored). Userinfo is accepted and dropped: catdb takes credentials from the
// spec's own user/password keys. A comma-separated failover host list keeps
// only its first entry — the only one a single catdb profile can represent.
func ParseJDBCURL(externalURL, prefix string, def ExternalURLParts) (ExternalURLParts, error) {
	rest, ok := cutPrefixFold(strings.TrimSpace(externalURL), prefix)
	if !ok {
		return ExternalURLParts{}, fmt.Errorf("dbdriver: %q is not a %s URL", externalURL, prefix)
	}

	out := ExternalURLParts{
		Host:     def.Host,
		Port:     def.Port,
		Database: def.Database,
		Props:    map[string]string{},
	}

	query := ""
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest, query = rest[:i], rest[i+1:]
	}
	authority, path := rest, ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		authority, path = rest[:i], rest[i+1:]
	}
	if i := strings.LastIndexByte(authority, '@'); i >= 0 {
		authority = authority[i+1:]
	}
	if i := strings.IndexByte(authority, ','); i >= 0 {
		authority = authority[:i]
	}

	if authority != "" {
		host, port, err := splitJDBCHostPort(authority)
		if err != nil {
			return ExternalURLParts{}, err
		}
		if host != "" {
			out.Host = host
		}
		if port != "" {
			out.Port = port
		}
	}
	if path != "" {
		out.Database = unescapePathPart(path)
	}
	for k, v := range parseJDBCQuery(query) {
		out.Props[k] = v
	}
	return out, nil
}

// splitJDBCHostPort splits "host", "host:port" and "[::1]:port". The port must
// be numeric — a non-numeric one is a malformed URL, not something to guess at.
func splitJDBCHostPort(authority string) (host, port string, err error) {
	if strings.HasPrefix(authority, "[") {
		end := strings.IndexByte(authority, ']')
		if end < 0 {
			return "", "", fmt.Errorf("dbdriver: malformed IPv6 host in %q", authority)
		}
		host = authority[1:end]
		tail := authority[end+1:]
		switch {
		case tail == "":
			return host, "", nil
		case strings.HasPrefix(tail, ":"):
			port = tail[1:]
		default:
			return "", "", fmt.Errorf("dbdriver: malformed host %q", authority)
		}
	} else if i := strings.LastIndexByte(authority, ':'); i >= 0 {
		host, port = authority[:i], authority[i+1:]
	} else {
		return authority, "", nil
	}
	if port == "" {
		return host, "", nil
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return "", "", fmt.Errorf("dbdriver: non-numeric port %q in %q", port, authority)
		}
	}
	return host, port, nil
}

// parseJDBCQuery splits a `k=v&k2=v2` query string, percent-decoding both
// sides but keeping the foreign property names verbatim.
func parseJDBCQuery(query string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			continue
		}
		k, v := pair, ""
		if i := strings.IndexByte(pair, '='); i >= 0 {
			k, v = pair[:i], pair[i+1:]
		}
		k = unescapeURLPart(k)
		if k == "" {
			continue
		}
		out[k] = unescapeURLPart(v)
	}
	return out
}

// unescapeURLPart percent-decodes one query token ('+' means space, as in any
// query string), falling back to the raw text when s is not valid
// percent-encoding (a value with a stray % must survive).
func unescapeURLPart(s string) string {
	if dec, err := url.QueryUnescape(s); err == nil {
		return dec
	}
	return s
}

// unescapePathPart percent-decodes the path segment. Unlike a query token a
// '+' there is a literal plus, so PathUnescape — not QueryUnescape.
func unescapePathPart(s string) string {
	if dec, err := url.PathUnescape(s); err == nil {
		return dec
	}
	return s
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return s[len(prefix):], true
}
