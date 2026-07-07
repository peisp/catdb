package postgresdrv

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"catdb/internal/dbdriver"
)

// pgxQuerier is the intersection of *pgxpool.Pool and pgx.Tx we need — the
// same querier struct serves both.
type pgxQuerier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// lazyPool satisfies pgxQuerier by resolving the target database's pool at
// call time — Connection.Querier()/QuerierFor carry no ctx, so the dial (for
// a not-yet-opened database) must happen inside the first Exec/Query.
type lazyPool struct {
	c  *connection
	db string
}

func (l lazyPool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	pool, err := l.c.poolFor(ctx, l.db)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	return pool.Exec(ctx, sql, args...)
}

func (l lazyPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	pool, err := l.c.poolFor(ctx, l.db)
	if err != nil {
		return nil, err
	}
	return pool.Query(ctx, sql, args...)
}

type querier struct {
	q pgxQuerier

	// onRows, when set (transaction queriers), registers every ResultSet so
	// the Tx can force-close them before COMMIT/ROLLBACK — a pgx.Tx runs on
	// one physical connection, and pgx errors with "conn busy" where
	// database/sql would have force-closed the pending Rows.
	onRows func(*resultSet)
}

func (q *querier) Exec(ctx context.Context, sqlText string, args ...any) (dbdriver.ExecResult, error) {
	if q == nil || q.q == nil {
		return dbdriver.ExecResult{}, fmt.Errorf("postgresdrv: querier not initialized")
	}
	tag, err := q.q.Exec(ctx, sqlText, args...)
	if err != nil {
		return dbdriver.ExecResult{}, err
	}
	// PostgreSQL has no LastInsertID concept (use RETURNING instead).
	return dbdriver.ExecResult{RowsAffected: tag.RowsAffected()}, nil
}

func (q *querier) Query(ctx context.Context, sqlText string, args ...any) (dbdriver.ResultSet, error) {
	if q == nil || q.q == nil {
		return nil, fmt.Errorf("postgresdrv: querier not initialized")
	}
	rows, err := q.q.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	rs, err := newResultSet(ctx, rows)
	if err != nil {
		return nil, err
	}
	if q.onRows != nil {
		q.onRows(rs)
	}
	return rs, nil
}

func (q *querier) Explain(ctx context.Context, sqlText string) (dbdriver.ResultSet, error) {
	return q.Query(ctx, "EXPLAIN "+sqlText)
}
