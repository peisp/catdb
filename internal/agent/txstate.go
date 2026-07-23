package agent

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"catdb/internal/dbdriver"
	"catdb/internal/storage"
)

// Task-transaction state (AGENT_DESIGN.md §5 gate 5): all DML of one write
// task runs inside a transaction on a DEDICATED connection; the user commits
// or rolls back after seeing the summary. While the tx is open, every
// run_sql of the session (reads included) routes through the same connection
// — otherwise the model's read-after-write verification would miss
// uncommitted rows. A pending tx blocks new SendMessage and auto-rolls back
// after an idle timeout (uncommitted transactions hold row locks).

const slugTxTimeout = "agent.tx-timeout"
const slugTxPending = "agent.tx-pending-block"

// txStmt is one executed statement awaiting commit/rollback.
type txStmt struct {
	SQL  string `json:"sql"`
	Rows int64  `json:"rows"`
}

type taskTx struct {
	sessID string
	connID string
	conn   dbdriver.Connection // dedicated physical connection, closed on finish
	tx     dbdriver.Tx
	mu     sync.Mutex
	stmts  []txStmt
	audit  []storage.AgentAuditEntry // buffered; written with final status on finish
	timer  *time.Timer
}

// txManager tracks at most one open task transaction per session.
type txManager struct {
	mu  sync.Mutex
	txs map[string]*taskTx
}

func newTxManager() *txManager { return &txManager{txs: map[string]*taskTx{}} }

func (m *txManager) get(sessID string) *taskTx {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.txs[sessID]
}

func (m *txManager) put(t *taskTx) {
	m.mu.Lock()
	m.txs[t.sessID] = t
	m.mu.Unlock()
}

// take removes and returns the session's tx (nil if none) — the caller owns
// finishing it.
func (m *txManager) take(sessID string) *taskTx {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.txs[sessID]
	delete(m.txs, sessID)
	return t
}

// openTaskTx starts a task transaction on a dedicated connection and arms the
// idle timer. onTimeout runs when the timer fires (engine rolls back + notifies).
func (e *Engine) openTaskTx(ctx context.Context, sessID, connID string) (*taskTx, error) {
	if e.txm.get(sessID) != nil {
		return nil, fmt.Errorf("agent: session %s already has an open transaction", sessID)
	}
	conn, err := e.dedicated(ctx, connID)
	if err != nil {
		return nil, fmt.Errorf("agent: open dedicated connection: %w", err)
	}
	// The task tx outlives this turn — the user commits/rolls back after the
	// summary — but database/sql auto-rolls a tx back the moment its BeginTx
	// ctx is canceled, and ctx here is the turn's (canceled when the stream
	// ends). Detach it: the tx's lifetime is owned by finishTx / the idle
	// timer, matching the manual-tx pattern in QueryService.
	tx, err := conn.Begin(context.WithoutCancel(ctx), nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("agent: begin task tx: %w", err)
	}
	t := &taskTx{sessID: sessID, connID: connID, conn: conn, tx: tx}
	timeout := e.txIdleTimeout(ctx)
	t.timer = time.AfterFunc(timeout, func() { e.txTimeout(sessID) })
	e.txm.put(t)
	return t, nil
}

// touch re-arms the idle timer (called on every statement executed in the tx).
func (t *taskTx) touch(d time.Duration) {
	t.mu.Lock()
	if t.timer != nil {
		t.timer.Reset(d)
	}
	t.mu.Unlock()
}

// record notes an executed statement and its buffered audit entry.
func (t *taskTx) record(s txStmt, a storage.AgentAuditEntry) {
	t.mu.Lock()
	t.stmts = append(t.stmts, s)
	t.audit = append(t.audit, a)
	t.mu.Unlock()
}

func (t *taskTx) statements() []txStmt {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]txStmt(nil), t.stmts...)
}

// CommitTx commits the session's pending task transaction and writes the
// buffered audit entries with status ok.
func (e *Engine) CommitTx(ctx context.Context, sessID string) error {
	return e.finishTx(ctx, sessID, true)
}

// RollbackTx rolls the pending task transaction back (audit status rolled-back).
func (e *Engine) RollbackTx(ctx context.Context, sessID string) error {
	return e.finishTx(ctx, sessID, false)
}

func (e *Engine) finishTx(ctx context.Context, sessID string, commit bool) error {
	t := e.txm.take(sessID)
	if t == nil {
		return fmt.Errorf("agent: session %s has no pending transaction", sessID)
	}
	t.mu.Lock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.mu.Unlock()
	defer t.conn.Close()

	var txErr error
	status := "rolled-back"
	if commit {
		if txErr = t.tx.Commit(); txErr == nil {
			status = "ok"
		} else {
			status = "error"
		}
	} else {
		txErr = t.tx.Rollback()
	}
	for _, a := range t.audit {
		a.Status = status
		if txErr != nil {
			a.Error = txErr.Error()
		}
		if _, err := e.store.AppendAgentAudit(ctx, a); err != nil {
			return fmt.Errorf("agent: write tx audit: %w", err)
		}
	}
	if txErr != nil && commit {
		return fmt.Errorf("agent: commit task tx: %w", txErr)
	}
	return nil
}

// txTimeout fires from the idle timer: roll back and tell the front-end.
func (e *Engine) txTimeout(sessID string) {
	if err := e.RollbackTx(context.Background(), sessID); err != nil {
		return // already finished by the user — nothing to do
	}
	e.emit("agent:error", map[string]any{
		"sessId": sessID, "slug": slugTxTimeout, "detail": "task transaction rolled back after idle timeout",
	})
}

// txIdleTimeout reads agent.limits.txIdleTimeoutSec (default 600s).
func (e *Engine) txIdleTimeout(ctx context.Context) time.Duration {
	if v := e.setting(ctx, "agent.limits.txIdleTimeoutSec"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 600 * time.Second
}
