package dmdrv

import (
	"context"
	"database/sql"
	"fmt"

	"catdb/internal/dbdriver"
	"catdb/internal/tunnel"
)

// connection wraps the live *sql.DB pool plus the registered dialer name and
// the SSH tunnel it depends on. Close releases them in reverse creation order.
//
// DM schemas are not isolation boundaries — one session can address any
// schema with qualified names — so no DatabaseRouter is needed (MySQL model).
type connection struct {
	db          *sql.DB
	tunnel      *tunnel.Tunnel
	dialerClean func()
}

func (c *connection) Ping(ctx context.Context) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("dmdrv: connection is closed")
	}
	return c.db.PingContext(ctx)
}

func (c *connection) ServerInfo(ctx context.Context) (dbdriver.ServerInfo, error) {
	if c == nil || c.db == nil {
		return dbdriver.ServerInfo{}, fmt.Errorf("dmdrv: connection is closed")
	}
	var ver, usr string
	// V$VERSION carries one row per component; the BANNER row holds the
	// server build ("DM Database Server 64 V8", …).
	err := c.db.QueryRowContext(ctx,
		"SELECT (SELECT TOP 1 BANNER FROM V$VERSION WHERE BANNER LIKE 'DM Database%'), CURRENT_USER FROM DUAL").
		Scan(&ver, &usr)
	if err != nil {
		return dbdriver.ServerInfo{}, fmt.Errorf("dmdrv: server info: %w", err)
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
	return firstErr
}

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

func (c *connection) Begin(ctx context.Context, opts *dbdriver.TxOptions) (dbdriver.Tx, error) {
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("dmdrv: connection is closed")
	}
	t, err := c.db.BeginTx(ctx, sqlTxOptions(opts))
	if err != nil {
		return nil, err
	}
	return newTx(t), nil
}

// sqlTxOptions maps the framework-agnostic TxOptions onto database/sql.
// The DM driver accepts READ UNCOMMITTED / READ COMMITTED / SERIALIZABLE and
// rejects REPEATABLE READ outside MySQL-compat mode — map it up to
// SERIALIZABLE (the nearest stronger level) instead of erroring.
func sqlTxOptions(opts *dbdriver.TxOptions) *sql.TxOptions {
	if opts == nil {
		return nil
	}
	iso := sql.LevelDefault
	switch opts.Isolation {
	case dbdriver.IsolationReadUncommitted:
		iso = sql.LevelReadUncommitted
	case dbdriver.IsolationReadCommitted:
		iso = sql.LevelReadCommitted
	case dbdriver.IsolationRepeatableRead, dbdriver.IsolationSerializable:
		iso = sql.LevelSerializable
	}
	return &sql.TxOptions{Isolation: iso, ReadOnly: opts.ReadOnly}
}
