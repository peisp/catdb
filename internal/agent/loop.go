package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"catdb/internal/dbdriver"
	"catdb/internal/llm"
	"catdb/internal/storage"
)

// emitter stamps every streamed event of one turn with a monotonically
// increasing seq. The Wails event channel does not guarantee delivery order;
// the front-end reassembles by seq (api/agent.ts) — without this, delta text
// can render scrambled. Mutex-guarded: parallel tool goroutines emit too.
type emitter struct {
	emit func(name string, data any)
	mu   sync.Mutex
	seq  int
}

func (em *emitter) send(name string, data map[string]any) {
	em.mu.Lock()
	data["seq"] = em.seq
	em.seq++
	em.mu.Unlock()
	em.emit(name, data)
}

// Stored message content (agent_messages.content JSON). Mirrors llm.Message
// so history rebuilds losslessly; thinking is kept for display only and never
// resent to the model.
type msgContent struct {
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ToolCalls []storedCall    `json:"toolCalls,omitempty"`
	Result    *storedResult   `json:"result,omitempty"`
	Extra     json.RawMessage `json:"extra,omitempty"` // room for later milestones (plans, approvals)
}

type storedCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type storedResult struct {
	CallID  string `json:"callId"`
	Content string `json:"content"`
	IsError bool   `json:"isError,omitempty"`
}

// msgMentions is the Extra payload of a user message carrying @table mentions
// (§10.3): each table's structure is fetched at send time and pinned with the
// message. The chat panel renders the chips from Tables[].Name.
type msgMentions struct {
	Tables []mentionTable `json:"tables"`
	// Truncated: the user @-mentioned more tables than the cap — the prompt
	// must say the structure list is incomplete (§4.3).
	Truncated bool `json:"truncated,omitempty"`
}

type mentionTable struct {
	DB        string `json:"db,omitempty"`
	Schema    string `json:"schema,omitempty"`
	Name      string `json:"name"`
	Structure string `json:"structure"` // compact JSON: columns/indexes/foreignKeys/comment
}

// Send runs one full agent turn for sessID: persists the user message (with
// any @table mentions, §10.3), then loops model ↔ tools until the model stops
// or the iteration cap is hit. It blocks until the turn ends; cancelling ctx
// (front-end promise cancel or Engine.Cancel) aborts both the LLM stream and
// any in-flight query.
func (e *Engine) Send(ctx context.Context, sessID, text string, mentions []string) error {
	if e.txm.get(sessID) != nil {
		// A task transaction awaits commit/rollback — no new turns until the
		// user decides (§5 gate 5).
		return fmt.Errorf("%s: commit or roll back the pending transaction first", slugTxPending)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := e.begin(sessID, cancel); err != nil {
		return err
	}
	defer e.end(sessID)

	err := e.run(ctx, sessID, text, mentions)
	if err != nil && !errors.Is(err, context.Canceled) {
		e.emit("agent:error", map[string]any{"sessId": sessID, "slug": "agent.loop-failed", "detail": err.Error()})
	}
	return err
}

func (e *Engine) run(ctx context.Context, sessID, text string, mentions []string) error {
	sess, err := e.store.GetAgentSession(ctx, sessID)
	if err != nil {
		return fmt.Errorf("agent: load session: %w", err)
	}
	if sess.Title == "" {
		_ = e.store.UpdateAgentSessionMeta(ctx, sessID, storage.AgentSessionMeta{
			Title: truncate(text, 50), Mode: sess.Mode, Grants: sess.Grants,
			ProviderID: sess.ProviderID, Model: sess.Model,
			CurrentDB: sess.CurrentDB, CurrentSchema: sess.CurrentSchema,
		})
	}
	provider, err := e.resolve(ctx, sess.ProviderID)
	if err != nil {
		return fmt.Errorf("agent: resolve provider: %w", err)
	}

	conn, drv, err := e.connect(ctx, sess.ConnID)
	if err != nil {
		return err
	}
	em := &emitter{emit: e.emit}

	// Tool-less model degradation (§3.1): Ask mode falls back to a schema
	// overview injected into the prompt + plain completion; Agent mode needs
	// tools and asks for a different model.
	mi := e.modelInfoOf(ctx, provider, sess.Model)
	toolless := mi.ID != "" && !mi.SupportsTools
	schemaOverview := ""
	var tools []Tool
	if toolless {
		if sess.Mode == "agent" {
			return fmt.Errorf("agent.model-no-tools: model %q does not support tool calls — switch to a tool-capable model for Agent mode", sess.Model)
		}
		schemaOverview = buildSchemaOverview(ctx, conn, sess.CurrentDB, sess.CurrentSchema)
	} else {
		tools = buildTools(toolEnv{
			conn:    conn,
			dialect: drv.Dialect(),
			caps:    drv.Capabilities(),
			privacy: e.settingBool(ctx, "agent.privacy.sendRowData", true),
		})
	}
	if !toolless && sess.Mode == "agent" {
		override, _ := drv.Dialect().(dbdriver.StatementClassifier)
		rs := &runState{
			sessID: sessID, connID: sess.ConnID, mode: sess.Mode,
			defaultDB: sess.CurrentDB, defaultSchema: sess.CurrentSchema,
			conn: conn, dialect: drv.Dialect(), caps: drv.Capabilities(),
			rules: drv.Dialect().ScriptRules(), override: override,
			em: em, e: e,
			autoVerbs: map[dbdriver.StatementVerb]bool{},
		}
		var granted []string
		for _, g := range sess.Grants {
			if g != "select" {
				granted = append(granted, g)
			}
		}
		tools = append(tools, buildRunSQL(rs, granted), buildSubmitPlan(rs))
	}
	environment := ""
	if prof, perr := e.store.GetConnection(ctx, sess.ConnID); perr == nil {
		environment = prof.Environment
	}
	system := buildSystemPrompt(promptEnv{
		driverName:     drv.Name(),
		driverVersion:  drv.Version(),
		quoteSample:    quoteSampleOf(drv.Dialect()),
		currentDB:      sess.CurrentDB,
		currentSchema:  sess.CurrentSchema,
		mode:           sess.Mode,
		environment:    environment,
		locale:         e.setting(ctx, "ui.locale"),
		hasTools:       len(tools) > 0,
		schemaOverview: schemaOverview,
	})

	userMsg := msgContent{Text: text}
	if len(mentions) > 0 {
		if tables, truncated := e.fetchMentions(ctx, conn, sess.CurrentDB, sess.CurrentSchema, mentions); len(tables) > 0 {
			extra, err := json.Marshal(msgMentions{Tables: tables, Truncated: truncated})
			if err == nil {
				userMsg.Extra = extra
			}
		}
	}
	if _, err := e.store.AppendAgentMessage(ctx, storage.AgentMessage{
		SessionID: sessID, Role: "user", Content: mustContent(userMsg),
	}); err != nil {
		return fmt.Errorf("agent: persist user message: %w", err)
	}

	messages, err := e.loadHistory(ctx, sessID)
	if err != nil {
		return err
	}

	defs := make([]llm.ToolDef, len(tools))
	byName := make(map[string]Tool, len(tools))
	for i, t := range tools {
		defs[i] = t.Def
		byName[t.Def.Name] = t
	}
	contextWindow := e.contextWindowOf(ctx, provider, sess.Model)
	threshold := e.compactThreshold(ctx)
	compactAuto := e.settingBool(ctx, "agent.compact.auto", true)
	maxIter := e.settingInt(ctx, "agent.limits.maxIterations", e.maxIterations)
	tokenBudget := e.settingInt(ctx, "agent.limits.sessionTokenBudget", 0)
	sessionTokens := e.sessionTokenTotal(ctx, sessID)
	emitMap := func(name string, data map[string]any) { em.send(name, data) }
	overflowRetried := false
	repairs := 0
	ranSQL := false

	for iter := 0; iter < maxIter; iter++ {
		// Session token budget (§4.1): pause before the next model call once
		// exceeded — the user raises the budget or compacts, then replies to
		// continue.
		if tokenBudget > 0 && sessionTokens >= tokenBudget {
			e.emitTxPending(em, sessID)
			em.send("agent:done", map[string]any{"sessId": sessID, "stopReason": "token_budget"})
			return nil
		}
		// Level-1 eviction (build-time, lossless): trim old tool results from
		// the request copy once the estimate crosses the threshold.
		reqMsgs := messages
		if contextWindow > 0 && float64(estimateTokens(messages)) > threshold*float64(contextWindow) {
			reqMsgs = evictOldToolResults(messages, keepTailRounds)
		}
		turn, err := e.streamTurn(ctx, em, provider, llm.ChatRequest{
			Model:     sess.Model,
			System:    system,
			Messages:  reqMsgs,
			Tools:     defs,
			MaxTokens: 8192,
		}, sessID)
		if err != nil {
			// Passive compaction (§9): a context-overflow error forces a fold
			// and one retry — watermark estimates can undershoot.
			if isContextOverflow(err) && !overflowRetried {
				overflowRetried = true
				if n, cerr := e.compactSession(ctx, sessID, provider, sess.Model, emitMap); cerr == nil && n > 0 {
					if messages, err = e.loadHistory(ctx, sessID); err == nil {
						iter--
						continue
					}
				}
			}
			return fmt.Errorf("agent: llm stream: %w", err)
		}

		// Persist the assistant turn (with usage) and mirror it into history.
		calls := make([]storedCall, len(turn.toolCalls))
		for i, c := range turn.toolCalls {
			calls[i] = storedCall{ID: c.ID, Name: c.Name, Args: c.Args}
		}
		tokIn, tokOut := turn.usage.InputTokens+turn.usage.CacheReadTokens+turn.usage.CacheWriteTokens, turn.usage.OutputTokens
		sessionTokens += tokIn + tokOut
		if _, err := e.store.AppendAgentMessage(ctx, storage.AgentMessage{
			SessionID: sessID, Role: "assistant",
			Content:  mustContent(msgContent{Text: turn.text, Thinking: turn.thinking, ToolCalls: calls}),
			TokensIn: &tokIn, TokensOut: &tokOut,
		}); err != nil {
			return fmt.Errorf("agent: persist assistant message: %w", err)
		}
		messages = append(messages, llm.Message{Role: llm.RoleAssistant, Text: turn.text, ToolCalls: turn.toolCalls})

		watermark := 0.0
		if contextWindow > 0 {
			watermark = float64(tokIn+tokOut) / float64(contextWindow)
		}
		em.send("agent:usage", map[string]any{
			"sessId": sessID, "tokensIn": tokIn, "tokensOut": tokOut, "watermark": watermark,
		})

		// Auto-compaction (§9): fold early rounds once the real usage crosses
		// the threshold, then rebuild the context for the next iteration.
		if compactAuto && contextWindow > 0 && watermark > threshold {
			if n, cerr := e.compactSession(ctx, sessID, provider, sess.Model, emitMap); cerr == nil && n > 0 {
				if rebuilt, lerr := e.loadHistory(ctx, sessID); lerr == nil {
					messages = rebuilt
				}
			}
		}

		if len(turn.toolCalls) == 0 || turn.stop != llm.StopToolUse {
			// Delivery validation (§6/§8): repair-and-retry a contract-breaking
			// final answer, capped; then deliver with a warning, never discard.
			if v := validateDelivery(sess.Mode, turn.text, ranSQL); !v.OK && repairs < maxDeliveryRepairs {
				repairs++
				messages = append(messages, llm.Message{Role: llm.RoleUser, Text: repairMessage(v.Missing)})
				continue
			} else if !v.OK {
				e.emitTxPending(em, sessID)
				em.send("agent:done", map[string]any{"sessId": sessID, "stopReason": string(turn.stop), "deliveryWarning": true})
				return nil
			}
			e.emitTxPending(em, sessID)
			em.send("agent:done", map[string]any{"sessId": sessID, "stopReason": string(turn.stop)})
			return nil
		}

		results := e.execTools(ctx, em, sessID, byName, turn.toolCalls)
		for i, r := range results {
			if !r.IsError && turn.toolCalls[i].Name == "run_sql" {
				ranSQL = true
			}
		}
		for _, r := range results {
			if _, err := e.store.AppendAgentMessage(ctx, storage.AgentMessage{
				SessionID: sessID, Role: "tool", Content: mustContent(msgContent{Result: &r}),
			}); err != nil {
				return fmt.Errorf("agent: persist tool result: %w", err)
			}
			messages = append(messages, llm.Message{Role: llm.RoleTool, ToolResult: &llm.ToolResult{
				CallID: r.CallID, Content: wrapToolResult(r.Content, r.IsError), IsError: r.IsError,
			}})
		}
	}

	// Iteration cap: not a failure — keep what was produced, the front-end
	// renders the "reply 继续 to keep going" hint off this stop reason (§4.1).
	e.emitTxPending(em, sessID)
	em.send("agent:done", map[string]any{"sessId": sessID, "stopReason": "max_iterations"})
	return nil
}

// fetchMentions resolves @table mentions against the session's current
// namespace: full structure (columns/indexes/foreign keys) per table, capped
// at 8 (§10.3). A table that fails to resolve is skipped — the model can
// still look it up with tools.
func (e *Engine) fetchMentions(ctx context.Context, conn dbdriver.Connection, db, schema string, names []string) ([]mentionTable, bool) {
	meta := conn.Metadata()
	if meta == nil {
		return nil, false
	}
	truncated := false
	if len(names) > 8 {
		names = names[:8]
		truncated = true
	}
	var out []mentionTable
	for _, name := range names {
		cols, err := meta.ListColumns(ctx, db, schema, name)
		if err != nil || len(cols) == 0 {
			continue
		}
		idx, _ := meta.ListIndexes(ctx, db, schema, name)
		fks, _ := meta.ListForeignKeys(ctx, db, schema, name)
		b, err := json.Marshal(map[string]any{"columns": cols, "indexes": idx, "foreignKeys": fks})
		if err != nil {
			continue
		}
		out = append(out, mentionTable{DB: db, Schema: schema, Name: name, Structure: string(b)})
	}
	return out, truncated
}

// emitTxPending announces an open task transaction awaiting the user's
// commit/rollback decision (§5 gate 5) when the turn ends.
func (e *Engine) emitTxPending(em *emitter, sessID string) {
	t := e.txm.get(sessID)
	if t == nil {
		return
	}
	em.send("agent:tx-pending", map[string]any{"sessId": sessID, "statements": t.statements()})
}

// turnData is everything one model turn produced.
type turnData struct {
	text      string
	thinking  string
	toolCalls []llm.ToolCall
	usage     llm.Usage
	stop      llm.StopReason
}

// streamTurn drains one ChatStream to io.EOF (some providers deliver Usage
// after Stop — never stop reading at the Stop event), emitting deltas as they
// arrive and assembling tool calls from fragments.
func (e *Engine) streamTurn(ctx context.Context, em *emitter, p llm.Provider, req llm.ChatRequest, sessID string) (turnData, error) {
	stream, err := p.ChatStream(ctx, req)
	if err != nil {
		return turnData{}, err
	}
	defer stream.Close()

	var t turnData
	frags := map[string]*[]byte{}
	for {
		ev, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return turnData{}, err
		}
		switch v := ev.(type) {
		case llm.TextDelta:
			t.text += v.Text
			em.send("agent:delta", map[string]any{"sessId": sessID, "text": v.Text})
		case llm.ThinkingDelta:
			t.thinking += v.Text
			em.send("agent:thinking", map[string]any{"sessId": sessID, "text": v.Text})
		case llm.ToolCallStart:
			t.toolCalls = append(t.toolCalls, llm.ToolCall{ID: v.ID, Name: v.Name})
			buf := []byte{}
			frags[v.ID] = &buf
			em.send("agent:tool", map[string]any{"sessId": sessID, "callId": v.ID, "name": v.Name, "phase": "start"})
		case llm.ToolCallDelta:
			if buf := frags[v.ID]; buf != nil {
				*buf = append(*buf, v.ArgsFragment...)
			}
		case llm.Usage:
			t.usage = v
		case llm.Stop:
			t.stop = v.Reason
		}
	}
	for i := range t.toolCalls {
		if buf := frags[t.toolCalls[i].ID]; buf != nil && len(*buf) > 0 {
			t.toolCalls[i].Args = json.RawMessage(*buf)
		}
	}
	return t, nil
}

// execTools runs one round of tool calls: ParallelOK tools run concurrently,
// the rest sequentially in call order (§4.2). Results come back in call order.
func (e *Engine) execTools(ctx context.Context, em *emitter, sessID string, byName map[string]Tool, calls []llm.ToolCall) []storedResult {
	results := make([]storedResult, len(calls))
	var wg sync.WaitGroup
	runOne := func(i int, c llm.ToolCall) {
		tool, ok := byName[c.Name]
		if !ok {
			results[i] = storedResult{CallID: c.ID, Content: fmt.Sprintf("unknown tool %q", c.Name), IsError: true}
			return
		}
		out, err := tool.Run(ctx, c.Args)
		if err != nil {
			results[i] = storedResult{CallID: c.ID, Content: err.Error(), IsError: true}
		} else {
			results[i] = storedResult{CallID: c.ID, Content: out}
		}
		// args/result/isError ride along so the live card can offer the same
		// expand view and error tint as a history reload (§10.4).
		em.send("agent:tool", map[string]any{
			"sessId": sessID, "callId": c.ID, "name": c.Name, "phase": "end",
			"summary": summarize(results[i]),
			"args":    string(c.Args),
			"result":  results[i].Content,
			"isError": results[i].IsError,
		})
	}
	for i, c := range calls {
		tool, ok := byName[c.Name]
		if ok && tool.ParallelOK {
			wg.Add(1)
			go func(i int, c llm.ToolCall) { defer wg.Done(); runOne(i, c) }(i, c)
		} else {
			runOne(i, c)
		}
	}
	wg.Wait()
	return results
}

func summarize(r storedResult) string {
	if r.IsError {
		return "error: " + truncate(r.Content, 120)
	}
	return truncate(r.Content, 120)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// loadHistory rebuilds the LLM message sequence from persisted messages in
// logical order (anchor → summaries → live rounds), skipping compacted ones
// (§9: compaction affects only what the model sees).
func (e *Engine) loadHistory(ctx context.Context, sessID string) ([]llm.Message, error) {
	logical, err := e.loadLogical(ctx, sessID)
	if err != nil {
		return nil, err
	}
	return toLLMMessages(logical), nil
}

func (e *Engine) contextWindowOf(ctx context.Context, p llm.Provider, model string) int {
	return e.modelInfoOf(ctx, p, model).ContextWindow
}

// modelInfoOf finds the session model's configured info; zero value when the
// model isn't in the provider's list (unknown models are assumed
// tool-capable — most are).
func (e *Engine) modelInfoOf(ctx context.Context, p llm.Provider, model string) llm.ModelInfo {
	models, err := p.Models(ctx)
	if err != nil {
		return llm.ModelInfo{}
	}
	for _, m := range models {
		if m.ID == model {
			return m
		}
	}
	return llm.ModelInfo{}
}

// buildSchemaOverview is the tool-less degradation context (§3.1): database
// list + current database's tables, capped.
func buildSchemaOverview(ctx context.Context, conn dbdriver.Connection, db, schema string) string {
	meta := conn.Metadata()
	if meta == nil {
		return ""
	}
	var b strings.Builder
	if dbs, err := meta.ListDatabases(ctx); err == nil {
		if len(dbs) > 50 {
			dbs = dbs[:50]
		}
		fmt.Fprintf(&b, "Databases: %s\n", strings.Join(dbs, ", "))
	}
	if db != "" {
		if ts, err := meta.ListTables(ctx, db, schema); err == nil {
			if len(ts) > 100 {
				ts = ts[:100]
			}
			fmt.Fprintf(&b, "Tables in %s:\n", db)
			for _, t := range ts {
				if t.Comment != "" {
					fmt.Fprintf(&b, "- %s (%s)\n", t.Name, t.Comment)
				} else {
					fmt.Fprintf(&b, "- %s\n", t.Name)
				}
			}
		}
	}
	return b.String()
}

func (e *Engine) setting(ctx context.Context, key string) string {
	v, _ := e.store.GetSetting(ctx, key)
	return v
}

func (e *Engine) settingInt(ctx context.Context, key string, def int) int {
	if v := e.setting(ctx, key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

// sessionTokenTotal sums the session's recorded usage (budget accounting).
func (e *Engine) sessionTokenTotal(ctx context.Context, sessID string) int {
	msgs, err := e.store.ListAgentMessages(ctx, sessID)
	if err != nil {
		return 0
	}
	total := 0
	for _, m := range msgs {
		if m.TokensIn != nil {
			total += *m.TokensIn
		}
		if m.TokensOut != nil {
			total += *m.TokensOut
		}
	}
	return total
}

func (e *Engine) settingBool(ctx context.Context, key string, def bool) bool {
	switch e.setting(ctx, key) {
	case "true", "1":
		return true
	case "false", "0":
		return false
	}
	return def
}

func mustContent(c msgContent) string {
	b, err := json.Marshal(c)
	if err != nil {
		// msgContent is marshal-safe by construction; this cannot happen.
		panic(err)
	}
	return string(b)
}
