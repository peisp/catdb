package postgresdrv

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"

	"catdb/internal/dbdriver"
)

// tx wraps a pgx.Tx and satisfies dbdriver.Tx via composition with querier.
//
// A pgx.Tx owns exactly one physical connection; committing while a ResultSet
// is still streaming would fail with "conn busy". database/sql force-closes
// pending Rows on Commit/Rollback, and generic layers rely on that, so we
// track every ResultSet opened through this Tx and close them first.
type tx struct {
	*querier
	raw pgx.Tx

	mu   sync.Mutex
	open []*resultSet
}

func newTx(raw pgx.Tx) dbdriver.Tx {
	t := &tx{
		querier: &querier{q: raw},
		raw:     raw,
	}
	t.querier.onRows = func(rs *resultSet) {
		t.mu.Lock()
		t.open = append(t.open, rs)
		t.mu.Unlock()
	}
	return t
}

func (t *tx) closeOpenRows() {
	t.mu.Lock()
	open := t.open
	t.open = nil
	t.mu.Unlock()
	for _, rs := range open {
		_ = rs.Close() // idempotent
	}
}

// dbdriver.Tx.Commit/Rollback carry no ctx; ending a transaction should not
// be cancellable anyway.
func (t *tx) Commit() error {
	t.closeOpenRows()
	return mapTxDone(t.raw.Commit(context.Background()))
}

func (t *tx) Rollback() error {
	t.closeOpenRows()
	return mapTxDone(t.raw.Rollback(context.Background()))
}

// mapTxDone folds pgx's "tx is closed" error into the driver-neutral sentinel
// so generic layers can errors.Is against dbdriver.ErrTxDone.
func mapTxDone(err error) error {
	if errors.Is(err, pgx.ErrTxClosed) {
		return dbdriver.ErrTxDone
	}
	return err
}
