package dmdrv

import (
	"context"
	"database/sql"
	"fmt"

	"catdb/internal/core/scanner"
	"catdb/internal/dbdriver"
)

// querier is the database/sql-backed Querier for DM. The same struct
// satisfies dbdriver.Querier whether it carries the pool (*sql.DB) or a Tx.
type querier struct {
	exec execerQueryer
}

// execerQueryer is the intersection of *sql.DB and *sql.Tx for our purposes.
type execerQueryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (q *querier) Exec(ctx context.Context, sqlText string, args ...any) (dbdriver.ExecResult, error) {
	if q == nil || q.exec == nil {
		return dbdriver.ExecResult{}, fmt.Errorf("dmdrv: querier not initialized")
	}
	res, err := q.exec.ExecContext(ctx, sqlText, args...)
	if err != nil {
		return dbdriver.ExecResult{}, err
	}
	out := dbdriver.ExecResult{}
	if n, e := res.RowsAffected(); e == nil {
		out.RowsAffected = n
	}
	if id, e := res.LastInsertId(); e == nil {
		out.LastInsertID = id
	}
	return out, nil
}

func (q *querier) Query(ctx context.Context, sqlText string, args ...any) (dbdriver.ResultSet, error) {
	if q == nil || q.exec == nil {
		return nil, fmt.Errorf("dmdrv: querier not initialized")
	}
	rows, err := q.exec.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	rs, err := scanner.NewSQLResultSet(ctx, rows, dialect{})
	if err != nil {
		return nil, err
	}
	return rs, nil
}

func (q *querier) Explain(ctx context.Context, sqlText string) (dbdriver.ResultSet, error) {
	return q.Query(ctx, "EXPLAIN "+sqlText)
}
