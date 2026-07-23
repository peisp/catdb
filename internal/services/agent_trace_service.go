package services

import (
	"context"
	"fmt"
	"time"

	"catdb/internal/agenttrace"
	"catdb/internal/storage"
	"catdb/wailsbridge"
)

// AgentTraceService exposes the dev-only agent interaction traces (see
// internal/agenttrace) to the Trace window. In production builds every method
// reports the subsystem as disabled/empty — the front-end hides the entry.
type AgentTraceService struct {
	store *storage.Store
}

func NewAgentTraceService(store *storage.Store) *AgentTraceService {
	return &AgentTraceService{store: store}
}

func (s *AgentTraceService) ServiceName() string { return "AgentTraceService" }

// TraceEnabled reports whether this build records traces (dev builds only).
func (s *AgentTraceService) TraceEnabled(ctx context.Context) bool { return agenttrace.Enabled }

// TraceSession is one traced session for the list pane, joined with the
// stored session's title (the trace outlives a deleted session — Title falls
// back to empty and the front-end shows the raw id).
type TraceSession struct {
	SessionID string    `json:"sessionId"`
	Title     string    `json:"title"`
	Size      int64     `json:"size"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ListTraceSessions returns traced sessions, most recently updated first.
func (s *AgentTraceService) ListTraceSessions(ctx context.Context) ([]TraceSession, error) {
	if !agenttrace.Enabled {
		return nil, nil
	}
	infos, err := agenttrace.ListSessions()
	if err != nil {
		return nil, err
	}
	out := make([]TraceSession, 0, len(infos))
	for _, in := range infos {
		ts := TraceSession{SessionID: in.SessionID, Size: in.Size, UpdatedAt: in.UpdatedAt}
		if sess, err := s.store.GetAgentSession(ctx, in.SessionID); err == nil {
			ts.Title = sess.Title
		}
		out = append(out, ts)
	}
	return out, nil
}

// GetTrace returns the session's raw JSONL trace; the front-end parses lines.
func (s *AgentTraceService) GetTrace(ctx context.Context, sessID string) (string, error) {
	if !agenttrace.Enabled {
		return "", fmt.Errorf("agenttrace: disabled in this build")
	}
	return agenttrace.ReadSession(sessID)
}

// ClearTraces deletes all trace files.
func (s *AgentTraceService) ClearTraces(ctx context.Context) error {
	if !agenttrace.Enabled {
		return nil
	}
	return agenttrace.Clear()
}

// OpenTraceWindow opens (or focuses) the Trace child window, optionally
// pre-selecting a session.
func (s *AgentTraceService) OpenTraceWindow(ctx context.Context, sessID string) {
	if !agenttrace.Enabled {
		return
	}
	wailsbridge.OpenAgentTraceWindow(sessID)
}
