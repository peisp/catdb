package agent

import (
	"context"
	"fmt"
	"sync"

	"catdb/internal/dbdriver"
)

// This file is gate logic for run_sql (AGENT_DESIGN.md §5): the pure verdict
// function for gates 1–3/5, the approval broker that suspends the loop for
// gate 4, and the shared vocabulary (slugs, scopes). Execution and audit
// wiring live with the run_sql tool.

// Deny slugs — stable, mapped to error.* / agent.* i18n keys on the front-end.
// Never localized here (CLAUDE.md i18n rules).
const (
	slugEnvReadonly  = "agent.env-readonly"   // prod is hard read-only for the agent
	slugForbidden    = "agent.stmt-forbidden" // ADMIN / UNKNOWN class — no grant can enable it
	slugNotGranted   = "agent.not-granted"    // verb outside session grants
	slugPlanRequired = "agent.plan-required"  // write before an approved task plan
)

// Approval scopes accepted by AgentService.Approve.
const (
	scopeOnce     = "once"
	scopeTaskVerb = "task-verb"
	warnNoWhere   = "no-where-clause" // approval-card red warning (gate 5)
	envProd       = "prod"
	envUnmarked   = "" // unmarked connections: approval always manual (决策 2)
)

// gateVerdict is the outcome of gates 1/2/3/5 for one classified statement.
// Exactly one of {allowed, denied, approval-needed} holds:
//   - Deny != ""            → rejected, slug fed back to the model
//   - NeedsApproval == true → suspend on gate 4 (Warning may flag the card)
//   - otherwise             → allowed without approval (reads)
type gateVerdict struct {
	Deny          string
	NeedsApproval bool
	// AutoApprovable: a prior task-verb approval may skip the card. False for
	// DDL (always per-statement), MissingWhere (always confirmed by a human)
	// and unmarked environments (no auto-approve, ever).
	AutoApprovable bool
	Warning        string
}

// gateStatement runs gates 1 (environment), 2 (class), 3 (grants) and 5
// (statement guards) for one statement. Gate 4 (the actual approval) and the
// plan contract are enforced by the caller — they need session state; this
// stays a pure function so the safety matrix is exhaustively unit-testable.
//
// grants holds lowercase verb keys ("insert", "update", "delete") plus "ddl".
// Reads are always allowed — Ask mode has no grants at all and metadata
// access is the agent's baseline capability.
func gateStatement(env string, c dbdriver.StatementClassification, grants map[string]bool) gateVerdict {
	// Gate 2: ADMIN and UNKNOWN are unconditionally forbidden — no grant,
	// approval or environment can enable them. Checked before gate 1 so the
	// model gets the strictest slug.
	if c.Class == dbdriver.ClassAdmin || c.Class == dbdriver.ClassUnknown {
		return gateVerdict{Deny: slugForbidden}
	}
	if c.Class == dbdriver.ClassRead {
		return gateVerdict{}
	}
	// Gate 1: prod is hard read-only. Session grants cannot override.
	if env == envProd {
		return gateVerdict{Deny: slugEnvReadonly}
	}
	// Gate 3: verb-level grants (write_dml matches its verb, DDL matches "ddl").
	key := string(c.Verb)
	if c.Class == dbdriver.ClassDDL {
		key = "ddl"
	}
	if !grants[key] {
		return gateVerdict{Deny: slugNotGranted}
	}
	// Gate 4 precondition + gate 5 flags.
	v := gateVerdict{NeedsApproval: true}
	if c.Class == dbdriver.ClassWriteDML && env != envUnmarked {
		v.AutoApprovable = true
	}
	if c.MissingWhere {
		// Gate 5: UPDATE/DELETE without WHERE — red card, human confirms,
		// never auto-approved.
		v.Warning = warnNoWhere
		v.AutoApprovable = false
	}
	return v
}

// --- approval broker ---------------------------------------------------------

// approvalDecision is what Approve/Reject deliver to a suspended loop.
type approvalDecision struct {
	Approved bool
	Scope    string // once | task-verb
	Reason   string // rejection reason, fed back to the model
}

// approvalBroker connects a loop suspended on gate 4 (or a pending task plan)
// with the AgentService Approve/Reject calls. Waiting does not hold any
// database connection — the wait happens before execution starts.
type approvalBroker struct {
	mu      sync.Mutex
	pending map[string]chan approvalDecision
}

func newApprovalBroker() *approvalBroker {
	return &approvalBroker{pending: map[string]chan approvalDecision{}}
}

// create registers a pending approval and returns its wait channel.
func (b *approvalBroker) create(id string) <-chan approvalDecision {
	ch := make(chan approvalDecision, 1)
	b.mu.Lock()
	b.pending[id] = ch
	b.mu.Unlock()
	return ch
}

// resolve delivers the decision. Unknown or already-resolved IDs error.
func (b *approvalBroker) resolve(id string, d approvalDecision) error {
	b.mu.Lock()
	ch, ok := b.pending[id]
	delete(b.pending, id)
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("agent: unknown approval id %q", id)
	}
	ch <- d
	return nil
}

// drop discards a pending approval (loop cancelled while suspended).
func (b *approvalBroker) drop(id string) {
	b.mu.Lock()
	delete(b.pending, id)
	b.mu.Unlock()
}

// waitOn blocks on a channel from create until decided or ctx cancelled.
// Callers MUST create BEFORE emitting the approval event — otherwise a fast
// Approve can race the registration and the loop hangs forever.
func (b *approvalBroker) waitOn(ctx context.Context, id string, ch <-chan approvalDecision) (approvalDecision, error) {
	select {
	case d := <-ch:
		return d, nil
	case <-ctx.Done():
		b.drop(id)
		return approvalDecision{}, ctx.Err()
	}
}
