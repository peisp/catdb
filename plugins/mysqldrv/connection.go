package mysqldrv

import (
	"context"
	"database/sql"
	"fmt"

	"catdb/internal/dbdriver"
	"catdb/internal/tunnel"
)

// connection wraps the live *sql.DB pool plus any registered TLS / dialer
// names and the SSH tunnel they depend on. Close releases all of them in the
// reverse order they were created.
type connection struct {
	db          *sql.DB
	tunnel      *tunnel.Tunnel
	tlsClean    func()
	dialerClean func()
}

func (c *connection) Ping(ctx context.Context) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("mysqldrv: connection is closed")
	}
	return c.db.PingContext(ctx)
}

func (c *connection) ServerInfo(ctx context.Context) (dbdriver.ServerInfo, error) {
	if c == nil || c.db == nil {
		return dbdriver.ServerInfo{}, fmt.Errorf("mysqldrv: connection is closed")
	}
	var ver, usr string
	err := c.db.QueryRowContext(ctx, "SELECT VERSION(), USER()").Scan(&ver, &usr)
	if err != nil {
		return dbdriver.ServerInfo{}, fmt.Errorf("mysqldrv: server info: %w", err)
	}
	return dbdriver.ServerInfo{Version: ver, User: usr}, nil
}

func (c *connection) Close() error {
	if c == nil {
		return nil
	}
	var firstErr error
	if c.db != nil {
		if err := c.db.Close(); err != nil {
			firstErr = err
		}
		c.db = nil
	}
	if c.dialerClean != nil {
		c.dialerClean()
		c.dialerClean = nil
	}
	if c.tunnel != nil {
		if err := c.tunnel.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		c.tunnel = nil
	}
	if c.tlsClean != nil {
		c.tlsClean()
		c.tlsClean = nil
	}
	return firstErr
}

// Querier ships in M2 (real impl); Metadata + Editor land in M3.

func (c *connection) Querier() dbdriver.Querier {
	if c == nil || c.db == nil {
		return nil
	}
	return &querier{exec: c.db}
}

func (c *connection) Metadata() dbdriver.Metadata {
	if c == nil || c.db == nil {
		return nil
	}
	return metadata{db: c.db}
}

func (c *connection) Editor() dbdriver.Editor {
	if c == nil || c.db == nil {
		return nil
	}
	return editor{db: c.db, dialect: dialect{}}
}

func (c *connection) Begin(ctx context.Context, opts *sql.TxOptions) (dbdriver.Tx, error) {
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("mysqldrv: connection is closed")
	}
	t, err := c.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return newTx(t), nil
}

