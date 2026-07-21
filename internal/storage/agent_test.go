package storage

import (
	"context"
	"testing"
	"time"
)

func TestAgentSessionCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	conn, err := s.SaveConnection(ctx, ConnectionProfile{Name: "c", Driver: "mysql"})
	if err != nil {
		t.Fatalf("SaveConnection: %v", err)
	}

	sess, err := s.CreateAgentSession(ctx, AgentSession{
		ConnID:     conn.ID,
		Title:      "first session",
		Mode:       "ask",
		ProviderID: "anthropic",
		Model:      "claude-x",
		Grants:     []string{"select"},
	})
	if err != nil {
		t.Fatalf("CreateAgentSession: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("ID should be assigned")
	}
	if sess.CreatedAt.IsZero() || sess.UpdatedAt.IsZero() {
		t.Fatal("timestamps should be set")
	}

	got, err := s.GetAgentSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	if got.Title != "first session" || got.Mode != "ask" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if len(got.Grants) != 1 || got.Grants[0] != "select" {
		t.Fatalf("grants round-trip mismatch: %+v", got.Grants)
	}

	if _, err := s.GetAgentSession(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	list, err := s.ListAgentSessions(ctx, conn.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListAgentSessions: got %d, err=%v", len(list), err)
	}

	if err := s.DeleteAgentSession(ctx, sess.ID); err != nil {
		t.Fatalf("DeleteAgentSession: %v", err)
	}
	if err := s.DeleteAgentSession(ctx, sess.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound on second delete, got %v", err)
	}
}

func TestAgentSessionGrantsJSONRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	conn, _ := s.SaveConnection(ctx, ConnectionProfile{Name: "c", Driver: "mysql"})

	sess, err := s.CreateAgentSession(ctx, AgentSession{
		ConnID: conn.ID, Title: "t", Mode: "agent", ProviderID: "p", Model: "m",
		Grants: []string{"select", "insert", "update"},
	})
	if err != nil {
		t.Fatalf("CreateAgentSession: %v", err)
	}
	got, err := s.GetAgentSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	want := []string{"select", "insert", "update"}
	if len(got.Grants) != len(want) {
		t.Fatalf("grants mismatch: %+v", got.Grants)
	}
	for i, g := range want {
		if got.Grants[i] != g {
			t.Fatalf("grants[%d] = %q, want %q", i, got.Grants[i], g)
		}
	}
}

func TestUpdateAgentSessionMeta(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	conn, _ := s.SaveConnection(ctx, ConnectionProfile{Name: "c", Driver: "mysql"})
	sess, err := s.CreateAgentSession(ctx, AgentSession{
		ConnID: conn.ID, Title: "t", Mode: "ask", ProviderID: "p", Model: "m",
	})
	if err != nil {
		t.Fatalf("CreateAgentSession: %v", err)
	}

	err = s.UpdateAgentSessionMeta(ctx, sess.ID, AgentSessionMeta{
		Title:         "renamed",
		Mode:          "agent",
		Grants:        []string{"select", "delete"},
		ProviderID:    "p2",
		Model:         "m2",
		CurrentDB:     "shop",
		CurrentSchema: "public",
	})
	if err != nil {
		t.Fatalf("UpdateAgentSessionMeta: %v", err)
	}

	got, err := s.GetAgentSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	if got.Title != "renamed" || got.Mode != "agent" || got.ProviderID != "p2" || got.Model != "m2" {
		t.Fatalf("meta update mismatch: %+v", got)
	}
	if got.CurrentDB != "shop" || got.CurrentSchema != "public" {
		t.Fatalf("namespace update mismatch: %+v", got)
	}
	if len(got.Grants) != 2 || got.Grants[0] != "select" || got.Grants[1] != "delete" {
		t.Fatalf("grants update mismatch: %+v", got.Grants)
	}
	// Compare at unix-second granularity: storage truncates to whole
	// seconds, so nanosecond-precision time.Now() values can look
	// "earlier" than their own persisted-and-reloaded selves.
	if got.UpdatedAt.Unix() < sess.UpdatedAt.Unix() {
		t.Fatal("UpdatedAt should advance")
	}

	if err := s.UpdateAgentSessionMeta(ctx, "nope", AgentSessionMeta{}); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestAgentMessageSeqAndUnique(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	conn, _ := s.SaveConnection(ctx, ConnectionProfile{Name: "c", Driver: "mysql"})
	sess, _ := s.CreateAgentSession(ctx, AgentSession{ConnID: conn.ID, Title: "t", Mode: "ask", ProviderID: "p", Model: "m"})

	m1, err := s.AppendAgentMessage(ctx, AgentMessage{SessionID: sess.ID, Role: "user", Content: `{"text":"hi"}`})
	if err != nil {
		t.Fatalf("AppendAgentMessage 1: %v", err)
	}
	if m1.Seq != 1 {
		t.Fatalf("expected seq 1, got %d", m1.Seq)
	}

	tokensIn, tokensOut := 10, 20
	m2, err := s.AppendAgentMessage(ctx, AgentMessage{
		SessionID: sess.ID, Role: "assistant", Content: `{"text":"hello"}`,
		TokensIn: &tokensIn, TokensOut: &tokensOut,
	})
	if err != nil {
		t.Fatalf("AppendAgentMessage 2: %v", err)
	}
	if m2.Seq != 2 {
		t.Fatalf("expected seq 2, got %d", m2.Seq)
	}

	msgs, err := s.ListAgentMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ListAgentMessages: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Seq != 1 || msgs[1].Seq != 2 {
		t.Fatalf("expected seq-ordered messages, got %+v", msgs)
	}
	if msgs[1].TokensIn == nil || *msgs[1].TokensIn != 10 || msgs[1].TokensOut == nil || *msgs[1].TokensOut != 20 {
		t.Fatalf("token round-trip mismatch: %+v", msgs[1])
	}
	if msgs[0].TokensIn != nil {
		t.Fatalf("expected nil TokensIn for message without usage, got %v", *msgs[0].TokensIn)
	}

	// Explicit seq collision within a session must violate UNIQUE(session_id, seq).
	if err := s.forceInsertAgentMessageSeq(ctx, sess.ID, 1); err == nil {
		t.Fatal("expected UNIQUE(session_id, seq) violation")
	}

	// AppendAgentMessage bumps the parent session's updated_at.
	got, err := s.GetAgentSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	if got.UpdatedAt.Unix() < sess.UpdatedAt.Unix() {
		t.Fatalf("expected session updated_at to advance after appending messages")
	}
}

// forceInsertAgentMessageSeq bypasses AppendAgentMessage's seq computation to
// directly exercise the UNIQUE(session_id, seq) constraint.
func (s *Store) forceInsertAgentMessageSeq(ctx context.Context, sessID string, seq int) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_messages(id, session_id, seq, role, content, compacted, created_at)
		VALUES (?,?,?,?,?,?,?)`,
		"forced-"+sessID, sessID, seq, "user", `{"text":"dup"}`, 0, time.Now().Unix())
	return err
}

func TestMarkMessagesCompacted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	conn, _ := s.SaveConnection(ctx, ConnectionProfile{Name: "c", Driver: "mysql"})
	sess, _ := s.CreateAgentSession(ctx, AgentSession{ConnID: conn.ID, Title: "t", Mode: "ask", ProviderID: "p", Model: "m"})

	for i := 0; i < 3; i++ {
		if _, err := s.AppendAgentMessage(ctx, AgentMessage{SessionID: sess.ID, Role: "user", Content: "{}"}); err != nil {
			t.Fatalf("AppendAgentMessage: %v", err)
		}
	}

	if err := s.MarkMessagesCompacted(ctx, sess.ID, 2); err != nil {
		t.Fatalf("MarkMessagesCompacted: %v", err)
	}

	msgs, err := s.ListAgentMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ListAgentMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected all messages retained, got %d", len(msgs))
	}
	if !msgs[0].Compacted || !msgs[1].Compacted {
		t.Fatalf("expected seq 1 and 2 compacted: %+v", msgs)
	}
	if msgs[2].Compacted {
		t.Fatalf("expected seq 3 not compacted: %+v", msgs[2])
	}
}

func TestAgentSessionCascadeDeletesMessagesButKeepsAudit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	conn, _ := s.SaveConnection(ctx, ConnectionProfile{Name: "c", Driver: "mysql"})
	sess, _ := s.CreateAgentSession(ctx, AgentSession{ConnID: conn.ID, Title: "t", Mode: "agent", ProviderID: "p", Model: "m"})

	if _, err := s.AppendAgentMessage(ctx, AgentMessage{SessionID: sess.ID, Role: "user", Content: "{}"}); err != nil {
		t.Fatalf("AppendAgentMessage: %v", err)
	}
	if _, err := s.AppendAgentAudit(ctx, AgentAuditEntry{
		SessionID: sess.ID, ConnID: conn.ID, SQL: "SELECT 1", Class: "read", Approval: "n/a", Status: "ok",
	}); err != nil {
		t.Fatalf("AppendAgentAudit: %v", err)
	}

	if err := s.DeleteAgentSession(ctx, sess.ID); err != nil {
		t.Fatalf("DeleteAgentSession: %v", err)
	}

	msgs, err := s.ListAgentMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ListAgentMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected messages cascade-deleted, got %d", len(msgs))
	}

	audit, err := s.ListAgentAudit(ctx, AgentAuditFilter{SessionID: sess.ID})
	if err != nil {
		t.Fatalf("ListAgentAudit: %v", err)
	}
	if len(audit) != 1 {
		t.Fatalf("expected audit entry preserved after session delete, got %d", len(audit))
	}
}

func TestAgentAuditAppendFilterAndClear(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	conn, _ := s.SaveConnection(ctx, ConnectionProfile{Name: "c", Driver: "mysql"})
	other, _ := s.SaveConnection(ctx, ConnectionProfile{Name: "c2", Driver: "mysql"})
	sess, _ := s.CreateAgentSession(ctx, AgentSession{ConnID: conn.ID, Title: "t", Mode: "agent", ProviderID: "p", Model: "m"})

	old := time.Now().Add(-2 * time.Hour)
	rows := int64(5)
	dur := int64(120)
	if _, err := s.AppendAgentAudit(ctx, AgentAuditEntry{
		SessionID: sess.ID, ConnID: conn.ID, SQL: "SELECT * FROM t", Class: "read", Approval: "n/a",
		Rows: &rows, DurationMS: &dur, Status: "ok", CreatedAt: old,
	}); err != nil {
		t.Fatalf("AppendAgentAudit old: %v", err)
	}
	if _, err := s.AppendAgentAudit(ctx, AgentAuditEntry{
		SessionID: sess.ID, ConnID: conn.ID, SQL: "DELETE FROM t", Class: "delete", Approval: "manual",
		Status: "error", Error: "boom",
	}); err != nil {
		t.Fatalf("AppendAgentAudit new: %v", err)
	}
	if _, err := s.AppendAgentAudit(ctx, AgentAuditEntry{
		SessionID: "other-session", ConnID: other.ID, SQL: "SELECT 1", Class: "read", Approval: "n/a", Status: "ok",
	}); err != nil {
		t.Fatalf("AppendAgentAudit other conn: %v", err)
	}

	all, err := s.ListAgentAudit(ctx, AgentAuditFilter{ConnID: conn.ID})
	if err != nil {
		t.Fatalf("ListAgentAudit: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 entries for conn, got %d", len(all))
	}
	// Most recent first.
	if all[0].Class != "delete" || all[1].Class != "read" {
		t.Fatalf("expected DESC order by created_at, got %+v", all)
	}
	if all[1].Rows == nil || *all[1].Rows != 5 {
		t.Fatalf("rows round-trip mismatch: %+v", all[1])
	}
	if all[1].DurationMS == nil || *all[1].DurationMS != 120 {
		t.Fatalf("duration round-trip mismatch: %+v", all[1])
	}
	if all[0].Error != "boom" {
		t.Fatalf("expected error text preserved, got %q", all[0].Error)
	}

	// Time-range filter excludes the old entry.
	recent, err := s.ListAgentAudit(ctx, AgentAuditFilter{ConnID: conn.ID, Since: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatalf("ListAgentAudit since: %v", err)
	}
	if len(recent) != 1 || recent[0].Class != "delete" {
		t.Fatalf("expected only the recent entry, got %+v", recent)
	}

	// Limit.
	limited, err := s.ListAgentAudit(ctx, AgentAuditFilter{ConnID: conn.ID, Limit: 1})
	if err != nil {
		t.Fatalf("ListAgentAudit limit: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected limit=1 to return 1 row, got %d", len(limited))
	}

	// Clear entries older than 1 hour ago — only the old one goes.
	if err := s.ClearAgentAudit(ctx, time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatalf("ClearAgentAudit: %v", err)
	}
	remaining, err := s.ListAgentAudit(ctx, AgentAuditFilter{ConnID: conn.ID})
	if err != nil {
		t.Fatalf("ListAgentAudit after clear: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Class != "delete" {
		t.Fatalf("expected only the recent entry to survive clear, got %+v", remaining)
	}

	// Other connection's audit entries are untouched.
	otherAudit, err := s.ListAgentAudit(ctx, AgentAuditFilter{ConnID: other.ID})
	if err != nil {
		t.Fatalf("ListAgentAudit other: %v", err)
	}
	if len(otherAudit) != 1 {
		t.Fatalf("expected other conn's audit untouched, got %d", len(otherAudit))
	}
}
