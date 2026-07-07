package sqlitedrv

import (
	"context"
	"database/sql"
	"fmt"
	"os/user"

	"catdb/internal/dbdriver"
)

// connection wraps the live *sql.DB pool over one SQLite file (or an
// in-memory database).
type connection struct {
	db *sql.DB
}

func (c *connection) Ping(ctx context.Context) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("sqlitedrv: connection is closed")
	}
	return c.db.PingContext(ctx)
}

// ServerInfo: SQLite is embedded — the "server" version is the library
// version and the "user" is the local OS user running the process.
func (c *connection) ServerInfo(ctx context.Context) (dbdriver.ServerInfo, error) {
	if c == nil || c.db == nil {
		return dbdriver.ServerInfo{}, fmt.Errorf("sqlitedrv: connection is closed")
	}
	var ver string
	if err := c.db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&ver); err != nil {
		return dbdriver.ServerInfo{}, fmt.Errorf("sqlitedrv: server info: %w", err)
	}
	usr := "local"
	if u, err := user.Current(); err == nil && u.Username != "" {
		usr = u.Username
	}
	return dbdriver.ServerInfo{Version: ver, User: usr}, nil
}

func (c *connection) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	err := c.db.Close()
	c.db = nil
	return err
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

// Begin starts a transaction. SQLite transactions are always serializable and
// have no read-only variant at this level, so TxOptions are intentionally
// ignored rather than mapped (modernc's driver rejects non-default levels).
func (c *connection) Begin(ctx context.Context, _ *dbdriver.TxOptions) (dbdriver.Tx, error) {
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("sqlitedrv: connection is closed")
	}
	t, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return newTx(t), nil
}
