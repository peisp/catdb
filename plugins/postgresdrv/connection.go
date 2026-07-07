package postgresdrv

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"catdb/internal/dbdriver"
	"catdb/internal/tunnel"
)

// connection implements dbdriver.Connection AND the optional DatabaseRouter
// extension. PostgreSQL databases are hard isolation boundaries — one session
// cannot query a sibling database — so the connection lazily opens one
// pgxpool per database the generic layers address (object tree expansion,
// table browsing, sync targets, …). All pools share the connection profile
// and the SSH tunnel; Close releases every pool, then the tunnel.
type connection struct {
	cfg    dbdriver.ConnConfig
	tunnel *tunnel.Tunnel

	mu        sync.Mutex
	pools     map[string]*pgxpool.Pool // keyed by database name
	defaultDB string
	closed    bool
}

var _ dbdriver.DatabaseRouter = (*connection)(nil)

// poolFor returns the pool bound to db, opening it on first use. db == ""
// means the connection's default database.
func (c *connection) poolFor(ctx context.Context, db string) (*pgxpool.Pool, error) {
	if c == nil {
		return nil, fmt.Errorf("postgresdrv: connection is closed")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("postgresdrv: connection is closed")
	}
	if db == "" {
		db = c.defaultDB
	}
	if p, ok := c.pools[db]; ok {
		return p, nil
	}

	cfg := c.cfg
	cfg.Database = db
	pc, err := buildPoolConfig(cfg)
	if err != nil {
		return nil, err
	}
	applyTunnel(pc, c.tunnel)
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: pool for %q: %w", db, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgresdrv: connect to database %q: %w", db, err)
	}
	c.pools[db] = pool
	return pool, nil
}

// defaultPool is poolFor("") — the profile's database.
func (c *connection) defaultPool(ctx context.Context) (*pgxpool.Pool, error) {
	return c.poolFor(ctx, "")
}

func (c *connection) Ping(ctx context.Context) error {
	pool, err := c.defaultPool(ctx)
	if err != nil {
		return err
	}
	return pool.Ping(ctx)
}

func (c *connection) ServerInfo(ctx context.Context) (dbdriver.ServerInfo, error) {
	pool, err := c.defaultPool(ctx)
	if err != nil {
		return dbdriver.ServerInfo{}, err
	}
	var ver, usr string
	err = pool.QueryRow(ctx, "SELECT current_setting('server_version'), current_user").Scan(&ver, &usr)
	if err != nil {
		return dbdriver.ServerInfo{}, fmt.Errorf("postgresdrv: server info: %w", err)
	}
	return dbdriver.ServerInfo{Version: ver, User: usr}, nil
}

func (c *connection) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	pools := c.pools
	c.pools = nil
	c.closed = true
	c.mu.Unlock()
	for _, p := range pools {
		p.Close()
	}
	if c.tunnel != nil {
		err := c.tunnel.Close()
		c.tunnel = nil
		return err
	}
	return nil
}

func (c *connection) Querier() dbdriver.Querier {
	if c == nil {
		return nil
	}
	// The Querier interface method carries no ctx; pool opening is deferred
	// into the first call's ctx via the lazy querier wrapper.
	return &querier{q: lazyPool{c: c, db: ""}}
}

func (c *connection) QuerierFor(_ context.Context, db string) (dbdriver.Querier, error) {
	if c == nil {
		return nil, fmt.Errorf("postgresdrv: connection is closed")
	}
	return &querier{q: lazyPool{c: c, db: db}}, nil
}

func (c *connection) Metadata() dbdriver.Metadata {
	if c == nil {
		return nil
	}
	return metadata{c: c}
}

func (c *connection) Editor() dbdriver.Editor {
	if c == nil {
		return nil
	}
	return editor{c: c, dialect: dialect{}}
}

func (c *connection) Begin(ctx context.Context, opts *dbdriver.TxOptions) (dbdriver.Tx, error) {
	return c.BeginFor(ctx, "", opts)
}

func (c *connection) BeginFor(ctx context.Context, db string, opts *dbdriver.TxOptions) (dbdriver.Tx, error) {
	pool, err := c.poolFor(ctx, db)
	if err != nil {
		return nil, err
	}
	t, err := pool.BeginTx(ctx, pgxTxOptions(opts))
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
