package mysqldrv

import (
	"catdb/internal/dbdriver"
	"database/sql"
)

// tx wraps a *sql.Tx and satisfies dbdriver.Tx via composition with querier.
type tx struct {
	*querier
	raw *sql.Tx
}

func newTx(raw *sql.Tx) dbdriver.Tx {
	return &tx{
		querier: &querier{exec: raw},
		raw:     raw,
	}
}

func (t *tx) Commit() error   { return t.raw.Commit() }
func (t *tx) Rollback() error { return t.raw.Rollback() }
