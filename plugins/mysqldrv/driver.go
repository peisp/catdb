// Package mysqldrv is the MVP's only production driver. It implements
// dbdriver.Driver against go-sql-driver/mysql, with SSL handled via
// mysql.RegisterTLSConfig and SSH handled via the internal/tunnel package
// and mysql.RegisterDialContext.
//
// Registration is automatic — main.go anonymously imports catdb/plugins,
// which (eventually) blank-imports catdb/plugins/mysqldrv (see
// plugins/plugins_mysql.go).
package mysqldrv

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"time"

	"github.com/go-sql-driver/mysql"

	"catdb/internal/dbdriver"
	"catdb/internal/registry"
	"catdb/internal/tunnel"
)

func init() {
	registry.Register(driver{})
}

const driverName = "mysql"

type driver struct{}

func (driver) Name() string    { return driverName }
func (driver) Version() string { return "0.1.0" }

func (driver) Capabilities() dbdriver.Capabilities {
	return dbdriver.Capabilities{
		Schemas:          false, // MySQL: schema == database, so don't surface a separate schema level
		StoredProcedures: true,
		Triggers:         true,
		Views:            true,
		Transactions:     true,
		ExplainPlan:      true,
		DatabaseEditor:   true, // metadata implements DatabaseEditor (see dbeditor.go)
	}
}

func (driver) Dialect() dbdriver.Dialect { return dialect{} }

// ConnectionSchema describes the form fields the front-end renders.
// Adding/removing fields here automatically updates the connection form.
func (driver) ConnectionSchema() []dbdriver.ConnParamField {
	// Group is a stable, locale-independent key ("general"/"advanced"/"ssl"/
	// "ssh"); the front-end localizes it (connection.form.groups.*). Label/Help
	// are the English baseline — the front-end localizes them per field key with
	// a fallback to these (connection.form.field.* / connection.form.help.*).
	return []dbdriver.ConnParamField{
		{Key: "host", Label: "Host", Type: "text", Default: "127.0.0.1", Required: true, Group: "general"},
		{Key: "port", Label: "Port", Type: "number", Default: "3306", Required: true, Group: "general"},
		{Key: "user", Label: "User", Type: "text", Default: "root", Required: true, Group: "general"},
		{Key: "password", Label: "Password", Type: "password", Group: "general"},
		{Key: "database", Label: "Database", Type: "text", Group: "general", Help: "Initial schema; leave blank to connect at server level"},
		{Key: "params.collation", Label: "Collation", Type: "text", Default: "utf8mb4_general_ci", Group: "advanced"},
		{Key: "params.timeout", Label: "Connect timeout", Type: "text", Default: "15s", Group: "advanced", Help: "Go duration string (e.g. 15s, 1m)"},
		{Key: "params.readTimeout", Label: "Read timeout", Type: "text", Group: "advanced"},
		{Key: "params.writeTimeout", Label: "Write timeout", Type: "text", Group: "advanced"},
		{Key: "params.maxAllowedPacket", Label: "Max allowed packet", Type: "number", Default: "4194304", Group: "advanced"},

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

// Open builds the DSN, sets up TLS/SSH as required, opens a *sql.DB pool, and
// pings it through ctx. On any error the partially-opened resources are
// cleaned up.
func (driver) Open(ctx context.Context, cfg dbdriver.ConnConfig) (dbdriver.Connection, error) {
	var (
		tlsName  string
		tlsClean func()
	)

	if cfg.SSL != nil && cfg.SSL.Mode != "" && cfg.SSL.Mode != "disable" {
		tcfg, sentinel, err := buildTLSConfig(cfg.SSL)
		if err != nil {
			return nil, err
		}
		switch {
		case sentinel != "":
			tlsName = sentinel
		case tcfg != nil:
			tlsName, tlsClean, err = registerTLS(tcfg)
			if err != nil {
				return nil, err
			}
		}
	}

	network := "tcp"
	var (
		t           *tunnel.Tunnel
		dialerClean func()
	)
	if cfg.SSHTunnel != nil && cfg.SSHTunnel.Host != "" {
		tn, err := tunnel.Open(ctx, cfg.SSHTunnel)
		if err != nil {
			if tlsClean != nil {
				tlsClean()
			}
			return nil, fmt.Errorf("mysqldrv: ssh tunnel: %w", err)
		}
		t = tn

		network = "tcp+ssh-" + randomID()
		mysql.RegisterDialContext(network, func(ctx context.Context, addr string) (net.Conn, error) {
			return t.Dial(ctx, addr)
		})
		netName := network
		dialerClean = func() { mysql.DeregisterDialContext(netName) }
	}

	dsn := buildDSN(cfg, network, tlsName)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		if dialerClean != nil {
			dialerClean()
		}
		if t != nil {
			_ = t.Close()
		}
		if tlsClean != nil {
			tlsClean()
		}
		return nil, fmt.Errorf("mysqldrv: sql.Open: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		if dialerClean != nil {
			dialerClean()
		}
		if t != nil {
			_ = t.Close()
		}
		if tlsClean != nil {
			tlsClean()
		}
		return nil, fmt.Errorf("mysqldrv: ping: %w", err)
	}

	return &connection{
		db:          db,
		tunnel:      t,
		tlsClean:    tlsClean,
		dialerClean: dialerClean,
	}, nil
}
