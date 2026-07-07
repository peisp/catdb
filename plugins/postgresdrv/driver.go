// Package postgresdrv implements dbdriver.Driver against jackc/pgx/v5
// (native pgx + pgxpool — NOT database/sql, see ARCHITECTURE.md §3.5).
// SSL is mapped from dbdriver.SSLConfig onto a *tls.Config; SSH goes through
// internal/tunnel with BOTH DialFunc and LookupFunc overridden so DNS
// resolution happens on the jump host (ARCHITECTURE.md §6.2).
//
// Scope note: a PostgreSQL connection is bound to ONE database — the server
// does not support cross-database queries. Metadata therefore only serves the
// connected database (ListDatabases returns just current_database()); to work
// with another database the user creates another connection profile.
//
// Registration is automatic — main.go anonymously imports catdb/plugins,
// which blank-imports this package (see plugins/plugins_postgres.go).
package postgresdrv

import (
	"context"
	"fmt"
	"net"

	"github.com/jackc/pgx/v5/pgxpool"

	"catdb/internal/dbdriver"
	"catdb/internal/registry"
	"catdb/internal/tunnel"
)

func init() {
	registry.Register(driver{})
}

const driverName = "postgres"

type driver struct{}

func (driver) Name() string    { return driverName }
func (driver) Version() string { return "0.1.0" }

func (driver) Capabilities() dbdriver.Capabilities {
	return dbdriver.Capabilities{
		Schemas:          true, // database → schema → table
		StoredProcedures: true,
		Triggers:         true,
		Views:            true,
		Transactions:     true,
		ExplainPlan:      true,
	}
}

func (driver) Dialect() dbdriver.Dialect { return dialect{} }

// ConnectionSchema describes the form fields the front-end renders.
// Group/Label/Help follow the same i18n contract as mysqldrv: Group is a
// stable key, Label/Help are the English baseline the front-end localizes by
// field key (falling back to these).
func (driver) ConnectionSchema() []dbdriver.ConnParamField {
	return []dbdriver.ConnParamField{
		{Key: "host", Label: "Host", Type: "text", Default: "127.0.0.1", Required: true, Group: "general"},
		{Key: "port", Label: "Port", Type: "number", Default: "5432", Required: true, Group: "general"},
		{Key: "user", Label: "User", Type: "text", Default: "postgres", Required: true, Group: "general"},
		{Key: "password", Label: "Password", Type: "password", Group: "general"},
		{Key: "database", Label: "Database", Type: "text", Default: "postgres", Required: true, Group: "general"},
		{Key: "params.timeout", Label: "Connect timeout", Type: "text", Default: "15s", Group: "advanced", Help: "Go duration string (e.g. 15s, 1m)"},

		{Key: "ssl.mode", Label: "SSL mode", Type: "select", Default: "disable", Options: []string{"disable", "prefer", "require", "verify-ca", "verify-full"}, Group: "ssl"},
		{Key: "ssl.caCert", Label: "CA certificate (PEM)", Type: "text", Group: "ssl"},
		{Key: "ssl.clientCert", Label: "Client certificate (PEM)", Type: "text", Group: "ssl"},
		{Key: "ssl.clientKey", Label: "Client key (PEM)", Type: "text", Group: "ssl"},
		{Key: "ssl.serverName", Label: "Server name (verify-full only)", Type: "text", Group: "ssl"},

		{Key: "sshTunnel.host", Label: "SSH host", Type: "text", Group: "ssh"},
		{Key: "sshTunnel.port", Label: "SSH port", Type: "number", Default: "22", Group: "ssh"},
		{Key: "sshTunnel.user", Label: "SSH user", Type: "text", Group: "ssh"},
		{Key: "sshTunnel.password", Label: "SSH password", Type: "password", Group: "ssh"},
		{Key: "sshTunnel.privateKey", Label: "Private key (PEM)", Type: "text", Group: "ssh"},
		{Key: "sshTunnel.privateKeyPass", Label: "Private key passphrase", Type: "password", Group: "ssh"},
		{Key: "sshTunnel.useAgent", Label: "Use ssh-agent", Type: "bool", Group: "ssh"},
		{Key: "sshTunnel.knownHostsPath", Label: "Known hosts path", Type: "text", Group: "ssh", Help: "Defaults to ~/.ssh/known_hosts (required for host-key verification)"},
	}
}

// Open builds the pgxpool config, wires TLS/SSH as required, and pings the
// pool through ctx. On any error the partially-opened resources are cleaned up.
func (driver) Open(ctx context.Context, cfg dbdriver.ConnConfig) (dbdriver.Connection, error) {
	pc, err := buildPoolConfig(cfg)
	if err != nil {
		return nil, err
	}

	var t *tunnel.Tunnel
	if cfg.SSHTunnel != nil && cfg.SSHTunnel.Host != "" {
		t, err = tunnel.Open(ctx, cfg.SSHTunnel)
		if err != nil {
			return nil, fmt.Errorf("postgresdrv: ssh tunnel: %w", err)
		}
		// Both hooks are required (ARCHITECTURE.md §6.2): DialFunc routes the
		// TCP stream through the jump host, LookupFunc keeps pgx from resolving
		// the (possibly jump-host-private) DB hostname on the local machine.
		pc.ConnConfig.DialFunc = func(ctx context.Context, _, addr string) (net.Conn, error) {
			return t.Dial(ctx, addr)
		}
		pc.ConnConfig.LookupFunc = func(_ context.Context, host string) ([]string, error) {
			return []string{host}, nil
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		if t != nil {
			_ = t.Close()
		}
		return nil, fmt.Errorf("postgresdrv: pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		if t != nil {
			_ = t.Close()
		}
		return nil, fmt.Errorf("postgresdrv: ping: %w", err)
	}

	return &connection{pool: pool, tunnel: t}, nil
}
