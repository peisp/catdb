package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"catdb/internal/core/session"
	"catdb/internal/dbdriver"
)

// EditService is the IPC entry point for row-level editing on tables. The
// Service stays thin: input validation + driver dispatch. The safety
// invariants (no keyless writes, parameterized SQL) live in the driver's
// Editor.
type EditService struct {
	mgr *session.Manager
}

// NewEditService wires the session manager dependency.
func NewEditService(mgr *session.Manager) *EditService {
	return &EditService{mgr: mgr}
}

func (s *EditService) ServiceName() string { return "EditService" }

func (s *EditService) resolve(ctx context.Context, connID string) (dbdriver.Connection, dbdriver.Editor, error) {
	if connID == "" {
		return nil, nil, fmt.Errorf("EditService: connID is required")
	}
	conn, err := s.mgr.Get(connID)
	if err != nil {
		conn, err = s.mgr.Open(ctx, connID)
		if err != nil {
			return nil, nil, err
		}
	}
	ed := conn.Editor()
	if ed == nil {
		return nil, nil, fmt.Errorf("EditService: connection has no editor")
	}
	return conn, ed, nil
}

// GetPrimaryKey returns the primary-key columns (or chosen unique key) for a
// table. An empty result means the table is editable READ-ONLY: the front-end
// hides the row-edit affordances and shows a banner.
func (s *EditService) GetPrimaryKey(ctx context.Context, connID, db, schema, table string) ([]string, error) {
	_, ed, err := s.resolve(ctx, connID)
	if err != nil {
		return nil, err
	}
	return ed.PrimaryKeys(ctx, db, schema, table)
}

// RowChange is the front-end's request to mutate one row.
//
// Op selects the operation:
//
//	"insert" — uses Values, ignores PK.
//	"update" — uses PK to locate the row, Values are the new column values.
//	"delete" — uses PK only.
//
// PK must match the columns reported by GetPrimaryKey; the driver enforces
// "no keyless UPDATE/DELETE" by refusing an empty pk map.
type RowChange struct {
	Op     string         `json:"op"`
	DB     string         `json:"db"`
	Schema string         `json:"schema,omitempty"`
	Table  string         `json:"table"`
	PK     map[string]any `json:"pk,omitempty"`
	Values map[string]any `json:"values,omitempty"`
}

// RowChangeResult reports what the driver did. RowsAffected==0 on UPDATE/
// DELETE is the front-end's "stale row" signal (optimistic-lock loss).
type RowChangeResult struct {
	RowsAffected int64  `json:"rowsAffected"`
	LastInsertID int64  `json:"lastInsertId,omitempty"`
	SQL          string `json:"sql"` // the rendered SQL, for the editor's "history" panel
}

// ApplyChange executes a single RowChange via the driver's Editor. Returns
// the SQL it issued so the front-end can show the user what really ran.
func (s *EditService) ApplyChange(ctx context.Context, connID string, ch RowChange) (RowChangeResult, error) {
	var empty RowChangeResult
	conn, ed, err := s.resolve(ctx, connID)
	if err != nil {
		return empty, err
	}
	if ch.Table == "" {
		return empty, fmt.Errorf("EditService: table is required")
	}

	var (
		sqlText string
		args    []any
	)
	switch ch.Op {
	case "insert":
		sqlText, args, err = ed.BuildInsert(ch.DB, ch.Schema, ch.Table, ch.Values)
	case "update":
		sqlText, args, err = ed.BuildUpdate(ch.DB, ch.Schema, ch.Table, ch.PK, ch.Values)
	case "delete":
		sqlText, args, err = ed.BuildDelete(ch.DB, ch.Schema, ch.Table, ch.PK)
	default:
		return empty, fmt.Errorf("EditService: unknown op %q", ch.Op)
	}
	if err != nil {
		return empty, err
	}

	q, err := dbdriver.RouteQuerier(ctx, conn, ch.DB)
	if err != nil {
		return empty, err
	}
	res, err := q.Exec(ctx, sqlText, args...)
	if err != nil {
		return empty, err
	}
	return RowChangeResult{
		RowsAffected: res.RowsAffected,
		LastInsertID: res.LastInsertID,
		SQL:          interpolateSQL(sqlText, args),
	}, nil
}

// interpolateSQL replaces placeholders with display-safe formatted values.
// Both placeholder styles drivers emit are handled: positional "?" (MySQL)
// and numbered "$1…$n" (Postgres). For display only — never use the result
// for execution.
func interpolateSQL(sql string, args []any) string {
	if len(args) == 0 {
		return sql
	}
	var buf strings.Builder
	argIdx := 0
	for {
		i := strings.IndexAny(sql, "?$")
		if i < 0 || argIdx >= len(args) {
			buf.WriteString(sql)
			break
		}
		buf.WriteString(sql[:i])
		if sql[i] == '?' {
			buf.WriteString(sqlArgValue(args[argIdx]))
			sql = sql[i+1:]
			argIdx++
			continue
		}
		// "$n" — the digits carry the arg index; skip bare "$" (e.g. inside
		// dollar-quoted strings, which never appear in Editor-built SQL anyway).
		j := i + 1
		for j < len(sql) && sql[j] >= '0' && sql[j] <= '9' {
			j++
		}
		if j == i+1 {
			buf.WriteByte('$')
			sql = sql[i+1:]
			continue
		}
		n, err := strconv.Atoi(sql[i+1 : j])
		if err != nil || n < 1 || n > len(args) {
			buf.WriteString(sql[i:j])
		} else {
			buf.WriteString(sqlArgValue(args[n-1]))
			argIdx++
		}
		sql = sql[j:]
	}
	return buf.String()
}

// sqlArgValue formats a Go value as a SQL literal string for display.
func sqlArgValue(v any) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case uint64:
		return strconv.FormatUint(val, 10)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case bool:
		if val {
			return "1"
		}
		return "0"
	case string:
		return "'" + strings.ReplaceAll(val, "'", "''") + "'"
	case []byte:
		return "X'" + fmt.Sprintf("%x", val) + "'"
	default:
		return "'" + strings.ReplaceAll(fmt.Sprintf("%v", val), "'", "''") + "'"
	}
}
