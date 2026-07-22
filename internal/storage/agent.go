package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AgentSession is a persisted AI Agent conversation, bound to one connection
// for its whole lifetime (see AGENT_DESIGN.md §10.2). Grants is the
// session-level statement-class allowlist (§5 gate 3); it round-trips to the
// "grants" column as a JSON array.
type AgentSession struct {
	ID            string    `json:"id"`
	ConnID        string    `json:"connId"`
	Title         string    `json:"title"`
	Mode          string    `json:"mode"` // ask | agent
	ProviderID    string    `json:"providerId"`
	Model         string    `json:"model"`
	Grants        []string  `json:"grants"`
	CurrentDB     string    `json:"currentDb,omitempty"`
	CurrentSchema string    `json:"currentSchema,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// AgentSessionMeta is the mutable subset of AgentSession written by
// UpdateAgentSessionMeta. It always overwrites all of these fields together
// (simplest option per CLAUDE.md style guidance) — callers pass the session's
// current values back through for fields they aren't changing.
type AgentSessionMeta struct {
	Title         string
	Mode          string
	Grants        []string
	ProviderID    string
	Model         string
	CurrentDB     string
	CurrentSchema string
}

// AgentMessage is one turn in a session's message log (role: user | assistant
// | tool). Content holds the raw JSON blob the agent package already
// serialized (text/tool-call/tool-result) — storage does not interpret it.
// TokensIn/TokensOut are nil when unknown (e.g. user messages, or providers
// without usage reporting).
type AgentMessage struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	Seq       int       `json:"seq"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	TokensIn  *int      `json:"tokensIn,omitempty"`
	TokensOut *int      `json:"tokensOut,omitempty"`
	Compacted bool      `json:"compacted"`
	CreatedAt time.Time `json:"createdAt"`
}

// AgentAuditEntry is one audited statement execution (§5 "审计"). Rows and
// DurationMS are nil when not applicable (e.g. a rejected statement never
// ran). Error is "" when Status is not "error".
type AgentAuditEntry struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"sessionId"`
	ConnID     string    `json:"connId"`
	SQL        string    `json:"sql"`
	Class      string    `json:"class"`
	Approval   string    `json:"approval"`
	Rows       *int64    `json:"rows,omitempty"`
	DurationMS *int64    `json:"durationMs,omitempty"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// AgentAuditFilter scopes ListAgentAudit. Zero values mean "no filter" for
// that dimension; Limit <= 0 means unbounded.
type AgentAuditFilter struct {
	ConnID    string
	SessionID string
	Since     time.Time
	Until     time.Time
	Limit     int
}

// --- agent sessions ---

// CreateAgentSession inserts a new session. If sess.ID is empty a fresh UUID
// is assigned; CreatedAt/UpdatedAt are managed here.
func (s *Store) CreateAgentSession(ctx context.Context, sess AgentSession) (AgentSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess.ConnID == "" {
		return AgentSession{}, fmt.Errorf("storage: create agent session: connId is required")
	}
	if sess.Mode == "" {
		return AgentSession{}, fmt.Errorf("storage: create agent session: mode is required")
	}
	if sess.ID == "" {
		sess.ID = uuid.NewString()
	}
	now := time.Now()
	sess.CreatedAt = now
	sess.UpdatedAt = now

	grantsJSON, err := json.Marshal(sess.Grants)
	if err != nil {
		return AgentSession{}, fmt.Errorf("storage: create agent session: marshal grants: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO agent_sessions(id, conn_id, title, mode, provider_id, model, grants, current_db, current_schema, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		sess.ID, sess.ConnID, sess.Title, sess.Mode, sess.ProviderID, sess.Model,
		string(grantsJSON), nilIfEmpty(sess.CurrentDB), nilIfEmpty(sess.CurrentSchema),
		sess.CreatedAt.Unix(), sess.UpdatedAt.Unix())
	if err != nil {
		return AgentSession{}, fmt.Errorf("storage: create agent session: %w", err)
	}
	return sess, nil
}

// GetAgentSession returns one session by ID.
func (s *Store) GetAgentSession(ctx context.Context, id string) (AgentSession, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, conn_id, title, mode, provider_id, model, grants, current_db, current_schema, created_at, updated_at
		FROM agent_sessions WHERE id=?`, id)
	sess, err := scanAgentSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentSession{}, ErrNotFound
	}
	if err != nil {
		return AgentSession{}, fmt.Errorf("storage: get agent session: %w", err)
	}
	return sess, nil
}

// ListAgentSessions returns sessions most recently updated first. An empty
// connID returns all sessions (the panel's global list, §10.2); a non-empty
// one filters to that connection.
func (s *Store) ListAgentSessions(ctx context.Context, connID string) ([]AgentSession, error) {
	q := `SELECT id, conn_id, title, mode, provider_id, model, grants, current_db, current_schema, created_at, updated_at
		FROM agent_sessions`
	var args []any
	if connID != "" {
		q += ` WHERE conn_id=?`
		args = append(args, connID)
	}
	q += ` ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list agent sessions: %w", err)
	}
	defer rows.Close()
	var out []AgentSession
	for rows.Next() {
		sess, err := scanAgentSession(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: list agent sessions: %w", err)
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// UpdateAgentSessionMeta overwrites the mutable session fields (title, mode,
// grants, provider/model, selected db/schema) and bumps updated_at.
func (s *Store) UpdateAgentSessionMeta(ctx context.Context, id string, meta AgentSessionMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	grantsJSON, err := json.Marshal(meta.Grants)
	if err != nil {
		return fmt.Errorf("storage: update agent session meta: marshal grants: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE agent_sessions
		SET title=?, mode=?, provider_id=?, model=?, grants=?, current_db=?, current_schema=?, updated_at=?
		WHERE id=?`,
		meta.Title, meta.Mode, meta.ProviderID, meta.Model, string(grantsJSON),
		nilIfEmpty(meta.CurrentDB), nilIfEmpty(meta.CurrentSchema), time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("storage: update agent session meta: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAgentSession removes a session and cascades its messages (FK ON
// DELETE CASCADE). Audit entries are intentionally preserved — agent_audit
// has no FK back to agent_sessions (see migrate()).
func (s *Store) DeleteAgentSession(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `DELETE FROM agent_sessions WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("storage: delete agent session: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearAgentSessions removes every session; messages cascade via FK. Audit
// entries are intentionally preserved (no FK, see migrate()).
func (s *Store) ClearAgentSessions(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM agent_sessions`); err != nil {
		return fmt.Errorf("storage: clear agent sessions: %w", err)
	}
	return nil
}

// --- agent messages ---

// AppendAgentMessage inserts a new message, assigning Seq as
// MAX(seq)+1 within the same transaction (guaranteeing the
// UNIQUE(session_id, seq) constraint holds) and bumping the parent session's
// updated_at so ListAgentSessions reflects recent activity.
func (s *Store) AppendAgentMessage(ctx context.Context, msg AgentMessage) (AgentMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if msg.SessionID == "" {
		return AgentMessage{}, fmt.Errorf("storage: append agent message: sessionId is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentMessage{}, fmt.Errorf("storage: append agent message: %w", err)
	}
	defer tx.Rollback()

	var maxSeq sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX(seq) FROM agent_messages WHERE session_id=?`, msg.SessionID,
	).Scan(&maxSeq); err != nil {
		return AgentMessage{}, fmt.Errorf("storage: append agent message: %w", err)
	}
	msg.Seq = int(maxSeq.Int64) + 1
	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	now := time.Now()
	msg.CreatedAt = now

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_messages(id, session_id, seq, role, content, tokens_in, tokens_out, compacted, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		msg.ID, msg.SessionID, msg.Seq, msg.Role, msg.Content,
		nullableInt(msg.TokensIn), nullableInt(msg.TokensOut), boolToInt(msg.Compacted), msg.CreatedAt.Unix(),
	); err != nil {
		return AgentMessage{}, fmt.Errorf("storage: append agent message: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE agent_sessions SET updated_at=? WHERE id=?`, now.Unix(), msg.SessionID,
	); err != nil {
		return AgentMessage{}, fmt.Errorf("storage: append agent message: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return AgentMessage{}, fmt.Errorf("storage: append agent message: %w", err)
	}
	return msg, nil
}

// ListAgentMessages returns every message of a session in seq order —
// callers filter out Compacted messages themselves when building the
// LLM-facing context (§9: the chat panel always shows full history).
func (s *Store) ListAgentMessages(ctx context.Context, sessID string) ([]AgentMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, seq, role, content, tokens_in, tokens_out, compacted, created_at
		FROM agent_messages WHERE session_id=? ORDER BY seq ASC`, sessID)
	if err != nil {
		return nil, fmt.Errorf("storage: list agent messages: %w", err)
	}
	defer rows.Close()
	var out []AgentMessage
	for rows.Next() {
		m, err := scanAgentMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: list agent messages: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkMessagesCompacted flags every message with seq <= upToSeq as folded
// into a context-compaction summary (§9). The rows themselves are kept.
func (s *Store) MarkMessagesCompacted(ctx context.Context, sessID string, upToSeq int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE agent_messages SET compacted=1 WHERE session_id=? AND seq<=?`, sessID, upToSeq,
	); err != nil {
		return fmt.Errorf("storage: mark agent messages compacted: %w", err)
	}
	return nil
}

// MarkMessagesCompactedByID flags specific messages as folded. Used by the
// compactor: after the first fold the logical order diverges from seq order
// (summary rows have high seqs but sit early logically), so folding selects
// by ID, not by a seq cutoff.
func (s *Store) MarkMessagesCompactedByID(ctx context.Context, sessID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	q := `UPDATE agent_messages SET compacted=1 WHERE session_id=? AND id IN (?` +
		strings.Repeat(",?", len(ids)-1) + `)`
	args := make([]any, 0, len(ids)+1)
	args = append(args, sessID)
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("storage: mark agent messages compacted by id: %w", err)
	}
	return nil
}

// --- agent audit ---

// AppendAgentAudit inserts one audited statement execution. If entry.ID is
// empty a fresh UUID is assigned; CreatedAt defaults to now when zero.
func (s *Store) AppendAgentAudit(ctx context.Context, entry AgentAuditEntry) (AgentAuditEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.SessionID == "" || entry.ConnID == "" {
		return AgentAuditEntry{}, fmt.Errorf("storage: append agent audit: sessionId and connId are required")
	}
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_audit(id, session_id, conn_id, "sql", class, approval, "rows", duration_ms, status, error, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		entry.ID, entry.SessionID, entry.ConnID, entry.SQL, entry.Class, entry.Approval,
		nullableInt64(entry.Rows), nullableInt64(entry.DurationMS), entry.Status, nilIfEmpty(entry.Error),
		entry.CreatedAt.Unix())
	if err != nil {
		return AgentAuditEntry{}, fmt.Errorf("storage: append agent audit: %w", err)
	}
	return entry, nil
}

// ListAgentAudit returns audit entries matching filter, most recent first.
func (s *Store) ListAgentAudit(ctx context.Context, filter AgentAuditFilter) ([]AgentAuditEntry, error) {
	q := `SELECT id, session_id, conn_id, "sql", class, approval, "rows", duration_ms, status, error, created_at
		FROM agent_audit WHERE 1=1`
	var args []any
	if filter.ConnID != "" {
		q += ` AND conn_id=?`
		args = append(args, filter.ConnID)
	}
	if filter.SessionID != "" {
		q += ` AND session_id=?`
		args = append(args, filter.SessionID)
	}
	if !filter.Since.IsZero() {
		q += ` AND created_at>=?`
		args = append(args, filter.Since.Unix())
	}
	if !filter.Until.IsZero() {
		q += ` AND created_at<=?`
		args = append(args, filter.Until.Unix())
	}
	q += ` ORDER BY created_at DESC`
	if filter.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list agent audit: %w", err)
	}
	defer rows.Close()
	var out []AgentAuditEntry
	for rows.Next() {
		e, err := scanAgentAudit(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: list agent audit: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ClearAgentAudit deletes audit entries created strictly before the given
// unix-epoch-seconds timestamp.
func (s *Store) ClearAgentAudit(ctx context.Context, before int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM agent_audit WHERE created_at < ?`, before); err != nil {
		return fmt.Errorf("storage: clear agent audit: %w", err)
	}
	return nil
}

// --- scanning helpers ---

func scanAgentSession(r rowScanner) (AgentSession, error) {
	var (
		sess                     AgentSession
		grantsJSON               string
		currentDB, currentSchema sql.NullString
		created, updated         int64
	)
	if err := r.Scan(&sess.ID, &sess.ConnID, &sess.Title, &sess.Mode, &sess.ProviderID, &sess.Model,
		&grantsJSON, &currentDB, &currentSchema, &created, &updated); err != nil {
		return AgentSession{}, err
	}
	sess.CurrentDB = currentDB.String
	sess.CurrentSchema = currentSchema.String
	sess.CreatedAt = time.Unix(created, 0)
	sess.UpdatedAt = time.Unix(updated, 0)
	if grantsJSON != "" {
		_ = json.Unmarshal([]byte(grantsJSON), &sess.Grants)
	}
	return sess, nil
}

func scanAgentMessage(r rowScanner) (AgentMessage, error) {
	var (
		m                   AgentMessage
		tokensIn, tokensOut sql.NullInt64
		compacted           int
		created             int64
	)
	if err := r.Scan(&m.ID, &m.SessionID, &m.Seq, &m.Role, &m.Content,
		&tokensIn, &tokensOut, &compacted, &created); err != nil {
		return AgentMessage{}, err
	}
	if tokensIn.Valid {
		v := int(tokensIn.Int64)
		m.TokensIn = &v
	}
	if tokensOut.Valid {
		v := int(tokensOut.Int64)
		m.TokensOut = &v
	}
	m.Compacted = compacted != 0
	m.CreatedAt = time.Unix(created, 0)
	return m, nil
}

func scanAgentAudit(r rowScanner) (AgentAuditEntry, error) {
	var (
		e                 AgentAuditEntry
		rowsVal, duration sql.NullInt64
		errText           sql.NullString
		created           int64
	)
	if err := r.Scan(&e.ID, &e.SessionID, &e.ConnID, &e.SQL, &e.Class, &e.Approval,
		&rowsVal, &duration, &e.Status, &errText, &created); err != nil {
		return AgentAuditEntry{}, err
	}
	if rowsVal.Valid {
		v := rowsVal.Int64
		e.Rows = &v
	}
	if duration.Valid {
		v := duration.Int64
		e.DurationMS = &v
	}
	e.Error = errText.String
	e.CreatedAt = time.Unix(created, 0)
	return e, nil
}

func nullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
