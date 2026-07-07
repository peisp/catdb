// Package postgresdrv implements dbdriver.Driver against jackc/pgx/v5
// (native pgx + pgxpool — NOT database/sql, see ARCHITECTURE.md §3.5).
// SSL is mapped from dbdriver.SSLConfig onto a *tls.Config; SSH goes through
// internal/tunnel with BOTH DialFunc and LookupFunc overridden so DNS
// resolution happens on the jump host (ARCHITECTURE.md §6.2).
//
// PostgreSQL databases are hard isolation boundaries — one session cannot
// query a sibling database — so the connection implements the optional
// dbdriver.DatabaseRouter extension: it lazily opens one pool per database
// and generic layers route SQL through dbdriver.RouteQuerier/RouteBegin.
// The object tree therefore lists every database and expands them
// transparently, Navicat-style.
//
// Registration is automatic — main.go anonymously imports catdb/plugins,
// which blank-imports this package (see plugins/plugins_postgres.go).
package postgresdrv

import (
	"context"
	"fmt"
	"strings"

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

// Open sets up the SSH tunnel when configured and dials the profile's
// default database (further databases open lazily via poolFor). On any error
// the partially-opened resources are cleaned up.
func (driver) Open(ctx context.Context, cfg dbdriver.ConnConfig) (dbdriver.Connection, error) {
	var (
		t   *tunnel.Tunnel
		err error
	)
	if cfg.SSHTunnel != nil && cfg.SSHTunnel.Host != "" {
		t, err = tunnel.Open(ctx, cfg.SSHTunnel)
		if err != nil {
			return nil, fmt.Errorf("postgresdrv: ssh tunnel: %w", err)
		}
	}

	defaultDB := strings.TrimSpace(cfg.Database)
	if defaultDB == "" {
		defaultDB = "postgres"
	}
	c := &connection{
		cfg:       cfg,
		tunnel:    t,
		pools:     map[string]*pgxpool.Pool{},
		defaultDB: defaultDB,
	}
	if _, err := c.defaultPool(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}
