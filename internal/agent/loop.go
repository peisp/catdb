package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

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

// Send runs one full agent turn for sessID: persists the user message, then
// loops model ↔ tools until the model stops or the iteration cap is hit.
// It blocks until the turn ends; cancelling ctx (front-end promise cancel or
// Engine.Cancel) aborts both the LLM stream and any in-flight query.
func (e *Engine) Send(ctx context.Context, sessID, text string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := e.begin(sessID, cancel); err != nil {
		return err
	}
	defer e.end(sessID)

	err := e.run(ctx, sessID, text)
	if err != nil && !errors.Is(err, context.Canceled) {
		e.emit("agent:error", map[string]any{"sessId": sessID, "slug": "agent.loop-failed", "detail": err.Error()})
	}
	return err
}

func (e *Engine) run(ctx context.Context, sessID, text string) error {
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

	tools := buildTools(toolEnv{
		conn:    conn,
		dialect: drv.Dialect(),
		caps:    drv.Capabilities(),
		privacy: e.settingBool(ctx, "agent.privacy.sendRowData", true),
	})
	system := buildSystemPrompt(promptEnv{
		driverName:    drv.Name(),
		driverVersion: drv.Version(),
		quoteSample:   quoteSampleOf(drv.Dialect()),
		currentDB:     sess.CurrentDB,
		currentSchema: sess.CurrentSchema,
		mode:          sess.Mode,
		locale:        e.setting(ctx, "ui.locale"),
		hasTools:      len(tools) > 0,
	})

	if _, err := e.store.AppendAgentMessage(ctx, storage.AgentMessage{
		SessionID: sessID, Role: "user", Content: mustContent(msgContent{Text: text}),
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

	for iter := 0; iter < e.maxIterations; iter++ {
		turn, err := e.streamTurn(ctx, em, provider, llm.ChatRequest{
			Model:     sess.Model,
			System:    system,
			Messages:  messages,
			Tools:     defs,
			MaxTokens: 8192,
		}, sessID)
		if err != nil {
			return fmt.Errorf("agent: llm stream: %w", err)
		}

		// Persist the assistant turn (with usage) and mirror it into history.
		calls := make([]storedCall, len(turn.toolCalls))
		for i, c := range turn.toolCalls {
			calls[i] = storedCall{ID: c.ID, Name: c.Name, Args: c.Args}
		}
		tokIn, tokOut := turn.usage.InputTokens+turn.usage.CacheReadTokens+turn.usage.CacheWriteTokens, turn.usage.OutputTokens
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

		if len(turn.toolCalls) == 0 || turn.stop != llm.StopToolUse {
			em.send("agent:done", map[string]any{"sessId": sessID, "stopReason": string(turn.stop)})
			return nil
		}

		results := e.execTools(ctx, em, sessID, byName, turn.toolCalls)
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
	em.send("agent:done", map[string]any{"sessId": sessID, "stopReason": "max_iterations"})
	return nil
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
		em.send("agent:tool", map[string]any{
			"sessId": sessID, "callId": c.ID, "name": c.Name, "phase": "end",
			"summary": summarize(results[i]),
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

// loadHistory rebuilds the LLM message sequence from persisted messages,
// skipping compacted ones (§9: compaction affects only what the model sees).
func (e *Engine) loadHistory(ctx context.Context, sessID string) ([]llm.Message, error) {
	stored, err := e.store.ListAgentMessages(ctx, sessID)
	if err != nil {
		return nil, fmt.Errorf("agent: load history: %w", err)
	}
	var out []llm.Message
	for _, m := range stored {
		if m.Compacted {
			continue
		}
		var c msgContent
		if err := json.Unmarshal([]byte(m.Content), &c); err != nil {
			return nil, fmt.Errorf("agent: decode message %s: %w", m.ID, err)
		}
		switch m.Role {
		case "assistant":
			calls := make([]llm.ToolCall, len(c.ToolCalls))
			for i, sc := range c.ToolCalls {
				calls[i] = llm.ToolCall{ID: sc.ID, Name: sc.Name, Args: sc.Args}
			}
			out = append(out, llm.Message{Role: llm.RoleAssistant, Text: c.Text, ToolCalls: calls})
		case "tool":
			if c.Result != nil {
				out = append(out, llm.Message{Role: llm.RoleTool, ToolResult: &llm.ToolResult{
					CallID: c.Result.CallID, Content: wrapToolResult(c.Result.Content, c.Result.IsError), IsError: c.Result.IsError,
				}})
			}
		default:
			out = append(out, llm.Message{Role: llm.RoleUser, Text: c.Text})
		}
	}
	return out, nil
}

func (e *Engine) contextWindowOf(ctx context.Context, p llm.Provider, model string) int {
	models, err := p.Models(ctx)
	if err != nil {
		return 0
	}
	for _, m := range models {
		if m.ID == model {
			return m.ContextWindow
		}
	}
	return 0
}

func (e *Engine) setting(ctx context.Context, key string) string {
	v, _ := e.store.GetSetting(ctx, key)
	return v
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
