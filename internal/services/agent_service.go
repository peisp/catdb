package services

import (
	"context"
	"fmt"

	"catdb/internal/agent"
	"catdb/internal/storage"
)

// AgentService is the thin Wails binding over internal/agent: input
// validation, forwarding to the engine, and session CRUD. Streaming output
// travels over agent:* events, not return values (AGENT_DESIGN.md §13).
type AgentService struct {
	store  *storage.Store
	engine *agent.Engine
}

// NewAgentService wires the storage and engine dependencies.
func NewAgentService(store *storage.Store, engine *agent.Engine) *AgentService {
	return &AgentService{store: store, engine: engine}
}

func (s *AgentService) ServiceName() string { return "AgentService" }

// --- sessions ---

// CreateSession opens a new session bound to connID. mode is "ask" or
// "agent"; provider/model default from settings when empty.
func (s *AgentService) CreateSession(ctx context.Context, connID, mode string) (storage.AgentSession, error) {
	if mode != "ask" && mode != "agent" {
		return storage.AgentSession{}, fmt.Errorf("AgentService: invalid mode %q", mode)
	}
	providerID, _ := s.store.GetSetting(ctx, "agent.provider")
	model, _ := s.store.GetSetting(ctx, "agent.model")
	return s.store.CreateAgentSession(ctx, storage.AgentSession{
		ConnID:     connID,
		Mode:       mode,
		ProviderID: providerID,
		Model:      model,
		Grants:     []string{"select"},
	})
}

// ListSessions returns the sessions of a connection, most recent first.
func (s *AgentService) ListSessions(ctx context.Context, connID string) ([]storage.AgentSession, error) {
	return s.store.ListAgentSessions(ctx, connID)
}

// GetMessages returns a session's full message history (compacted included —
// the chat panel always shows everything, §9).
func (s *AgentService) GetMessages(ctx context.Context, sessID string) ([]storage.AgentMessage, error) {
	return s.store.ListAgentMessages(ctx, sessID)
}

// RenameSession sets a session's title.
func (s *AgentService) RenameSession(ctx context.Context, sessID, title string) error {
	return s.updateMeta(ctx, sessID, func(m *storage.AgentSessionMeta) { m.Title = title })
}

// DeleteSession removes a session and its messages (audit is preserved).
func (s *AgentService) DeleteSession(ctx context.Context, sessID string) error {
	s.engine.Cancel(sessID)
	return s.store.DeleteAgentSession(ctx, sessID)
}

// SetMode switches a session between ask and agent mode.
func (s *AgentService) SetMode(ctx context.Context, sessID, mode string) error {
	if mode != "ask" && mode != "agent" {
		return fmt.Errorf("AgentService: invalid mode %q", mode)
	}
	return s.updateMeta(ctx, sessID, func(m *storage.AgentSessionMeta) { m.Mode = mode })
}

// SetGrants replaces the session's statement grants (§5 gate 3).
func (s *AgentService) SetGrants(ctx context.Context, sessID string, grants []string) error {
	return s.updateMeta(ctx, sessID, func(m *storage.AgentSessionMeta) { m.Grants = grants })
}

// SetNamespace switches the session's selected database/schema (§10.2).
func (s *AgentService) SetNamespace(ctx context.Context, sessID, db, schema string) error {
	return s.updateMeta(ctx, sessID, func(m *storage.AgentSessionMeta) {
		m.CurrentDB, m.CurrentSchema = db, schema
	})
}

// SetSessionModel switches the session's provider/model (takes effect next turn).
func (s *AgentService) SetSessionModel(ctx context.Context, sessID, providerID, model string) error {
	return s.updateMeta(ctx, sessID, func(m *storage.AgentSessionMeta) {
		m.ProviderID, m.Model = providerID, model
	})
}

// --- conversation ---

// SendMessage runs one agent turn. It blocks until the turn completes;
// cancelling the front-end promise aborts the loop (LLM stream + queries).
func (s *AgentService) SendMessage(ctx context.Context, sessID, text string) error {
	if text == "" {
		return fmt.Errorf("AgentService: empty message")
	}
	return s.engine.Send(ctx, sessID, text)
}

// Cancel stops the session's running loop, if any.
func (s *AgentService) Cancel(ctx context.Context, sessID string) error {
	s.engine.Cancel(sessID)
	return nil
}

// --- approvals & task transaction (M2, §5 gates 4/5) ---

// Approve resolves a pending statement approval or task plan.
// scope: "once" | "task-verb" (auto-approve same verb for the rest of the task).
func (s *AgentService) Approve(ctx context.Context, approvalID, scope string) error {
	if scope != "once" && scope != "task-verb" {
		return fmt.Errorf("AgentService: invalid approval scope %q", scope)
	}
	return s.engine.Approve(approvalID, scope)
}

// Reject declines a pending approval; reason (optional) is fed back to the model.
func (s *AgentService) Reject(ctx context.Context, approvalID, reason string) error {
	return s.engine.Reject(approvalID, reason)
}

// CommitTx commits the session's pending task transaction.
func (s *AgentService) CommitTx(ctx context.Context, sessID string) error {
	return s.engine.CommitTx(ctx, sessID)
}

// RollbackTx rolls the session's pending task transaction back.
func (s *AgentService) RollbackTx(ctx context.Context, sessID string) error {
	return s.engine.RollbackTx(ctx, sessID)
}

// updateMeta loads current session state, applies patch, writes it back —
// UpdateAgentSessionMeta overwrites all mutable fields together.
func (s *AgentService) updateMeta(ctx context.Context, sessID string, patch func(*storage.AgentSessionMeta)) error {
	sess, err := s.store.GetAgentSession(ctx, sessID)
	if err != nil {
		return err
	}
	meta := storage.AgentSessionMeta{
		Title: sess.Title, Mode: sess.Mode, Grants: sess.Grants,
		ProviderID: sess.ProviderID, Model: sess.Model,
		CurrentDB: sess.CurrentDB, CurrentSchema: sess.CurrentSchema,
	}
	patch(&meta)
	return s.store.UpdateAgentSessionMeta(ctx, sessID, meta)
}
