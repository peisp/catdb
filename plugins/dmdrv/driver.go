// Package dmdrv implements dbdriver.Driver for DM (达梦) against the
// official Go driver (gitee.com/chunanyong/dm — pure Go, database/sql).
// SSH is handled via internal/tunnel plus dm.RegisterDialContext (the DSN's
// dialName property routes all traffic through the tunnel's net.Conn).
//
// DM has one database per instance and any schema is addressable from one
// session, so — like MySQL — the schema level is collapsed into the database
// position (Capabilities.Schemas=false) and no DatabaseRouter is needed.
//
// Registration is automatic — main.go anonymously imports catdb/plugins,
// which blank-imports this package (see plugins/plugins_dm.go).
package dmdrv

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"time"

	dm "gitee.com/chunanyong/dm"

	"catdb/internal/dbdriver"
	"catdb/internal/registry"
	"catdb/internal/tunnel"
)

func init() {
	registry.Register(driver{})
}

const driverName = "dm"

type driver struct{}

func (driver) Name() string    { return driverName }
func (driver) Version() string { return "0.1.0" }

func (driver) Capabilities() dbdriver.Capabilities {
	return dbdriver.Capabilities{
		Schemas:          false, // schema level collapsed into the database position
		StoredProcedures: true,
		Triggers:         true,
		Views:            true,
		Transactions:     true,
		ExplainPlan:      true,
	}
}

func (driver) Dialect() dbdriver.Dialect { return dialect{} }

// ConnectionSchema describes the form fields the front-end renders.
// Group is a stable key, Label/Help are the English baseline the front-end
// localizes by field key (falling back to these). No SSL group: the DM
// driver only accepts certificate *paths* (sslCertPath/sslKeyPath), which
// doesn't fit the PEM-content SSLConfig contract — revisit when needed.
func (driver) ConnectionSchema() []dbdriver.ConnParamField {
	return []dbdriver.ConnParamField{
		{Key: "host", Label: "Host", Type: "text", Default: "127.0.0.1", Required: true, Group: "general"},
		{Key: "port", Label: "Port", Type: "number", Default: "5236", Required: true, Group: "general"},
		{Key: "user", Label: "User", Type: "text", Default: "SYSDBA", Required: true, Group: "general"},
		{Key: "password", Label: "Password", Type: "password", Group: "general"},
		{Key: "database", Label: "Database", Type: "text", Group: "general", Help: "Initial schema; leave blank to use the login user's default schema"},
		{Key: "params.timeout", Label: "Connect timeout", Type: "text", Default: "15s", Group: "advanced", Help: "Go duration string (e.g. 15s, 1m)"},

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

// Open builds the DSN, sets up the SSH tunnel as required, opens a *sql.DB
// pool, and pings it through ctx. On any error the partially-opened
// resources are cleaned up.
func (driver) Open(ctx context.Context, cfg dbdriver.ConnConfig) (dbdriver.Connection, error) {
	var (
		t           *tunnel.Tunnel
		dialerClean func()
		dialName    string
	)
	if cfg.SSHTunnel != nil && cfg.SSHTunnel.Host != "" {
		tn, err := tunnel.Open(ctx, cfg.SSHTunnel)
		if err != nil {
			return nil, fmt.Errorf("dmdrv: ssh tunnel: %w", err)
		}
		t = tn

		dialName = "ssh-" + randomID()
		dm.RegisterDialContext(dialName, func(ctx context.Context, addr string) (net.Conn, error) {
			return t.Dial(ctx, addr)
		})
		name := dialName
		// The dm driver has no deregister API — overwrite the entry with a
		// dead dialer so a stale pool can never dial through a closed tunnel.
		dialerClean = func() {
			dm.RegisterDialContext(name, func(context.Context, string) (net.Conn, error) {
				return nil, fmt.Errorf("dmdrv: ssh tunnel closed")
			})
		}
	}

	cleanup := func() {
		if dialerClean != nil {
			dialerClean()
		}
		if t != nil {
			_ = t.Close()
		}
	}

	dsn, err := buildDSN(cfg, dialName)
	if err != nil {
		cleanup()
		return nil, err
	}
	db, err := sql.Open("dm", dsn)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("dmdrv: sql.Open: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		cleanup()
		return nil, fmt.Errorf("dmdrv: ping: %w", err)
	}

	return &connection{
		db:          db,
		tunnel:      t,
		dialerClean: dialerClean,
	}, nil
}
