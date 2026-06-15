package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"catdb/internal/core/session"
	"catdb/internal/dbdriver"
	"catdb/internal/registry"
)

// DefaultQueryTimeout caps any single RunQuery / Exec call. Streaming a
// huge SELECT still finishes within this — the back-end fetches in batches
// of bounded size, not all at once.
const DefaultQueryTimeout = 60 * time.Second

// DefaultBatchSize is what RunQuery uses for the first batch and FetchMore
// defaults to.
const DefaultBatchSize = 500

// MaxPreviewRows is the safety net at the FRONT of the pipeline. The Service
// will refuse to keep a result set open beyond this many rows — past it we
// require the user to switch to the export path (M4) which streams to disk.
const MaxPreviewRows = 10000

// QueryService is the IPC entry point for SQL execution. It is THIN: parses
// inputs, picks the right driver via session.Manager, and forwards. Streaming
// state (open ResultSets) lives in its private registry keyed by handle.
type QueryService struct {
	mgr *session.Manager

	mu      sync.Mutex
	handles map[string]*openQuery
}

// NewQueryService wires the dependency.
func NewQueryService(mgr *session.Manager) *QueryService {
	return &QueryService{
		mgr:     mgr,
		handles: make(map[string]*openQuery),
	}
}

func (s *QueryService) ServiceName() string { return "QueryService" }

// ServiceShutdown is invoked by Wails on app shutdown — close every dangling
// result set so we don't leak server cursors.
func (s *QueryService) ServiceShutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, h := range s.handles {
		_ = h.rs.Close()
		releaseTx(h.tx, nil)
		delete(s.handles, id)
	}
	return nil
}

type openQuery struct {
	connID    string
	sql       string
	rs        dbdriver.ResultSet
	columns   []dbdriver.ColumnMeta
	rowsRead  int
	done      bool
	createdAt time.Time
	// tx is non-nil when the query was launched against a default schema
	// (Connection.Begin + USE). It pins the underlying connection so the
	// streaming ResultSet sees the right current-database, and must be
	// committed/rolled back when the handle is closed.
	tx dbdriver.Tx
}

// QueryOptions tweaks one call's behaviour. All fields optional.
type QueryOptions struct {
	BatchSize     int    `json:"batchSize,omitempty"`     // first-batch size (default 500)
	TimeoutMs     int    `json:"timeoutMs,omitempty"`     // per-call ctx timeout (default 60s)
	MaxRows       int    `json:"maxRows,omitempty"`       // hard cap for the open handle (default MaxPreviewRows)
	DefaultSchema string `json:"defaultSchema,omitempty"` // when non-empty, the SQL is run with this database
	// "selected" (e.g. MySQL `USE db`) so unqualified tables resolve to it.
}

// QueryRunResult is what RunQuery / Explain return to the front-end.
//
//   - When Done=true, Handle is empty (the result fit in one batch); the
//     front-end need not call FetchMore or Close.
//   - When Done=false, Handle is non-empty and the front-end must eventually
//     call Close to release the cursor.
type QueryRunResult struct {
	Handle    string                `json:"handle,omitempty"`
	Columns   []dbdriver.ColumnMeta `json:"columns"`
	Rows      [][]any               `json:"rows"`
	RowsTotal int                   `json:"rowsTotal"` // running total of rows returned so far
	Done      bool                  `json:"done"`
	Truncated bool                  `json:"truncated"` // hit MaxRows; cursor closed
	ElapsedMs int64                 `json:"elapsedMs"`
	// IsResultSet=true means the SQL returned rows. False means it was an
	// Exec-style statement and the caller should look at ExecResult instead.
	IsResultSet bool                 `json:"isResultSet"`
	ExecResult  *dbdriver.ExecResult `json:"execResult,omitempty"`
}

// QueryBatchResult is what FetchMore returns.
type QueryBatchResult struct {
	Rows      [][]any `json:"rows"`
	RowsTotal int     `json:"rowsTotal"`
	Done      bool    `json:"done"`
	Truncated bool    `json:"truncated"`
}

// RunQuery executes sqlText against connID's live Connection, returning the
// first batch + column metadata. Behaviour:
//   - SELECT-like (rows): keeps the cursor open, returns Handle for paging.
//   - Non-SELECT: returns ExecResult, no Handle.
//   - Cancellation: the Wails CancellablePromise on the front-end cancels
//     ctx; QueryContext aborts the query server-side.
//   - Timeout: capped at QueryOptions.TimeoutMs (default 60s).
func (s *QueryService) RunQuery(ctx context.Context, connID, sqlText string, opts QueryOptions) (QueryRunResult, error) {
	var empty QueryRunResult
	if connID == "" {
		return empty, fmt.Errorf("QueryService: connID is required")
	}
	if strings.TrimSpace(sqlText) == "" {
		return empty, fmt.Errorf("QueryService: sql is empty")
	}

	conn, err := s.mgr.Get(connID)
	if err != nil {
		// Try opening it lazily so the user doesn't see an opaque "not open"
		// error if they Ran from a freshly-saved connection.
		conn, err = s.mgr.Open(ctx, connID)
		if err != nil {
			return empty, err
		}
	}

	timeout := time.Duration(opts.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = DefaultQueryTimeout
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	q, tx, err := s.acquireQuerier(tctx, conn, connID, opts.DefaultSchema)
	if err != nil {
		return empty, classifyErr(err, tctx)
	}

	start := time.Now()

	if !looksLikeRowsQuery(sqlText) {
		// Exec path: INSERT/UPDATE/DELETE/DDL.
		res, err := q.Exec(tctx, sqlText)
		if err != nil {
			releaseTx(tx, err)
			return empty, classifyErr(err, tctx)
		}
		releaseTx(tx, nil)
		return QueryRunResult{
			ElapsedMs:   time.Since(start).Milliseconds(),
			IsResultSet: false,
			ExecResult:  &res,
			Done:        true,
		}, nil
	}

	rs, err := q.Query(tctx, sqlText)
	if err != nil {
		releaseTx(tx, err)
		return empty, classifyErr(err, tctx)
	}

	batch := opts.BatchSize
	if batch <= 0 {
		batch = DefaultBatchSize
	}
	maxRows := opts.MaxRows
	if maxRows <= 0 {
		maxRows = MaxPreviewRows
	}

	rows, done, err := rs.Next(batch)
	if err != nil {
		_ = rs.Close()
		releaseTx(tx, err)
		return empty, classifyErr(err, tctx)
	}

	cols := rs.Columns()
	out := QueryRunResult{
		Columns:     cols,
		Rows:        rows,
		RowsTotal:   len(rows),
		Done:        done,
		ElapsedMs:   time.Since(start).Milliseconds(),
		IsResultSet: true,
	}

	if !done && len(rows) >= maxRows {
		out.Truncated = true
		out.Done = true
		_ = rs.Close()
		releaseTx(tx, nil)
		return out, nil
	}

	if done {
		_ = rs.Close()
		releaseTx(tx, nil)
		return out, nil
	}

	// Keep the cursor open for FetchMore.
	h := s.makeHandle()
	s.mu.Lock()
	s.handles[h] = &openQuery{
		connID:    connID,
		sql:       sqlText,
		rs:        rs,
		columns:   cols,
		rowsRead:  len(rows),
		createdAt: time.Now(),
		tx:        tx,
	}
	s.mu.Unlock()
	out.Handle = h
	return out, nil
}

// FetchMore reads the next batch from an open handle. Once Done=true the
// handle is closed automatically — the front-end should drop its reference.
func (s *QueryService) FetchMore(ctx context.Context, handle string, batch int) (QueryBatchResult, error) {
	var empty QueryBatchResult
	if handle == "" {
		return empty, fmt.Errorf("QueryService: handle is required")
	}
	s.mu.Lock()
	h, ok := s.handles[handle]
	s.mu.Unlock()
	if !ok {
		return empty, fmt.Errorf("QueryService: unknown handle %s", handle)
	}
	if h.done {
		return QueryBatchResult{Done: true, RowsTotal: h.rowsRead}, nil
	}
	if batch <= 0 {
		batch = DefaultBatchSize
	}

	tctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	rows, done, err := h.rs.Next(batch)
	if err != nil {
		_ = h.rs.Close()
		releaseTx(h.tx, err)
		s.dropHandle(handle)
		return empty, classifyErr(err, tctx)
	}
	h.rowsRead += len(rows)
	h.done = done
	out := QueryBatchResult{
		Rows:      rows,
		RowsTotal: h.rowsRead,
		Done:      done,
	}
	if !done && h.rowsRead >= MaxPreviewRows {
		out.Truncated = true
		out.Done = true
		_ = h.rs.Close()
		releaseTx(h.tx, nil)
		s.dropHandle(handle)
		return out, nil
	}
	if done {
		_ = h.rs.Close()
		releaseTx(h.tx, nil)
		s.dropHandle(handle)
	}
	return out, nil
}

// Close releases a handle and its underlying cursor. Idempotent.
func (s *QueryService) Close(_ context.Context, handle string) error {
	s.mu.Lock()
	h, ok := s.handles[handle]
	if ok {
		delete(s.handles, handle)
	}
	s.mu.Unlock()
	if !ok {
		return nil
	}
	err := h.rs.Close()
	releaseTx(h.tx, err)
	return err
}

// Explain runs EXPLAIN against the SQL and returns the entire plan inline.
// Caps the rows to DefaultBatchSize — EXPLAIN plans are never huge.
//
// Gated by Capabilities.ExplainPlan (the front-end hides the button when the
// driver doesn't support it).
func (s *QueryService) Explain(ctx context.Context, connID, sqlText string, opts QueryOptions) (QueryRunResult, error) {
	var empty QueryRunResult
	if connID == "" {
		return empty, fmt.Errorf("QueryService: connID is required")
	}
	if strings.TrimSpace(sqlText) == "" {
		return empty, fmt.Errorf("QueryService: sql is empty")
	}

	conn, err := s.mgr.Get(connID)
	if err != nil {
		conn, err = s.mgr.Open(ctx, connID)
		if err != nil {
			return empty, err
		}
	}

	tctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	q, tx, err := s.acquireQuerier(tctx, conn, connID, opts.DefaultSchema)
	if err != nil {
		return empty, classifyErr(err, tctx)
	}

	start := time.Now()
	rs, err := q.Explain(tctx, sqlText)
	if err != nil {
		releaseTx(tx, err)
		return empty, classifyErr(err, tctx)
	}
	defer rs.Close()
	rows, _, err := rs.Next(DefaultBatchSize)
	if err != nil {
		releaseTx(tx, err)
		return empty, classifyErr(err, tctx)
	}
	releaseTx(tx, nil)
	return QueryRunResult{
		Columns:     rs.Columns(),
		Rows:        rows,
		RowsTotal:   len(rows),
		Done:        true,
		ElapsedMs:   time.Since(start).Milliseconds(),
		IsResultSet: true,
	}, nil
}

// CapabilitiesFor returns the registered driver's Capabilities for the given
// connID, so the front-end can render UI gates without going through the
// active connection.
func (s *QueryService) CapabilitiesFor(_ context.Context, driverName string) (dbdriver.Capabilities, error) {
	d, err := registry.Get(driverName)
	if err != nil {
		return dbdriver.Capabilities{}, err
	}
	return d.Capabilities(), nil
}

// --- internals ---

// acquireQuerier returns the Querier the caller should run their SQL through.
// If schema is empty it is just the pool-level Querier. Otherwise we open a
// transaction so we hold a single physical connection, run `USE schema` on it,
// and return the Tx as the Querier — that way unqualified table names in the
// caller's SQL resolve against `schema`, and streaming ResultSets continue to
// see the same default-database for the life of the handle.
//
// The returned Tx is nil when schema is empty. Callers MUST eventually call
// releaseTx on it (with either nil err for commit, or non-nil for rollback)
// so the underlying connection returns to the pool.
func (s *QueryService) acquireQuerier(
	ctx context.Context,
	conn dbdriver.Connection,
	connID, schema string,
) (dbdriver.Querier, dbdriver.Tx, error) {
	if schema == "" {
		q := conn.Querier()
		if q == nil {
			return nil, nil, fmt.Errorf("QueryService: connection has no querier")
		}
		return q, nil, nil
	}

	driverName, err := s.mgr.DriverName(ctx, connID)
	if err != nil {
		return nil, nil, fmt.Errorf("QueryService: resolve driver: %w", err)
	}
	d, err := registry.Get(driverName)
	if err != nil {
		return nil, nil, fmt.Errorf("QueryService: resolve driver: %w", err)
	}

	tx, err := conn.Begin(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("QueryService: begin tx for default schema: %w", err)
	}
	quoted := d.Dialect().QuoteIdentifier(schema)
	if _, err := tx.Exec(ctx, "USE "+quoted); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	return tx, tx, nil
}

// releaseTx commits the tx when runErr is nil and rolls back otherwise. The
// implicit-commit a DDL statement performs in MySQL leaves the tx in a
// finished state — sql.ErrTxDone is therefore treated as success here so
// callers don't see spurious errors after a successful CREATE/ALTER/DROP.
func releaseTx(tx dbdriver.Tx, runErr error) {
	if tx == nil {
		return
	}
	if runErr != nil {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			// Best-effort: rollback failure on an already-errored path is
			// not worth surfacing.
		}
		return
	}
	if err := tx.Commit(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		// Same: commit failures on the happy path are rare and the user
		// already has their result; logging would be the right answer
		// once we wire up structured logging.
	}
}

func (s *QueryService) makeHandle() string {
	return "q-" + uuid.NewString()
}

func (s *QueryService) dropHandle(h string) {
	s.mu.Lock()
	delete(s.handles, h)
	s.mu.Unlock()
}

// looksLikeRowsQuery is a cheap front-of-pipeline heuristic: SELECT / WITH /
// SHOW / DESCRIBE / EXPLAIN / TABLE … return rows. Anything else is treated
// as an Exec. False positives are recoverable — the driver tells us if Query
// fails — but this saves us from having to call Query() and then degrade.
func looksLikeRowsQuery(s string) bool {
	for _, ch := range s {
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '(' {
			continue
		}
		// Move to a word boundary and compare the leading token.
		s = strings.TrimLeftFunc(s, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '('
		})
		break
	}
	upper := strings.ToUpper(s)
	for _, kw := range []string{"SELECT", "WITH", "SHOW", "DESC", "EXPLAIN", "TABLE", "VALUES", "PRAGMA"} {
		if strings.HasPrefix(upper, kw) {
			return true
		}
	}
	return false
}

func classifyErr(err error, ctx context.Context) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return fmt.Errorf("canceled: %w", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("timeout: %w", err)
	}
	return err
}
