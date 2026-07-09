package dmdrv

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"catdb/internal/dbdriver"
)

const defaultDialTimeout = 15 * time.Second

// buildDSN renders the DM DSN: dm://user:password@host:port?prop=val&…
//
// The dm driver's parseDSN is naive string splitting with NO URL-unescaping:
// the query starts at the LAST '?', credentials end at the LAST '@', and
// user/password split at the FIRST ':'. Characters that would break that
// parse cannot be transported at all, so they are rejected up front with a
// clear error instead of producing a confusing connect failure.
func buildDSN(cfg dbdriver.ConnConfig, dialName string) (string, error) {
	if strings.ContainsAny(cfg.User, ":@?") {
		return "", fmt.Errorf("dmdrv: user name must not contain ':', '@' or '?'")
	}
	if strings.ContainsAny(cfg.Password, "@?") {
		return "", fmt.Errorf("dmdrv: password must not contain '@' or '?' (DM DSN limitation)")
	}

	host := cfg.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := cfg.Port
	if port == 0 {
		port = 5236
	}

	timeout := defaultDialTimeout
	if v, ok := cfg.Params["timeout"]; ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			timeout = d
		}
	}

	var b strings.Builder
	b.WriteString("dm://")
	b.WriteString(cfg.User)
	if cfg.Password != "" {
		b.WriteString(":")
		b.WriteString(cfg.Password)
	}
	fmt.Fprintf(&b, "@%s:%d", host, port)

	params := []string{
		fmt.Sprintf("connectTimeout=%d", timeout.Milliseconds()), // ms
		"appName=catdb",
	}
	if s := strings.TrimSpace(cfg.Database); s != "" {
		params = append(params, "schema="+s)
	}
	if dialName != "" {
		params = append(params, "dialName="+dialName)
	}
	b.WriteString("?")
	b.WriteString(strings.Join(params, "&"))
	return b.String(), nil
}

func randomID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
