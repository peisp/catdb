package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"catdb/internal/dbdriver"
	"catdb/internal/llm"
	"catdb/internal/llm/llmtest"
	"catdb/internal/storage"
)

// --- fakes ---

type fakeMeta struct{ dbdriver.Metadata }

func (fakeMeta) ListDatabases(ctx context.Context) ([]string, error) {
	return []string{"shop", "test"}, nil
}
func (fakeMeta) ListTables(ctx context.Context, db, schema string) ([]dbdriver.TableInfo, error) {
	return []dbdriver.TableInfo{{Name: "orders", Comment: "订单表"}}, nil
}
func (fakeMeta) ListColumns(ctx context.Context, db, schema, table string) ([]dbdriver.ColumnMeta, error) {
	return []dbdriver.ColumnMeta{{Name: "id"}, {Name: "total"}}, nil
}
func (fakeMeta) ListIndexes(ctx context.Context, db, schema, table string) ([]dbdriver.IndexInfo, error) {
	return nil, nil
}
func (fakeMeta) ListForeignKeys(ctx context.Context, db, schema, table string) ([]dbdriver.ForeignKeyInfo, error) {
	return nil, nil
}
func (fakeMeta) GetCreateTable(ctx context.Context, db, schema, table string) (string, error) {
	return "CREATE TABLE `orders` (...)", nil
}

type fakeConn struct{ dbdriver.Connection }

func (fakeConn) Metadata() dbdriver.Metadata { return fakeMeta{} }
func (fakeConn) Querier() dbdriver.Querier   { return nil }

type fakeDialect struct{ dbdriver.Dialect }

func (fakeDialect) QuoteIdentifier(name string) string { return "`" + name + "`" }
func (fakeDialect) Paginate(baseSQL string, limit, offset int) string {
	return baseSQL + " LIMIT 50"
}
func (fakeDialect) ScriptRules() dbdriver.ScriptRules { return dbdriver.ScriptRules{} }

type fakeDriver struct{ dbdriver.Driver }

func (fakeDriver) Name() string    { return "mysql" }
func (fakeDriver) Version() string { return "8.0-test" }
func (fakeDriver) Capabilities() dbdriver.Capabilities {
	return dbdriver.Capabilities{Views: false, ExplainPlan: false}
}
func (fakeDriver) Dialect() dbdriver.Dialect { return fakeDialect{} }

// eventLog captures emitted events, thread-safe (tools run concurrently).
type eventLog struct {
	mu     sync.Mutex
	events []struct {
		Name string
		Data map[string]any
	}
}

func (l *eventLog) emit(name string, data any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, struct {
		Name string
		Data map[string]any
	}{name, data.(map[string]any)})
}

func (l *eventLog) names() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.events))
	for i, e := range l.events {
		out[i] = e.Name
	}
	return out
}

func (l *eventLog) count(name string) int {
	n := 0
	for _, e := range l.names() {
		if e == name {
			n++
		}
	}
	return n
}

func newTestEngine(t *testing.T, p llm.Provider) (*Engine, *storage.Store, *eventLog) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	log := &eventLog{}
	e := &Engine{
		store:   store,
		resolve: func(ctx context.Context, id string) (llm.Provider, error) { return p, nil },
		connect: func(ctx context.Context, id string) (dbdriver.Connection, dbdriver.Driver, error) {
			return fakeConn{}, fakeDriver{}, nil
		},
		emit:          log.emit,
		broker:        newApprovalBroker(),
		txm:           newTxManager(),
		maxIterations: 25,
	}
	return e, store, log
}

func newTestSession(t *testing.T, store *storage.Store) storage.AgentSession {
	t.Helper()
	sess, err := store.CreateAgentSession(context.Background(), storage.AgentSession{
		ConnID: "c1", Mode: "ask", ProviderID: "p1", Model: "m1", Grants: []string{"select"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

// --- tests ---

func TestTextOnlyTurn(t *testing.T) {
	p := llmtest.New("fake", []llm.Event{
		llm.TextDelta{Text: "Hello "},
		llm.TextDelta{Text: "world"},
		llm.Usage{InputTokens: 10, OutputTokens: 5},
		llm.Stop{Reason: llm.StopEndTurn},
	})
	e, store, log := newTestEngine(t, p)
	sess := newTestSession(t, store)

	if err := e.Send(context.Background(), sess.ID, "hi", nil); err != nil {
		t.Fatal(err)
	}
	if got := log.count("agent:delta"); got != 2 {
		t.Fatalf("want 2 deltas, got %d", got)
	}
	if got := log.count("agent:done"); got != 1 {
		t.Fatalf("want 1 done, got %d", got)
	}
	msgs, _ := store.ListAgentMessages(context.Background(), sess.ID)
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("unexpected persisted messages: %+v", msgs)
	}
	var c msgContent
	json.Unmarshal([]byte(msgs[1].Content), &c)
	if c.Text != "Hello world" {
		t.Fatalf("assistant text = %q", c.Text)
	}
	if msgs[1].TokensIn == nil || *msgs[1].TokensIn != 10 {
		t.Fatalf("tokensIn = %v", msgs[1].TokensIn)
	}
	// Session title auto-set from first message.
	got, _ := store.GetAgentSession(context.Background(), sess.ID)
	if got.Title != "hi" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestToolCallRound(t *testing.T) {
	p := llmtest.New("fake",
		[]llm.Event{
			llm.ToolCallStart{ID: "t1", Name: "list_tables"},
			llm.ToolCallDelta{ID: "t1", ArgsFragment: `{"db":"shop"`},
			llm.ToolCallDelta{ID: "t1", ArgsFragment: `}`},
			llm.Usage{InputTokens: 20, OutputTokens: 8},
			llm.Stop{Reason: llm.StopToolUse},
		},
		[]llm.Event{
			llm.TextDelta{Text: "orders 表包含订单"},
			llm.Usage{InputTokens: 40, OutputTokens: 12},
			llm.Stop{Reason: llm.StopEndTurn},
		},
	)
	e, store, log := newTestEngine(t, p)
	sess := newTestSession(t, store)

	if err := e.Send(context.Background(), sess.ID, "shop 库有哪些表", nil); err != nil {
		t.Fatal(err)
	}
	// user, assistant(tool call), tool result, assistant(final)
	msgs, _ := store.ListAgentMessages(context.Background(), sess.ID)
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages, got %d", len(msgs))
	}
	if msgs[2].Role != "tool" {
		t.Fatalf("msg[2].role = %s", msgs[2].Role)
	}
	var c msgContent
	json.Unmarshal([]byte(msgs[2].Content), &c)
	if c.Result == nil || c.Result.IsError || !strings.Contains(c.Result.Content, "orders") {
		t.Fatalf("tool result = %+v", c.Result)
	}
	if got := log.count("agent:tool"); got != 2 { // start + end
		t.Fatalf("want 2 tool events, got %d", got)
	}
	// Every streamed event carries a per-turn seq covering 0..n-1 exactly once
	// (the front-end reassembles by it — the event channel is not ordered).
	seen := map[int]bool{}
	for _, ev := range log.events {
		seq, ok := ev.Data["seq"].(int)
		if !ok {
			t.Fatalf("event %s missing seq: %v", ev.Name, ev.Data)
		}
		if seen[seq] {
			t.Fatalf("duplicate seq %d", seq)
		}
		seen[seq] = true
	}
	for i := 0; i < len(seen); i++ {
		if !seen[i] {
			t.Fatalf("seq %d missing (have %v)", i, seen)
		}
	}

	// Second LLM call must include the tool result wrapped in delimiters.
	req2 := p.Requests[1]
	last := req2.Messages[len(req2.Messages)-1]
	if last.Role != llm.RoleTool || !strings.Contains(last.ToolResult.Content, "<tool_result>") {
		t.Fatalf("tool result not wrapped: %+v", last)
	}
	// Args assembled from fragments.
	var mid llm.Message
	for _, m := range req2.Messages {
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0 {
			mid = m
		}
	}
	if string(mid.ToolCalls[0].Args) != `{"db":"shop"}` {
		t.Fatalf("args = %s", mid.ToolCalls[0].Args)
	}
}

func TestUnknownToolBecomesError(t *testing.T) {
	p := llmtest.New("fake",
		[]llm.Event{
			llm.ToolCallStart{ID: "t1", Name: "no_such_tool"},
			llm.Stop{Reason: llm.StopToolUse},
		},
		[]llm.Event{
			llm.TextDelta{Text: "ok"},
			llm.Stop{Reason: llm.StopEndTurn},
		},
	)
	e, store, _ := newTestEngine(t, p)
	sess := newTestSession(t, store)
	if err := e.Send(context.Background(), sess.ID, "x", nil); err != nil {
		t.Fatal(err)
	}
	msgs, _ := store.ListAgentMessages(context.Background(), sess.ID)
	var c msgContent
	json.Unmarshal([]byte(msgs[2].Content), &c)
	if c.Result == nil || !c.Result.IsError {
		t.Fatalf("want error tool result, got %+v", c.Result)
	}
}

func TestIterationCap(t *testing.T) {
	// Every round issues another tool call; the loop must stop at the cap
	// and emit done with max_iterations, not fail.
	loopRound := []llm.Event{
		llm.ToolCallStart{ID: "t", Name: "list_databases"},
		llm.Stop{Reason: llm.StopToolUse},
	}
	scripts := make([][]llm.Event, 30)
	for i := range scripts {
		scripts[i] = loopRound
	}
	p := llmtest.New("fake", scripts...)
	e, store, log := newTestEngine(t, p)
	e.maxIterations = 3
	sess := newTestSession(t, store)

	if err := e.Send(context.Background(), sess.ID, "x", nil); err != nil {
		t.Fatal(err)
	}
	if len(p.Requests) != 3 {
		t.Fatalf("want 3 llm calls, got %d", len(p.Requests))
	}
	var done map[string]any
	for _, ev := range log.events {
		if ev.Name == "agent:done" {
			done = ev.Data
		}
	}
	if done == nil || done["stopReason"] != "max_iterations" {
		t.Fatalf("done = %v", done)
	}
}

func TestConcurrentSendRejected(t *testing.T) {
	p := llmtest.New("fake", []llm.Event{llm.Stop{Reason: llm.StopEndTurn}})
	e, store, _ := newTestEngine(t, p)
	sess := newTestSession(t, store)
	if err := e.begin(sess.ID, func() {}); err != nil {
		t.Fatal(err)
	}
	if err := e.Send(context.Background(), sess.ID, "x", nil); err == nil {
		t.Fatal("want busy error")
	}
}

func TestCancelStopsLoop(t *testing.T) {
	p := llmtest.New("fake", []llm.Event{
		llm.TextDelta{Text: "a"},
		llm.Stop{Reason: llm.StopEndTurn},
	})
	e, store, _ := newTestEngine(t, p)
	sess := newTestSession(t, store)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Send(ctx, sess.ID, "x", nil); err == nil {
		t.Fatal("want ctx error")
	}
}

func TestSystemPromptAndTools(t *testing.T) {
	p := llmtest.New("fake", []llm.Event{llm.TextDelta{Text: "ok"}, llm.Stop{Reason: llm.StopEndTurn}})
	e, store, _ := newTestEngine(t, p)
	sess := newTestSession(t, store)
	if err := e.Send(context.Background(), sess.ID, "x", nil); err != nil {
		t.Fatal(err)
	}
	req := p.Requests[0]
	if !strings.Contains(req.System, "mysql 8.0-test") || !strings.Contains(req.System, "`name`") {
		t.Fatalf("system prompt missing dialect context:\n%s", req.System)
	}
	names := map[string]bool{}
	for _, d := range req.Tools {
		names[d.Name] = true
	}
	// Views/ExplainPlan are off in fakeDriver caps → no list_views/explain.
	if !names["list_tables"] || !names["get_table_schema"] || names["list_views"] || names["explain"] {
		t.Fatalf("unexpected tool set: %v", names)
	}
	// run_sql must never be registered in ask mode (M1 has no run_sql at all).
	if names["run_sql"] {
		t.Fatal("run_sql must not be registered")
	}
}

func TestMentionsInjectedAndPinned(t *testing.T) {
	p := llmtest.New("fake", []llm.Event{llm.TextDelta{Text: "见结构。"}, llm.Stop{Reason: llm.StopEndTurn}})
	e, store, _ := newTestEngine(t, p)
	sess := newTestSession(t, store)

	if err := e.Send(context.Background(), sess.ID, "orders 表怎么查", []string{"orders"}); err != nil {
		t.Fatal(err)
	}
	// The request's user message carries the fetched structure.
	req := p.Requests[0]
	um := req.Messages[len(req.Messages)-1]
	if um.Role != llm.RoleUser || !strings.Contains(um.Text, "Referenced table structures") ||
		!strings.Contains(um.Text, `"total"`) {
		t.Fatalf("mention structure not injected: %q", um.Text)
	}
	// The message is pinned in logical order.
	logical, err := e.loadLogical(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var pinned bool
	for _, m := range logical {
		if m.rec.Role == "user" && len(m.content.Extra) > 0 {
			pinned = m.pinned
		}
	}
	if !pinned {
		t.Fatal("mention message must be pinned")
	}
}

func TestReadOnlyPrefix(t *testing.T) {
	cases := map[string]bool{
		"SELECT 1":                        true,
		"  with x as (select 1) select *": true,
		"UPDATE t SET a=1":                false,
		"DELETE FROM t":                   false,
		"-- note\nSELECT 1":               true,
		"/* c */ SELECT 1":                true,
		"EXPLAIN ANALYZE DELETE FROM t":   false,
		"":                                false,
	}
	for sql, want := range cases {
		if got := readOnlyPrefix(sql); got != want {
			t.Errorf("readOnlyPrefix(%q) = %v, want %v", sql, got, want)
		}
	}
}
