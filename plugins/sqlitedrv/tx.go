package sqlitedrv

import (
	"database/sql"
	"errors"

	"catdb/internal/dbdriver"
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

func (t *tx) Commit() error   { return mapTxDone(t.raw.Commit()) }
func (t *tx) Rollback() error { return mapTxDone(t.raw.Rollback()) }

// mapTxDone folds database/sql's ErrTxDone into the driver-neutral sentinel.
func mapTxDone(err error) error {
	if errors.Is(err, sql.ErrTxDone) {
		return dbdriver.ErrTxDone
	}
	return err
}
