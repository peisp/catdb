package services

import (
	"context"
	"fmt"

	"catdb/internal/core/session"
	"catdb/internal/dbdriver"
	"catdb/internal/registry"
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
func (s *EditService) GetPrimaryKey(ctx context.Context, connID, db, table string) ([]string, error) {
	_, ed, err := s.resolve(ctx, connID)
	if err != nil {
		return nil, err
	}
	return ed.PrimaryKeys(ctx, db, "", table)
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
	Table  string         `json:"table"`
	PK     map[string]any `json:"pk,omitempty"`
	Values map[string]any `json:"values,omitempty"`
}

// RowChangeResult reports what the driver did. RowsAffected==0 on UPDATE/
// DELETE is the front-end's "stale row" signal (optimistic-lock loss).
type RowChangeResult struct {
	RowsAffected int64  `json:"rowsAffected"`
	LastInsertID int64  `json:"lastInsertId,omitempty"`
	SQL          string `json:"sql"`  // the rendered SQL, for the editor's "history" panel
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

	dia, err := s.dialect(ctx, connID)
	if err != nil {
		return empty, err
	}
	tbl := tableQualified(dia, ch.DB, ch.Table)

	var (
		sqlText string
		args    []any
	)
	switch ch.Op {
	case "insert":
		sqlText, args, err = ed.BuildInsert(tbl, ch.Values)
	case "update":
		sqlText, args, err = ed.BuildUpdate(tbl, ch.PK, ch.Values)
	case "delete":
		sqlText, args, err = ed.BuildDelete(tbl, ch.PK)
	default:
		return empty, fmt.Errorf("EditService: unknown op %q", ch.Op)
	}
	if err != nil {
		return empty, err
	}

	q := conn.Querier()
	if q == nil {
		return empty, fmt.Errorf("EditService: connection has no querier")
	}
	res, err := q.Exec(ctx, sqlText, args...)
	if err != nil {
		return empty, err
	}
	return RowChangeResult{
		RowsAffected: res.RowsAffected,
		LastInsertID: res.LastInsertID,
		SQL:          sqlText,
	}, nil
}

// dialect resolves the connection's dialect for identifier quoting.
func (s *EditService) dialect(ctx context.Context, connID string) (dbdriver.Dialect, error) {
	name, err := s.mgr.DriverName(ctx, connID)
	if err != nil {
		return nil, err
	}
	d, err := registry.Get(name)
	if err != nil {
		return nil, err
	}
	return d.Dialect(), nil
}

// tableQualified formats db.table for the Editor's BuildXxx, using the
// driver's identifier-quoting rules. The Editor's own quoteTable already
// handles the dotted form; this helper just composes the string consistently.
func tableQualified(_ dbdriver.Dialect, db, table string) string {
	if db == "" {
		return table
	}
	return db + "." + table
}
