package scanner

import (
	"context"
	"database/sql"
	"sync"

	"catdb/internal/dbdriver"
)

// SQLResultSet is the canonical dbdriver.ResultSet implementation on top of
// *sql.Rows. Plugins backed by database/sql can return it directly.
//
// ctx is captured at construction so the consumer's promise cancellation
// (which cancels the originating context) reaches our row reads.
type SQLResultSet struct {
	ctx     context.Context
	rows    *sql.Rows
	colT    []*sql.ColumnType
	cols    []dbdriver.ColumnMeta
	mu      sync.Mutex
	closed  bool
	doneEOF bool
}

// NewSQLResultSet captures the rows' column metadata up front and returns a
// ready-to-stream ResultSet. The dialect is needed for type mapping; pass
// the driver's Dialect().
func NewSQLResultSet(ctx context.Context, rows *sql.Rows, d dbdriver.Dialect) (*SQLResultSet, error) {
	t, err := rows.ColumnTypes()
	if err != nil {
		_ = rows.Close()
		return nil, err
	}
	return &SQLResultSet{
		ctx:  ctx,
		rows: rows,
		colT: t,
		cols: ColumnMetasFromTypes(t, d),
	}, nil
}

// Columns returns the static column metadata (cached at construction).
func (r *SQLResultSet) Columns() []dbdriver.ColumnMeta { return r.cols }

// Next fetches the next batch. After done=true is returned once, subsequent
// calls return (nil, true, nil) until Close is invoked.
func (r *SQLResultSet) Next(batch int) ([][]any, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, true, ErrAlreadyClosed
	}
	if r.doneEOF {
		return nil, true, nil
	}
	rows, done, err := ScanBatch(r.ctx, r.rows, r.colT, batch)
	if done {
		r.doneEOF = true
	}
	return rows, done, err
}

// Close releases the underlying *sql.Rows and marks the stream finished.
func (r *SQLResultSet) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.rows != nil {
		return r.rows.Close()
	}
	return nil
}
