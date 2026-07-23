package services

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"catdb/internal/core/session"
	"catdb/internal/core/sqlscript"
	"catdb/internal/dbdriver"
	"catdb/internal/registry"
)

// TableRef identifies a single table that the result rows map to, enabling
// inline editing on query results. nil means the query is too complex for
// safe inline editing (multi-table, aggregate, etc.).
type TableRef struct {
	DB    string `json:"db"`
	Table string `json:"table"`
}

// DefaultQueryTimeout caps any single RunQuery / Exec call. Streaming a
// huge SELECT still finishes within this — the back-end fetches in batches
// of bounded size, not all at once.
const DefaultQueryTimeout = 60 * time.Second

// DefaultBatchSize is what RunQuery uses for the first batch and FetchMore
// defaults to.
const DefaultBatchSize = 500

// QueryService is the IPC entry point for SQL execution. It is THIN: parses
// inputs, picks the right driver via session.Manager, and forwards. Streaming
// state (open ResultSets) lives in its private registry keyed by handle.
type QueryService struct {
	mgr *session.Manager

	mu      sync.Mutex
	handles map[string]*openQuery

	txnMu sync.Mutex
	txns  map[string]*openTxn
}

// NewQueryService wires the dependency.
func NewQueryService(mgr *session.Manager) *QueryService {
	return &QueryService{
		mgr:     mgr,
		handles: make(map[string]*openQuery),
		txns:    make(map[string]*openTxn),
	}
}

func (s *QueryService) ServiceName() string { return "QueryService" }

// ServiceShutdown is invoked by Wails on app shutdown — close every dangling
// result set so we don't leak server cursors.
func (s *QueryService) ServiceShutdown() error {
	s.mu.Lock()
	for id, h := range s.handles {
		_ = h.rs.Close()
		releaseTx(h.tx, nil)
		if h.cancel != nil {
			h.cancel()
		}
		delete(s.handles, id)
	}
	s.mu.Unlock()

	s.txnMu.Lock()
	for id, t := range s.txns {
		_ = t.tx.Rollback()
		delete(s.txns, id)
	}
	s.txnMu.Unlock()
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
	// cancel tears down the context the streaming ResultSet (and tx) were
	// created with. It is decoupled from the RunQuery call's ctx so the cursor
	// survives after RunQuery returns; it must be called on every teardown
	// path so we don't leak the context's watcher goroutine.
	cancel context.CancelFunc
	// maxRows caps how many rows FetchMore will hand out before force-closing
	// the cursor (Truncated). 0 means unlimited — drain the whole result set.
	maxRows int
}

// openTxn is an explicit transaction started by BeginTransaction. It wraps a
// dbdriver.Tx and is stored by ID so subsequent RunQuery calls can be directed
// into it.
type openTxn struct {
	connID string
	tx     dbdriver.Tx
}

// QueryOptions tweaks one call's behaviour. All fields optional.
type QueryOptions struct {
	BatchSize     int    `json:"batchSize,omitempty"`     // first-batch size (default 500)
	TimeoutMs     int    `json:"timeoutMs,omitempty"`     // per-call ctx timeout (default 60s)
	MaxRows       int    `json:"maxRows,omitempty"`       // hard cap for the open handle (0 = unlimited)
	DefaultSchema string `json:"defaultSchema,omitempty"` // when non-empty, the SQL is run with this database
	// "selected" (e.g. MySQL `USE db`) so unqualified tables resolve to it.
	// DefaultDatabase routes the SQL to this database's session on drivers
	// whose databases are isolation boundaries (Postgres). Empty = the
	// connection's default database.
	DefaultDatabase string `json:"defaultDatabase,omitempty"`
	TxnID           string `json:"txnId,omitempty"` // when non-empty, the query runs inside the referenced transaction
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
	// StatementCount is how many statements the submitted script was split into.
	StatementCount int `json:"statementCount"`
	// EditTable is non-nil when the result targets a single identifiable table.
	// The front-end uses this to enable inline editing on the result grid.
	EditTable *TableRef `json:"editTable,omitempty"`
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

	// Split the script into statements client-side (honoring quotes, comments,
	// and the DELIMITER directive per the driver's lexical rules) — the same
	// job the mysql CLI does. The server never understands DELIMITER, and the
	// driver runs one statement at a time, so a multi-statement script must be
	// chopped here. Only the final statement's result is streamed back;
	// leading ones are run and discarded.
	rules, err := s.scriptRulesFor(ctx, connID)
	if err != nil {
		return empty, err
	}
	stmts := sqlscript.Split(sqlText, rules)
	if len(stmts) == 0 {
		return empty, fmt.Errorf("QueryService: sql is empty")
	}
	final := stmts[len(stmts)-1]

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

	// runCtx drives the initial execution. It is decoupled from the per-call
	// ctx's lifetime: Wails cancels the call ctx when RunQuery returns, and a
	// kept-open streaming ResultSet (and its tx) are bound to whatever context
	// created them — so binding them to the call ctx would tear the cursor down
	// the instant we return the handle. During the initial run we still honor
	// frontend cancellation and the timeout by cancelling runCtx when the
	// caller's ctx or the deadline fires (via AfterFunc); once we hand the
	// cursor to a handle we detach that link and transfer runCancel to Close.
	runCtx, runCancel := context.WithCancel(context.Background())
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, timeout)
	stopLink := context.AfterFunc(timeoutCtx, runCancel)
	// detach abandons the link + timer without cancelling runCtx (kept-open
	// path); teardown also cancels runCtx (every other path).
	detach := func() {
		stopLink()
		timeoutCancel()
	}
	teardown := func() {
		detach()
		runCancel()
	}

	q, tx, err := s.acquireQuerier(runCtx, context.Background(), conn, connID, opts.DefaultDatabase, opts.DefaultSchema, opts.TxnID)
	if err != nil {
		cerr := classifyErr(err, timeoutCtx)
		teardown()
		return empty, cerr
	}

	start := time.Now()

	// Run every statement before the last; their results are discarded. Exec
	// drives any kind of statement (SELECT included — its rows are ignored).
	for _, st := range stmts[:len(stmts)-1] {
		if _, err := q.Exec(runCtx, st); err != nil {
			cerr := classifyErr(err, timeoutCtx)
			releaseTx(tx, err)
			teardown()
			return empty, cerr
		}
	}

	if !looksLikeRowsQuery(final) {
		// Exec path: INSERT/UPDATE/DELETE/DDL.
		res, err := q.Exec(runCtx, final)
		if err != nil {
			cerr := classifyErr(err, timeoutCtx)
			releaseTx(tx, err)
			teardown()
			return empty, cerr
		}
		releaseTx(tx, nil)
		teardown()
		return QueryRunResult{
			ElapsedMs:      time.Since(start).Milliseconds(),
			IsResultSet:    false,
			ExecResult:     &res,
			Done:           true,
			StatementCount: len(stmts),
		}, nil
	}

	rs, err := q.Query(runCtx, final)
	if err != nil {
		cerr := classifyErr(err, timeoutCtx)
		releaseTx(tx, err)
		teardown()
		return empty, cerr
	}

	batch := opts.BatchSize
	if batch <= 0 {
		batch = DefaultBatchSize
	}
	// maxRows == 0 means unlimited: the caller drains the whole result set
	// (the SQL editor loads full data with no preview cap). A positive cap is
	// still honored for callers that want a bounded preview.
	maxRows := opts.MaxRows

	rows, done, err := rs.Next(batch)
	if err != nil {
		cerr := classifyErr(err, timeoutCtx)
		_ = rs.Close()
		releaseTx(tx, err)
		teardown()
		return empty, cerr
	}

	cols := rs.Columns()
	out := QueryRunResult{
		Columns:        cols,
		Rows:           rows,
		RowsTotal:      len(rows),
		Done:           done,
		ElapsedMs:      time.Since(start).Milliseconds(),
		IsResultSet:    true,
		StatementCount: len(stmts),
	}
	out.EditTable = extractTableRef(final, opts.DefaultSchema)

	if maxRows > 0 && !done && len(rows) >= maxRows {
		out.Truncated = true
		out.Done = true
		_ = rs.Close()
		releaseTx(tx, nil)
		teardown()
		return out, nil
	}

	if done {
		_ = rs.Close()
		releaseTx(tx, nil)
		teardown()
		return out, nil
	}

	// Keep the cursor open for FetchMore. Detach runCtx from the per-call
	// ctx/timeout and transfer runCancel to the handle's Close.
	detach()
	h := s.makeHandle()
	s.mu.Lock()
	s.handles[h] = &openQuery{
		connID:    connID,
		sql:       final,
		rs:        rs,
		columns:   cols,
		rowsRead:  len(rows),
		createdAt: time.Now(),
		tx:        tx,
		cancel:    runCancel,
		maxRows:   maxRows,
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

	// The streaming rows are bound to the handle's own context, not this call's
	// ctx (see RunQuery). Bridge frontend cancellation / a per-batch timeout to
	// it by cancelling the cursor when this call's ctx fires; detach on a normal
	// batch so the cursor stays open for the next scroll.
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	stop := context.AfterFunc(timeoutCtx, func() {
		if h.cancel != nil {
			h.cancel()
		}
	})
	defer timeoutCancel()
	defer stop()

	rows, done, err := h.rs.Next(batch)
	if err != nil {
		cerr := classifyErr(err, timeoutCtx)
		s.closeHandle(handle, h, err)
		return empty, cerr
	}
	h.rowsRead += len(rows)
	h.done = done
	out := QueryBatchResult{
		Rows:      rows,
		RowsTotal: h.rowsRead,
		Done:      done,
	}
	if h.maxRows > 0 && !done && h.rowsRead >= h.maxRows {
		out.Truncated = true
		out.Done = true
		s.closeHandle(handle, h, nil)
		return out, nil
	}
	if done {
		s.closeHandle(handle, h, nil)
	}
	return out, nil
}

// closeHandle tears down an open query: closes the cursor, releases the tx
// (commit on runErr==nil, rollback otherwise), cancels the handle's context,
// and removes it from the registry. Safe to call once per handle.
func (s *QueryService) closeHandle(id string, h *openQuery, runErr error) {
	_ = h.rs.Close()
	releaseTx(h.tx, runErr)
	if h.cancel != nil {
		h.cancel()
	}
	s.dropHandle(id)
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
	if h.cancel != nil {
		h.cancel()
	}
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

	// EXPLAIN targets a single statement — use the last one in the script
	// (typically the user's selection) so a DELIMITER-prefixed script doesn't
	// reach the server verbatim.
	rules, err := s.scriptRulesFor(ctx, connID)
	if err != nil {
		return empty, err
	}
	stmts := sqlscript.Split(sqlText, rules)
	if len(stmts) == 0 {
		return empty, fmt.Errorf("QueryService: sql is empty")
	}
	final := stmts[len(stmts)-1]

	conn, err := s.mgr.Get(connID)
	if err != nil {
		conn, err = s.mgr.Open(ctx, connID)
		if err != nil {
			return empty, err
		}
	}

	tctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	q, tx, err := s.acquireQuerier(tctx, context.Background(), conn, connID, opts.DefaultDatabase, opts.DefaultSchema, opts.TxnID)
	if err != nil {
		return empty, classifyErr(err, tctx)
	}

	start := time.Now()
	rs, err := q.Explain(tctx, final)
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
		Columns:        rs.Columns(),
		Rows:           rows,
		RowsTotal:      len(rows),
		Done:           true,
		ElapsedMs:      time.Since(start).Milliseconds(),
		IsResultSet:    true,
		StatementCount: 1,
	}, nil
}

// CountQuery returns the total row count of the (final) statement in sqlText
// by wrapping it in SELECT COUNT(*) FROM (…) AS a derived table. The editor
// fires it alongside a streaming RunQuery so the result grid can show
// "N / total" before the drain finishes. Only statements that can legally sit
// in a derived table are countable (SELECT / WITH / TABLE / VALUES); anything
// else errors and the front-end treats the total as unknown.
//
// The count always runs on the pooled connection, never inside an open
// user transaction — a Tx serializes on one physical connection, which the
// concurrently-streaming cursor may be holding.
func (s *QueryService) CountQuery(ctx context.Context, connID, sqlText string, opts QueryOptions) (int64, error) {
	if connID == "" {
		return 0, fmt.Errorf("QueryService: connID is required")
	}
	rules, err := s.scriptRulesFor(ctx, connID)
	if err != nil {
		return 0, err
	}
	stmts := sqlscript.Split(sqlText, rules)
	if len(stmts) == 0 {
		return 0, fmt.Errorf("QueryService: sql is empty")
	}
	final := strings.TrimSuffix(strings.TrimSpace(stmts[len(stmts)-1]), ";")
	if !isCountableQuery(final) {
		return 0, fmt.Errorf("QueryService: statement is not countable")
	}

	conn, err := s.mgr.Get(connID)
	if err != nil {
		conn, err = s.mgr.Open(ctx, connID)
		if err != nil {
			return 0, err
		}
	}
	timeout := time.Duration(opts.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = DefaultQueryTimeout
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	q, tx, err := s.acquireQuerier(tctx, tctx, conn, connID, opts.DefaultDatabase, opts.DefaultSchema, "")
	if err != nil {
		return 0, err
	}

	// Unquoted alias on purpose: plain lowercase identifier, valid in every
	// dialect — no driver-specific quoting in this generic layer (铁律 12).
	rs, err := q.Query(tctx, "SELECT COUNT(*) FROM (\n"+final+"\n) AS __catdb_count")
	if err != nil {
		releaseTx(tx, err)
		return 0, classifyErr(err, tctx)
	}
	rows, _, err := rs.Next(1)
	_ = rs.Close()
	releaseTx(tx, err)
	if err != nil {
		return 0, classifyErr(err, tctx)
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return 0, fmt.Errorf("QueryService: count returned no rows")
	}
	return scalarToInt64(rows[0][0])
}

// lockingReadRe matches locking-read clauses. The scan can false-positive on
// a string literal containing the words; skipping the count is the safe
// direction, so we don't bother tokenizing.
var lockingReadRe = regexp.MustCompile(`(?i)\bFOR\s+UPDATE\b|\bFOR\s+SHARE\b|\bLOCK\s+IN\s+SHARE\s+MODE\b`)

// isCountableQuery reports whether the statement can be wrapped as a derived
// table for COUNT(*). SHOW/EXPLAIN/DESC return rows but are not valid inside
// a subquery. Locking reads are excluded: MySQL 8 allows FOR UPDATE inside a
// derived table, so the count would re-run the full locking scan next to the
// user's own query.
func isCountableQuery(s string) bool {
	s = stripLeadingComments(s)
	upper := strings.ToUpper(strings.TrimLeft(s, " \t\n\r("))
	for _, kw := range []string{"SELECT", "WITH", "TABLE", "VALUES"} {
		if strings.HasPrefix(upper, kw) {
			return !lockingReadRe.MatchString(s)
		}
	}
	return false
}

// stripLeadingComments drops -- / # line comments and /* */ block comments
// before the first real token, so a SELECT under a leading comment is still
// recognized as countable. (The comments stay in the wrapped SQL — they are
// legal inside a derived table.)
func stripLeadingComments(s string) string {
	for {
		s = strings.TrimLeft(s, " \t\n\r")
		switch {
		case strings.HasPrefix(s, "--"), strings.HasPrefix(s, "#"):
			i := strings.IndexByte(s, '\n')
			if i < 0 {
				return ""
			}
			s = s[i+1:]
		case strings.HasPrefix(s, "/*"):
			i := strings.Index(s, "*/")
			if i < 0 {
				return ""
			}
			s = s[i+2:]
		default:
			return s
		}
	}
}

// scalarToInt64 normalizes the driver's COUNT(*) scalar representation.
func scalarToInt64(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case uint64:
		return int64(x), nil
	case []byte:
		return strconv.ParseInt(string(x), 10, 64)
	case string:
		return strconv.ParseInt(x, 10, 64)
	default:
		return 0, fmt.Errorf("QueryService: unexpected count type %T", v)
	}
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

// BeginTransaction opens a new transaction on the connection. Returns a
// transaction ID the front-end passes to RunQuery (via QueryOptions.TxnID),
// CommitTransaction, or RollbackTransaction.
func (s *QueryService) BeginTransaction(ctx context.Context, connID string, db string) (string, error) {
	conn, err := s.mgr.Get(connID)
	if err != nil {
		conn, err = s.mgr.Open(ctx, connID)
		if err != nil {
			return "", err
		}
	}
	// db routes the transaction to the addressed database on drivers whose
	// databases are isolation boundaries (Postgres); "" = connection default.
	tx, err := dbdriver.RouteBegin(context.Background(), conn, db, nil)
	if err != nil {
		return "", fmt.Errorf("QueryService: begin tx: %w", err)
	}

	id := "txn-" + uuid.NewString()
	s.txnMu.Lock()
	s.txns[id] = &openTxn{connID: connID, tx: tx}
	s.txnMu.Unlock()

	return id, nil
}

// CommitTransaction commits the referenced transaction and releases it.
func (s *QueryService) CommitTransaction(_ context.Context, txnID string) error {
	s.txnMu.Lock()
	t, ok := s.txns[txnID]
	if ok {
		delete(s.txns, txnID)
	}
	s.txnMu.Unlock()
	if !ok {
		return fmt.Errorf("QueryService: unknown transaction %s", txnID)
	}
	return t.tx.Commit()
}

// RollbackTransaction rolls back the referenced transaction and releases it.
func (s *QueryService) RollbackTransaction(_ context.Context, txnID string) error {
	s.txnMu.Lock()
	t, ok := s.txns[txnID]
	if ok {
		delete(s.txns, txnID)
	}
	s.txnMu.Unlock()
	if !ok {
		return fmt.Errorf("QueryService: unknown transaction %s", txnID)
	}
	return t.tx.Rollback()
}

// IsTransactionActive returns true when the referenced transaction exists.
func (s *QueryService) IsTransactionActive(_ context.Context, txnID string) (bool, error) {
	s.txnMu.Lock()
	defer s.txnMu.Unlock()
	_, ok := s.txns[txnID]
	return ok, nil
}

// --- internals ---

// acquireQuerier returns the Querier the caller should run their SQL through.
// db routes to the addressed database's session on drivers whose databases
// are isolation boundaries (dbdriver.DatabaseRouter); "" = the connection's
// default. If schema is empty the result is just the (routed) pool-level
// Querier. Otherwise we open a transaction so we hold a single physical
// connection, run the dialect's default-namespace statement on it, and return
// the Tx as the Querier — that way unqualified table names in the caller's
// SQL resolve against `schema`, and streaming ResultSets continue to see the
// same default namespace for the life of the handle.
//
// The returned Tx is nil when schema is empty. Callers MUST eventually call
// releaseTx on it (with either nil err for commit, or non-nil for rollback)
// so the underlying connection returns to the pool.
//
// beginCtx bounds the pinned tx itself: Begin blocks when the pool is
// exhausted, and database/sql rolls the tx back when its context dies.
// Streaming callers whose tx must outlive the call pass context.Background();
// short-lived callers (Count) pass their request context so both the Begin
// and the tx obey cancellation/timeout.
func (s *QueryService) acquireQuerier(
	ctx, beginCtx context.Context,
	conn dbdriver.Connection,
	connID, db, schema, txnID string,
) (dbdriver.Querier, dbdriver.Tx, error) {
	// If a transaction is specified, use its Querier directly (no tx to release).
	if txnID != "" {
		s.txnMu.Lock()
		t, ok := s.txns[txnID]
		s.txnMu.Unlock()
		if !ok {
			return nil, nil, fmt.Errorf("QueryService: unknown transaction %s", txnID)
		}
		// Set the default schema on the transaction's connection when needed.
		if schema != "" {
			d, err := s.driverFor(ctx, connID)
			if err != nil {
				return nil, nil, err
			}
			if stmt := d.Dialect().DefaultNamespaceSQL(schema); stmt != "" {
				if _, err := t.tx.Exec(ctx, stmt); err != nil {
					return nil, nil, err
				}
			}
		}
		return t.tx, nil, nil
	}

	if schema == "" {
		q, err := dbdriver.RouteQuerier(ctx, conn, db)
		if err != nil {
			return nil, nil, err
		}
		return q, nil, nil
	}

	d, err := s.driverFor(ctx, connID)
	if err != nil {
		return nil, nil, err
	}
	stmt := d.Dialect().DefaultNamespaceSQL(schema)
	if stmt == "" {
		// The database has no session-default statement; callers must qualify.
		q, err := dbdriver.RouteQuerier(ctx, conn, db)
		if err != nil {
			return nil, nil, err
		}
		return q, nil, nil
	}

	tx, err := dbdriver.RouteBegin(beginCtx, conn, db, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("QueryService: begin tx for default schema: %w", err)
	}
	if _, err := tx.Exec(ctx, stmt); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	return tx, tx, nil
}

// scriptRulesFor resolves the lexical script-splitting rules for connID's
// driver.
func (s *QueryService) scriptRulesFor(ctx context.Context, connID string) (dbdriver.ScriptRules, error) {
	d, err := s.driverFor(ctx, connID)
	if err != nil {
		return dbdriver.ScriptRules{}, err
	}
	return d.Dialect().ScriptRules(), nil
}

// driverFor resolves the registered driver behind a connection ID.
func (s *QueryService) driverFor(ctx context.Context, connID string) (dbdriver.Driver, error) {
	driverName, err := s.mgr.DriverName(ctx, connID)
	if err != nil {
		return nil, fmt.Errorf("QueryService: resolve driver: %w", err)
	}
	d, err := registry.Get(driverName)
	if err != nil {
		return nil, fmt.Errorf("QueryService: resolve driver: %w", err)
	}
	return d, nil
}

// releaseTx commits the tx when runErr is nil and rolls back otherwise. An
// implicit commit (e.g. DDL in MySQL) leaves the tx in a finished state —
// dbdriver.ErrTxDone is therefore treated as success here so callers don't
// see spurious errors after a successful CREATE/ALTER/DROP.
func releaseTx(tx dbdriver.Tx, runErr error) {
	if tx == nil {
		return
	}
	if runErr != nil {
		if err := tx.Rollback(); err != nil && !errors.Is(err, dbdriver.ErrTxDone) {
			// Best-effort: rollback failure on an already-errored path is
			// not worth surfacing.
		}
		return
	}
	if err := tx.Commit(); err != nil && !errors.Is(err, dbdriver.ErrTxDone) {
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

// extractTableRef heuristically extracts the single-table reference from a
// SELECT statement for inline editing, or nil if the query is too complex.
//
// ponytail: keyword scanner — false negatives just mean "no edit toolbar",
// which is safe. False positives are blocked by the conservative reject list.
func extractTableRef(sql, defaultSchema string) *TableRef {
	s := strings.TrimSpace(sql)
	if s == "" {
		return nil
	}
	upper := strings.ToUpper(s)

	// Reject multi-table / aggregate patterns.
	for _, kw := range []string{" JOIN ", " GROUP BY ", " HAVING ", " UNION ", " INTERSECT ", " EXCEPT ", " WITH "} {
		if strings.Contains(upper, kw) {
			return nil
		}
	}

	// Must start with SELECT (possibly parenthesized).
	trimmed := strings.TrimLeft(upper, " \t\n\r(")
	if !strings.HasPrefix(trimmed, "SELECT") {
		return nil
	}

	// Walk the SQL string tracking quote state to find FROM.
	fromPos := -1
	var inQ, inDQ, inBT bool
	for i := 0; i <= len(s)-4; i++ {
		switch s[i] {
		case '\'':
			if !inDQ && !inBT {
				inQ = !inQ
			}
		case '"':
			if !inQ && !inBT {
				inDQ = !inDQ
			}
		case '`':
			if !inQ && !inDQ {
				inBT = !inBT
			}
		}
		if inQ || inDQ || inBT {
			continue
		}
		if strings.HasPrefix(upper[i:], "FROM") && (i == 0 || !isIdentChar(s[i-1])) {
			ch := s[i+4]
			if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
				fromPos = i + 4
				break
			}
		}
	}
	if fromPos < 0 {
		return nil
	}

	j := skipWS(s, fromPos)
	if j >= len(s) {
		return nil
	}

	parts := readQualIdent(s, j)
	if len(parts) == 0 {
		return nil
	}

	table := parts[len(parts)-1]
	db := defaultSchema
	if len(parts) >= 2 {
		db = parts[0]
	}
	if db == "" {
		return nil
	}
	return &TableRef{DB: db, Table: table}
}

func isIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '$'
}

func skipWS(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

// readQualIdent reads a qualified SQL identifier at position i in s, handling
// backtick quoting and db.table dot notation. Returns up to 2 parts.
func readQualIdent(s string, i int) []string {
	var parts []string
	for len(parts) < 2 && i < len(s) {
		i = skipWS(s, i)
		if i >= len(s) {
			break
		}
		var ident string
		if s[i] == '`' {
			i++
			for i < len(s) && s[i] != '`' {
				ident += string(s[i])
				i++
			}
			if i < len(s) {
				i++ // skip closing backtick
			}
		} else {
			start := i
			for i < len(s) && isIdentChar(s[i]) {
				i++
			}
			if i == start {
				break
			}
			ident = s[start:i]
		}
		parts = append(parts, ident)

		// Check for dot separator (skip surrounding whitespace).
		i = skipWS(s, i)
		if i < len(s) && s[i] == '.' {
			i++
			continue
		}
		break
	}
	return parts
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
