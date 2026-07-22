// Package agent is the business core of catdb's built-in AI assistant: the
// multi-turn tool loop, tool registry, prompt assembly and session state.
// It depends on dbdriver interfaces only (never a concrete database) and
// never touches application.* — UI events go out through an injected emitter
// (wailsbridge.Emit in production). See docs/AGENT_DESIGN.md.
package agent

import (
	"context"
	"fmt"
	"sync"

	"catdb/internal/core/session"
	"catdb/internal/dbdriver"
	"catdb/internal/llm"
	"catdb/internal/registry"
	"catdb/internal/storage"
	"catdb/wailsbridge"
)

// ProviderResolver turns a persisted provider instance ID into a ready
// llm.Provider. Wired to llmconfig.Resolve in main; injected so this package
// stays decoupled from config storage and tests can use llmtest.
type ProviderResolver func(ctx context.Context, providerID string) (llm.Provider, error)

// ConnectFunc resolves a connection ID to its open Connection and Driver.
// The production implementation goes through session.Manager + registry;
// tests inject fakes.
type ConnectFunc func(ctx context.Context, connID string) (dbdriver.Connection, dbdriver.Driver, error)

// DedicatedFunc opens a NEW physical connection for a task transaction
// (production: session.Manager.OpenDedicated). The caller owns closing it.
type DedicatedFunc func(ctx context.Context, connID string) (dbdriver.Connection, error)

// Engine owns every running agent loop. One Engine per app.
type Engine struct {
	store     *storage.Store
	resolve   ProviderResolver
	connect   ConnectFunc
	dedicated DedicatedFunc
	emit      func(name string, data any)

	broker *approvalBroker
	txm    *txManager

	maxIterations int

	mu   sync.Mutex
	runs map[string]context.CancelFunc // sessID → cancel of the running loop
}

// NewEngine wires the engine's dependencies.
func NewEngine(store *storage.Store, mgr *session.Manager, resolve ProviderResolver) *Engine {
	return &Engine{
		store:         store,
		resolve:       resolve,
		connect:       managerConnect(mgr),
		dedicated:     mgr.OpenDedicated,
		emit:          wailsbridge.Emit,
		broker:        newApprovalBroker(),
		txm:           newTxManager(),
		maxIterations: 25,
	}
}

// Approve resolves a pending gate-4 approval (scope: once | task-verb) or a
// pending task plan.
func (e *Engine) Approve(approvalID, scope string) error {
	return e.broker.resolve(approvalID, approvalDecision{Approved: true, Scope: scope})
}

// Reject declines a pending approval; reason is fed back to the model.
func (e *Engine) Reject(approvalID, reason string) error {
	return e.broker.resolve(approvalID, approvalDecision{Approved: false, Reason: reason})
}

func managerConnect(mgr *session.Manager) ConnectFunc {
	return func(ctx context.Context, connID string) (dbdriver.Connection, dbdriver.Driver, error) {
		conn, err := mgr.Get(connID)
		if err != nil {
			if conn, err = mgr.Open(ctx, connID); err != nil {
				return nil, nil, fmt.Errorf("agent: open connection: %w", err)
			}
		}
		name, err := mgr.DriverName(ctx, connID)
		if err != nil {
			return nil, nil, fmt.Errorf("agent: driver name: %w", err)
		}
		drv, err := registry.Get(name)
		if err != nil {
			return nil, nil, fmt.Errorf("agent: driver: %w", err)
		}
		return conn, drv, nil
	}
}

// begin registers a running loop for sessID; fails if one is already running
// (one loop per session, see AGENT_DESIGN.md §4.1).
func (e *Engine) begin(sessID string, cancel context.CancelFunc) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runs == nil {
		e.runs = map[string]context.CancelFunc{}
	}
	if _, busy := e.runs[sessID]; busy {
		return fmt.Errorf("agent: session %s already has a running loop", sessID)
	}
	e.runs[sessID] = cancel
	return nil
}

func (e *Engine) end(sessID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.runs, sessID)
}

// Cancel stops the running loop of sessID, if any.
func (e *Engine) Cancel(sessID string) {
	e.mu.Lock()
	cancel := e.runs[sessID]
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
