package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"catdb/internal/dbdriver"
	"catdb/internal/llm"
	"catdb/internal/llm/llmtest"
	"catdb/internal/storage"
)

// --- agent-mode fakes: querier / tx / dedicated connection -------------------

// dbProbe records everything the fakes executed.
type dbProbe struct {
	mu        sync.Mutex
	execs     []string // via pooled querier or tx
	txExecs   []string // via task tx only
	committed bool
	rolled    bool
	closed    bool
}

func (p *dbProbe) log(dst *[]string, sql string) {
	p.mu.Lock()
	*dst = append(*dst, sql)
	p.mu.Unlock()
}

type fakeRS struct {
	cols []string
	rows [][]any
	done bool
}

func (r *fakeRS) Columns() []dbdriver.ColumnMeta {
	out := make([]dbdriver.ColumnMeta, len(r.cols))
	for i, c := range r.cols {
		out[i] = dbdriver.ColumnMeta{Name: c}
	}
	return out
}
func (r *fakeRS) Next(batch int) ([][]any, bool, error) {
	if r.done {
		return nil, true, nil
	}
	r.done = true
	return r.rows, true, nil
}
func (r *fakeRS) Close() error { return nil }

type fakeQuerier struct {
	probe *dbProbe
	dst   *[]string
}

func (q fakeQuerier) Exec(ctx context.Context, sql string, args ...any) (dbdriver.ExecResult, error) {
	q.probe.log(q.dst, sql)
	return dbdriver.ExecResult{RowsAffected: 3}, nil
}
func (q fakeQuerier) Query(ctx context.Context, sql string, args ...any) (dbdriver.ResultSet, error) {
	q.probe.log(q.dst, sql)
	return &fakeRS{cols: []string{"id", "name"}, rows: [][]any{{1, "a"}, {2, "b"}}}, nil
}
func (q fakeQuerier) Explain(ctx context.Context, sql string) (dbdriver.ResultSet, error) {
	return &fakeRS{cols: []string{"plan"}, rows: [][]any{{"scan"}}}, nil
}

type fakeTx struct {
	fakeQuerier
	probe *dbProbe
}

func (t fakeTx) Commit() error {
	t.probe.mu.Lock()
	defer t.probe.mu.Unlock()
	t.probe.committed = true
	return nil
}
func (t fakeTx) Rollback() error {
	t.probe.mu.Lock()
	defer t.probe.mu.Unlock()
	t.probe.rolled = true
	return nil
}

// dedicatedConn is what Engine.dedicated returns for the task tx.
type dedicatedConn struct {
	dbdriver.Connection
	probe *dbProbe
}

func (c *dedicatedConn) Begin(ctx context.Context, opts *dbdriver.TxOptions) (dbdriver.Tx, error) {
	return fakeTx{fakeQuerier: fakeQuerier{probe: c.probe, dst: &c.probe.txExecs}, probe: c.probe}, nil
}
func (c *dedicatedConn) Close() error {
	c.probe.mu.Lock()
	defer c.probe.mu.Unlock()
	c.probe.closed = true
	return nil
}

// agentConn is the pooled connection fake for agent-mode tests.
type agentConn struct {
	dbdriver.Connection
	probe *dbProbe
}

func (c agentConn) Metadata() dbdriver.Metadata { return fakeMeta{} }
func (c agentConn) Querier() dbdriver.Querier {
	return fakeQuerier{probe: c.probe, dst: &c.probe.execs}
}

type agentDriver struct{ fakeDriver }

func (agentDriver) Capabilities() dbdriver.Capabilities {
	return dbdriver.Capabilities{Transactions: true}
}

// decision scripts the auto-responder for approval / plan events.
type decision struct {
	approve bool
	scope   string
	reason  string
}

// newAgentEngine builds an engine + store with a connection (environment env)
// and an agent-mode session granted the verbs in grants. Approval and plan
// events are auto-answered from the decisions queue, in order.
func newAgentEngine(t *testing.T, p llm.Provider, env string, grants []string, decisions []decision) (*Engine, *storage.Store, string, *dbProbe, *eventLog) {
	t.Helper()
	store, err := storage.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	conn, err := store.SaveConnection(context.Background(), storage.ConnectionProfile{
		Name: "c", Driver: "mysql", Environment: env,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.CreateAgentSession(context.Background(), storage.AgentSession{
		ConnID: conn.ID, Mode: "agent", ProviderID: "p1", Model: "m1", Grants: grants,
	})
	if err != nil {
		t.Fatal(err)
	}

	probe := &dbProbe{}
	log := &eventLog{}
	e := &Engine{
		store:   store,
		resolve: func(ctx context.Context, id string) (llm.Provider, error) { return p, nil },
		connect: func(ctx context.Context, id string) (dbdriver.Connection, dbdriver.Driver, error) {
			return agentConn{probe: probe}, agentDriver{}, nil
		},
		dedicated: func(ctx context.Context, id string) (dbdriver.Connection, error) {
			return &dedicatedConn{probe: probe}, nil
		},
		broker:        newApprovalBroker(),
		txm:           newTxManager(),
		maxIterations: 25,
	}
	// Auto-responder: answer approval/plan events from the queue.
	var dmu sync.Mutex
	queue := append([]decision(nil), decisions...)
	e.emit = func(name string, data any) {
		log.emit(name, data)
		if name != "agent:approval" && name != "agent:plan" {
			return
		}
		dmu.Lock()
		if len(queue) == 0 {
			dmu.Unlock()
			t.Errorf("unexpected %s event, empty decision queue", name)
			return
		}
		d := queue[0]
		queue = queue[1:]
		dmu.Unlock()
		m := data.(map[string]any)
		id, _ := m["approvalID"].(string)
		if id == "" {
			id, _ = m["planID"].(string)
		}
		go func() {
			if d.approve {
				_ = e.Approve(id, d.scope)
			} else {
				_ = e.Reject(id, d.reason)
			}
		}()
	}
	return e, store, sess.ID, probe, log
}

func toolRound(name, args string) []llm.Event {
	return []llm.Event{
		llm.ToolCallStart{ID: "t", Name: name},
		llm.ToolCallDelta{ID: "t", ArgsFragment: args},
		llm.Stop{Reason: llm.StopToolUse},
	}
}

func endRound(text string) []llm.Event {
	return []llm.Event{llm.TextDelta{Text: text}, llm.Stop{Reason: llm.StopEndTurn}}
}

func lastToolResult(t *testing.T, store *storage.Store, sessID string) storedResult {
	t.Helper()
	msgs, _ := store.ListAgentMessages(context.Background(), sessID)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "tool" {
			var c msgContent
			if err := jsonUnmarshal(msgs[i].Content, &c); err != nil || c.Result == nil {
				t.Fatalf("bad tool message: %v %s", err, msgs[i].Content)
			}
			return *c.Result
		}
	}
	t.Fatal("no tool message found")
	return storedResult{}
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

// --- golden flows -------------------------------------------------------------

func TestRunSQLReadNoApproval(t *testing.T) {
	p := llmtest.New("fake",
		toolRound("run_sql", `{"db":"shop","sql":"SELECT id, name FROM users"}`),
		endRound("两行数据"),
	)
	e, store, sessID, probe, log := newAgentEngine(t, p, "dev", []string{"select"}, nil)
	if err := e.Send(context.Background(), sessID, "查用户", nil); err != nil {
		t.Fatal(err)
	}
	if len(probe.execs) != 1 || !strings.Contains(probe.execs[0], "SELECT") {
		t.Fatalf("execs = %v", probe.execs)
	}
	if log.count("agent:approval") != 0 {
		t.Fatal("reads must not require approval")
	}
	if log.count("agent:result") != 1 {
		t.Fatal("user-path result event missing")
	}
	r := lastToolResult(t, store, sessID)
	if r.IsError || !strings.Contains(r.Content, `"id"`) {
		t.Fatalf("tool result = %+v", r)
	}
	audits, _ := store.ListAgentAudit(context.Background(), storage.AgentAuditFilter{SessionID: sessID})
	if len(audits) != 1 || audits[0].Status != "ok" || audits[0].Class != "read" {
		t.Fatalf("audit = %+v", audits)
	}
}

func TestWriteWithoutPlanRejected(t *testing.T) {
	p := llmtest.New("fake",
		toolRound("run_sql", `{"db":"shop","sql":"INSERT INTO t (a) VALUES (1)"}`),
		endRound("ok"),
	)
	e, store, sessID, probe, _ := newAgentEngine(t, p, "dev", []string{"select", "insert"}, nil)
	if err := e.Send(context.Background(), sessID, "插入", nil); err != nil {
		t.Fatal(err)
	}
	r := lastToolResult(t, store, sessID)
	if !r.IsError || !strings.Contains(r.Content, slugPlanRequired) {
		t.Fatalf("want plan-required error, got %+v", r)
	}
	if len(probe.execs)+len(probe.txExecs) != 0 {
		t.Fatal("nothing must execute without a plan")
	}
}

func TestProdHardReadonly(t *testing.T) {
	p := llmtest.New("fake",
		toolRound("run_sql", `{"db":"shop","sql":"DELETE FROM t WHERE id=1"}`),
		endRound("ok"),
	)
	// All grants on — prod must still refuse.
	e, store, sessID, probe, _ := newAgentEngine(t, p, "prod", []string{"select", "insert", "update", "delete", "ddl"}, nil)
	if err := e.Send(context.Background(), sessID, "删一行", nil); err != nil {
		t.Fatal(err)
	}
	r := lastToolResult(t, store, sessID)
	if !r.IsError || !strings.Contains(r.Content, slugEnvReadonly) {
		t.Fatalf("want env-readonly, got %+v", r)
	}
	if len(probe.execs)+len(probe.txExecs) != 0 {
		t.Fatal("prod write must never execute")
	}
	audits, _ := store.ListAgentAudit(context.Background(), storage.AgentAuditFilter{SessionID: sessID})
	if len(audits) != 1 || audits[0].Status != "rejected" {
		t.Fatalf("rejected statement must be audited: %+v", audits)
	}
}

func TestPlanApproveExecuteCommit(t *testing.T) {
	p := llmtest.New("fake",
		toolRound("submit_plan", `{"goal":"插入一行","statements":["INSERT INTO t (a) VALUES (1)"],"impact":"1 行"}`),
		toolRound("run_sql", `{"db":"shop","sql":"INSERT INTO t (a) VALUES (1)"}`),
		endRound("已执行，等待提交"),
	)
	e, store, sessID, probe, log := newAgentEngine(t, p, "dev", []string{"select", "insert"},
		[]decision{{approve: true, scope: scopeOnce}, {approve: true, scope: scopeOnce}})
	if err := e.Send(context.Background(), sessID, "插入一行", nil); err != nil {
		t.Fatal(err)
	}
	if log.count("agent:plan") != 1 || log.count("agent:approval") != 1 {
		t.Fatalf("plan/approval events: %v", log.names())
	}
	if len(probe.txExecs) != 1 {
		t.Fatalf("txExecs = %v", probe.txExecs)
	}
	if log.count("agent:tx-pending") != 1 {
		t.Fatal("tx-pending must be announced at turn end")
	}
	// No audit before the commit decision.
	audits, _ := store.ListAgentAudit(context.Background(), storage.AgentAuditFilter{SessionID: sessID})
	if len(audits) != 0 {
		t.Fatalf("tx audit must be buffered until commit, got %+v", audits)
	}
	// New turns are blocked while the tx is pending.
	if err := e.Send(context.Background(), sessID, "再来", nil); err == nil || !strings.Contains(err.Error(), slugTxPending) {
		t.Fatalf("want tx-pending block, got %v", err)
	}
	if err := e.CommitTx(context.Background(), sessID); err != nil {
		t.Fatal(err)
	}
	if !probe.committed || !probe.closed {
		t.Fatalf("commit=%v closed=%v", probe.committed, probe.closed)
	}
	audits, _ = store.ListAgentAudit(context.Background(), storage.AgentAuditFilter{SessionID: sessID})
	if len(audits) != 1 || audits[0].Status != "ok" || audits[0].Class != "insert" || audits[0].Approval != "manual" {
		t.Fatalf("audit after commit = %+v", audits)
	}
}

func TestRejectionFedBackToModel(t *testing.T) {
	p := llmtest.New("fake",
		toolRound("submit_plan", `{"goal":"g","statements":["DELETE FROM t"]}`),
		endRound("好的，不执行"),
	)
	e, store, sessID, _, _ := newAgentEngine(t, p, "dev", []string{"select", "delete"},
		[]decision{{approve: false, reason: "太危险"}})
	if err := e.Send(context.Background(), sessID, "清空表", nil); err != nil {
		t.Fatal(err)
	}
	r := lastToolResult(t, store, sessID)
	if !r.IsError || !strings.Contains(r.Content, "太危险") {
		t.Fatalf("rejection reason must reach the model, got %+v", r)
	}
}

func TestTaskVerbAutoApprove(t *testing.T) {
	p := llmtest.New("fake",
		toolRound("submit_plan", `{"goal":"两次插入","statements":["INSERT ...","INSERT ..."]}`),
		toolRound("run_sql", `{"db":"d","sql":"INSERT INTO t (a) VALUES (1)"}`),
		toolRound("run_sql", `{"db":"d","sql":"INSERT INTO t (a) VALUES (2)"}`),
		endRound("done"),
	)
	// Plan approve + first insert approve with task-verb scope; second insert
	// must NOT produce an approval event.
	e, _, sessID, probe, log := newAgentEngine(t, p, "dev", []string{"select", "insert"},
		[]decision{{approve: true, scope: scopeOnce}, {approve: true, scope: scopeTaskVerb}})
	if err := e.Send(context.Background(), sessID, "插两行", nil); err != nil {
		t.Fatal(err)
	}
	if got := log.count("agent:approval"); got != 1 {
		t.Fatalf("want exactly 1 approval event, got %d", got)
	}
	if len(probe.txExecs) != 2 {
		t.Fatalf("txExecs = %v", probe.txExecs)
	}
	_ = e.RollbackTx(context.Background(), sessID)
}
