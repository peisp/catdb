package postgresdrv

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"catdb/internal/dbdriver"
	"catdb/internal/tunnel"
)

// connection wraps the live pgxpool.Pool plus the SSH tunnel it may depend
// on. Close releases them in reverse creation order.
type connection struct {
	pool   *pgxpool.Pool
	tunnel *tunnel.Tunnel
}

func (c *connection) Ping(ctx context.Context) error {
	if c == nil || c.pool == nil {
		return fmt.Errorf("postgresdrv: connection is closed")
	}
	return c.pool.Ping(ctx)
}

func (c *connection) ServerInfo(ctx context.Context) (dbdriver.ServerInfo, error) {
	if c == nil || c.pool == nil {
		return dbdriver.ServerInfo{}, fmt.Errorf("postgresdrv: connection is closed")
	}
	var ver, usr string
	err := c.pool.QueryRow(ctx, "SELECT current_setting('server_version'), current_user").Scan(&ver, &usr)
	if err != nil {
		return dbdriver.ServerInfo{}, fmt.Errorf("postgresdrv: server info: %w", err)
	}
	return dbdriver.ServerInfo{Version: ver, User: usr}, nil
}

func (c *connection) Close() error {
	if c == nil {
		return nil
	}
	if c.pool != nil {
		c.pool.Close()
		c.pool = nil
	}
	if c.tunnel != nil {
		err := c.tunnel.Close()
		c.tunnel = nil
		return err
	}
	return nil
}

func (c *connection) Querier() dbdriver.Querier {
	if c == nil || c.pool == nil {
		return nil
	}
	return &querier{q: c.pool}
}

func (c *connection) Metadata() dbdriver.Metadata {
	if c == nil || c.pool == nil {
		return nil
	}
	return metadata{pool: c.pool}
}

func (c *connection) Editor() dbdriver.Editor {
	if c == nil || c.pool == nil {
		return nil
	}
	return editor{pool: c.pool, dialect: dialect{}}
}

func (c *connection) Begin(ctx context.Context, opts *dbdriver.TxOptions) (dbdriver.Tx, error) {
	if c == nil || c.pool == nil {
		return nil, fmt.Errorf("postgresdrv: connection is closed")
	}
	t, err := c.pool.BeginTx(ctx, pgxTxOptions(opts))
	if err != nil {
		return nil, err
	}
	return newTx(t), nil
}

// pgxTxOptions maps the framework-agnostic TxOptions onto pgx.
func pgxTxOptions(opts *dbdriver.TxOptions) pgx.TxOptions {
	if opts == nil {
		return pgx.TxOptions{}
	}
	out := pgx.TxOptions{}
	switch opts.Isolation {
	case dbdriver.IsolationReadUncommitted:
		out.IsoLevel = pgx.ReadUncommitted
	case dbdriver.IsolationReadCommitted:
		out.IsoLevel = pgx.ReadCommitted
	case dbdriver.IsolationRepeatableRead:
		out.IsoLevel = pgx.RepeatableRead
	case dbdriver.IsolationSerializable:
		out.IsoLevel = pgx.Serializable
	}
	if opts.ReadOnly {
		out.AccessMode = pgx.ReadOnly
	}
	return out
}
