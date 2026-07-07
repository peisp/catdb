package postgresdrv

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"catdb/internal/dbdriver"
	"catdb/internal/tunnel"
)

const defaultDialTimeout = 15 * time.Second

// buildPoolConfig translates a ConnConfig into a *pgxpool.Config.
//
// The base DSN is built with sslmode=disable so pgconn's env-var / sslmode
// magic never kicks in; TLS is then applied deterministically from
// dbdriver.SSLConfig (see applyTLS).
//
// DefaultQueryExecMode is the simple protocol: results always arrive in text
// format (which the resultset converter relies on), ad-hoc user queries don't
// pollute the prepared-statement cache, and multi-statement strings (our
// GenerateCreateTable output, imported scripts) execute in one round trip.
// Parameterized calls are still safe — pgx sanitizes the arguments client-side.
func buildPoolConfig(cfg dbdriver.ConnConfig) (*pgxpool.Config, error) {
	port := cfg.Port
	if port == 0 {
		port = 5432
	}
	db := strings.TrimSpace(cfg.Database)
	if db == "" {
		db = "postgres"
	}
	timeout := defaultDialTimeout
	if v, ok := cfg.Params["timeout"]; ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			timeout = d
		}
	}

	parts := []string{
		"host=" + quoteDSNValue(cfg.Host),
		fmt.Sprintf("port=%d", port),
		"user=" + quoteDSNValue(cfg.User),
		"dbname=" + quoteDSNValue(db),
		"sslmode=disable",
		fmt.Sprintf("connect_timeout=%d", int(timeout.Seconds())),
		"application_name=catdb",
	}
	if cfg.Password != "" {
		parts = append(parts, "password="+quoteDSNValue(cfg.Password))
	}

	pc, err := pgxpool.ParseConfig(strings.Join(parts, " "))
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: parse config: %w", err)
	}
	pc.MaxConns = 10
	pc.MinConns = 0
	pc.MaxConnLifetime = 30 * time.Minute
	pc.MaxConnIdleTime = 5 * time.Minute
	pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	if err := applyTLS(&pc.ConnConfig.Config, cfg); err != nil {
		return nil, err
	}
	return pc, nil
}

// applyTLS installs the tls.Config derived from cfg.SSL onto the pgconn
// config. "prefer" additionally appends a plaintext fallback so a server
// without SSL still connects.
func applyTLS(cc *pgconn.Config, cfg dbdriver.ConnConfig) error {
	tcfg, err := buildTLSConfig(cfg.SSL, cfg.Host)
	if err != nil {
		return err
	}
	if tcfg == nil {
		return nil
	}
	cc.TLSConfig = tcfg
	if cfg.SSL.Mode == "prefer" {
		cc.Fallbacks = append(cc.Fallbacks, &pgconn.FallbackConfig{
			Host: cc.Host, Port: cc.Port, TLSConfig: nil,
		})
	}
	return nil
}

// applyTunnel wires the SSH tunnel's dial hooks into a pool config. Both are
// required (ARCHITECTURE.md §6.2): DialFunc routes the TCP stream through the
// jump host, LookupFunc keeps pgx from resolving the (possibly
// jump-host-private) DB hostname on the local machine. nil tunnel = no-op.
func applyTunnel(pc *pgxpool.Config, t *tunnel.Tunnel) {
	if t == nil {
		return
	}
	pc.ConnConfig.DialFunc = func(ctx context.Context, _, addr string) (net.Conn, error) {
		return t.Dial(ctx, addr)
	}
	pc.ConnConfig.LookupFunc = func(_ context.Context, host string) ([]string, error) {
		return []string{host}, nil
	}
}

// quoteDSNValue renders a value for the keyword/value DSN form: single-quoted
// with backslash and quote escapes, so hosts/users/passwords with spaces or
// quotes survive parsing.
func quoteDSNValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `'`, `\'`)
	return "'" + v + "'"
}
