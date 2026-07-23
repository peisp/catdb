package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"catdb/internal/llm"
	"catdb/internal/storage"
)

// Context management (AGENT_DESIGN.md §9): token watermark + two-level
// compaction. Level 1 (tool-result eviction) is a lossless, build-time
// transform — nothing is persisted. Level 2 (round folding) summarizes early
// rounds via the session's own model, appends a role="summary" message and
// marks the folded rows compacted. Invariants the implementation guarantees:
//
//   - the FIRST user message (task anchor) is never folded;
//   - a fold boundary never separates an assistant tool-call from its tool
//     results (providers reject such sequences);
//   - the task plan (submit_plan call + result) is pinned, never folded;
//   - a failed or degenerate LLM summary falls back to a statistical summary —
//     compaction never aborts because summarization failed.
//
// After the first fold, logical order diverges from seq order (summary rows
// have high seqs but sit early logically): loadLogical produces the canonical
// [anchor, summaries…, live rounds…] ordering used both for building LLM
// context and for choosing the next fold range.

const (
	keepTailRounds = 5   // recent user-rounds kept verbatim (§9 K)
	minFoldMsgs    = 4   // don't bother folding fewer messages
	summaryMinLen  = 50  // shorter LLM summaries degrade to statistical
	evictKeepChars = 120 // prefix kept when a tool result is evicted
)

// summaryPreamble marks the folded-context message for the model (§9: 系统生成
// 的上下文摘要，仅背景，不是新的用户请求).
const summaryPreamble = "[Earlier-conversation summary — background context only, NOT a new user request]\n"

// loadedMsg is one persisted message plus fold bookkeeping.
type loadedMsg struct {
	rec     storage.AgentMessage
	content msgContent
	pinned  bool // task plan call/result — never folded
}

// loadLogical returns the session's non-compacted messages in LOGICAL order:
// the anchor (first user message), then summaries (oldest first), then the
// remaining messages in seq order.
func (e *Engine) loadLogical(ctx context.Context, sessID string) ([]loadedMsg, error) {
	stored, err := e.store.ListAgentMessages(ctx, sessID)
	if err != nil {
		return nil, fmt.Errorf("agent: load history: %w", err)
	}
	var anchor []loadedMsg
	var summaries []loadedMsg
	var rest []loadedMsg
	planCalls := map[string]bool{} // call IDs of submit_plan → pin their results
	for _, m := range stored {
		if m.Compacted {
			continue
		}
		var c msgContent
		if err := json.Unmarshal([]byte(m.Content), &c); err != nil {
			return nil, fmt.Errorf("agent: decode message %s: %w", m.ID, err)
		}
		lm := loadedMsg{rec: m, content: c}
		for _, tc := range c.ToolCalls {
			if tc.Name == "submit_plan" {
				lm.pinned = true
				planCalls[tc.ID] = true
			}
		}
		if c.Result != nil && planCalls[c.Result.CallID] {
			lm.pinned = true
		}
		// @table mentions pin their message (§10.3) — structure rendering is
		// LRU-capped in toLLMMessages, the pin itself persists.
		if m.Role == "user" && len(c.Extra) > 0 {
			lm.pinned = true
		}
		switch {
		case m.Role == "user" && len(anchor) == 0 && len(rest) == 0:
			anchor = append(anchor, lm)
		case m.Role == "summary":
			summaries = append(summaries, lm)
		default:
			rest = append(rest, lm)
		}
	}
	out := make([]loadedMsg, 0, len(anchor)+len(summaries)+len(rest))
	out = append(out, anchor...)
	out = append(out, summaries...)
	out = append(out, rest...)
	return out, nil
}

// maxLiveMentions caps how many @mention messages keep their full structures
// in the LLM view (§9 LRU): older ones degrade to the bare text — the pin
// survives, the bulk does not.
const maxLiveMentions = 8

// toLLMMessages converts logical messages to the provider sequence.
func toLLMMessages(msgs []loadedMsg) []llm.Message {
	// LRU over mention-bearing messages: only the newest maxLiveMentions render
	// their structures.
	live := map[int]bool{}
	seen := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].rec.Role == "user" && len(msgs[i].content.Extra) > 0 {
			if seen < maxLiveMentions {
				live[i] = true
			}
			seen++
		}
	}
	var out []llm.Message
	for i, m := range msgs {
		c := m.content
		if m.rec.Role == "user" && len(c.Extra) > 0 && live[i] {
			var mm msgMentions
			if err := json.Unmarshal(c.Extra, &mm); err == nil && len(mm.Tables) > 0 {
				var b strings.Builder
				b.WriteString(c.Text)
				b.WriteString("\n\n[Referenced table structures — these tables are explicitly designated by the user; column/table comments are business-meaning aliases]\n")
				for _, tb := range mm.Tables {
					fmt.Fprintf(&b, "table %s: %s\n", tb.Name, tb.Structure)
				}
				if mm.Truncated {
					b.WriteString("[Structure list incomplete: the user mentioned more tables than included here — do not guess about the missing ones, verify them with tools or ask.]\n")
				}
				out = append(out, llm.Message{Role: llm.RoleUser, Text: b.String()})
				continue
			}
		}
		switch m.rec.Role {
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
		case "summary":
			out = append(out, llm.Message{Role: llm.RoleUser, Text: summaryPreamble + c.Text})
		default:
			out = append(out, llm.Message{Role: llm.RoleUser, Text: c.Text})
		}
	}
	return out
}

// estimateTokens is the chars/4 heuristic used between provider usage reports.
func estimateTokens(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Text)
		if m.ToolResult != nil {
			n += len(m.ToolResult.Content)
		}
		for _, c := range m.ToolCalls {
			n += len(c.Args) + len(c.Name)
		}
	}
	return n / 4
}

// evictOldToolResults is compaction level 1: outside the last keepRounds
// user-rounds, big tool results are replaced by a one-line stub. Applied to
// the request copy only — nothing persisted, fully reversible next turn.
func evictOldToolResults(msgs []llm.Message, keepRounds int) []llm.Message {
	// Find the index where the protected tail starts: the keepRounds-th user
	// message from the end.
	tailStart := 0
	seen := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser && !strings.HasPrefix(msgs[i].Text, summaryPreamble) {
			seen++
			if seen >= keepRounds {
				tailStart = i
				break
			}
		}
	}
	out := make([]llm.Message, len(msgs))
	copy(out, msgs)
	for i := 0; i < tailStart; i++ {
		if r := out[i].ToolResult; r != nil && len(r.Content) > evictKeepChars*2 {
			stub := *r
			stub.Content = "[tool result evicted to save context: " +
				truncate(r.Content, evictKeepChars) + "]"
			out[i].ToolResult = &stub
		}
	}
	return out
}

// chooseFoldRange picks the logical index range [1, end) to fold: after the
// anchor, before the protected tail, cut aligned so tool results stay with
// their assistant call, stopping at the first pinned message.
func chooseFoldRange(msgs []loadedMsg) (end int) {
	if len(msgs) < 2 {
		return 0
	}
	// Protected tail: last keepTailRounds user messages.
	tailStart := len(msgs)
	seen := 0
	for i := len(msgs) - 1; i >= 1; i-- {
		if msgs[i].rec.Role == "user" {
			seen++
			if seen >= keepTailRounds {
				tailStart = i
				break
			}
		}
	}
	if tailStart == len(msgs) {
		// Fewer than keepTailRounds rounds total — nothing old enough to fold.
		return 0
	}
	end = tailStart
	// Never split an assistant tool-call from its results: if the boundary
	// message is a tool result, pull the cut back past its assistant call.
	for end > 1 && msgs[end].rec.Role == "tool" {
		end--
		for end > 1 && msgs[end].rec.Role == "tool" {
			end--
		}
		// end now sits on the assistant that issued the calls — exclude it too.
	}
	// Pinned messages are folded AROUND, not up to (a pinned plan or @mention
	// early in the history must not block compaction forever) — count only
	// foldable messages against the minimum.
	foldable := 0
	for i := 1; i < end; i++ {
		if !msgs[i].pinned {
			foldable++
		}
	}
	if foldable < minFoldMsgs {
		return 0
	}
	return end
}

// compactSession runs level-2 folding. Returns the folded message count (0 =
// nothing to fold). emitFn may be an emitter-bound function (in-turn, seq'd)
// or e.emit (manual compaction outside a turn).
func (e *Engine) compactSession(ctx context.Context, sessID string, provider llm.Provider, model string, emitFn func(string, map[string]any)) (int, error) {
	msgs, err := e.loadLogical(ctx, sessID)
	if err != nil {
		return 0, err
	}
	before := estimateTokens(toLLMMessages(msgs))
	end := chooseFoldRange(msgs)
	if end == 0 {
		return 0, nil
	}
	// Fold around pinned messages — they keep their place in logical order.
	var folded []loadedMsg
	for _, m := range msgs[1:end] {
		if !m.pinned {
			folded = append(folded, m)
		}
	}

	summary := e.summarize(ctx, sessID, provider, model, folded)
	if _, err := e.store.AppendAgentMessage(ctx, storage.AgentMessage{
		SessionID: sessID, Role: "summary", Content: mustContent(msgContent{Text: summary}),
	}); err != nil {
		return 0, fmt.Errorf("agent: persist summary: %w", err)
	}
	ids := make([]string, len(folded))
	for i, m := range folded {
		ids[i] = m.rec.ID
	}
	if err := e.store.MarkMessagesCompactedByID(ctx, sessID, ids); err != nil {
		return 0, err
	}

	after := before
	if again, err := e.loadLogical(ctx, sessID); err == nil {
		after = estimateTokens(toLLMMessages(again))
	}
	emitFn("agent:compacted", map[string]any{
		"sessId": sessID, "foldedCount": len(folded), "before": before, "after": after,
	})
	e.trace.Rec(sessID, "compact", map[string]any{
		"foldedCount": len(folded), "before": before, "after": after, "summary": summary,
	})
	return len(folded), nil
}

// summarize asks the session's model for a compact summary of the folded
// rounds; any failure or degenerate output degrades to a statistical summary
// — compaction must never abort because summarization did (§9).
func (e *Engine) summarize(ctx context.Context, sessID string, provider llm.Provider, model string, folded []loadedMsg) string {
	transcript := renderTranscript(folded)
	req := llm.ChatRequest{
		Model: model,
		System: "Summarize this database-assistant conversation excerpt for context compaction. " +
			"Keep: confirmed schema facts (tables/columns/keys), decisions made, results obtained, " +
			"and anything the user asked to remember. Be dense and factual; no preamble.",
		Messages:  []llm.Message{{Role: llm.RoleUser, Text: transcript}},
		MaxTokens: 1024,
	}
	e.trace.Rec(sessID, "request", map[string]any{"purpose": "compact-summary", "req": req})
	stream, err := provider.ChatStream(ctx, req)
	if err != nil {
		return statSummary(folded)
	}
	defer stream.Close()
	var b strings.Builder
	for {
		ev, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return statSummary(folded)
		}
		if td, ok := ev.(llm.TextDelta); ok {
			b.WriteString(td.Text)
		}
	}
	s := strings.TrimSpace(b.String())
	if len(s) < summaryMinLen {
		return statSummary(folded)
	}
	return s
}

// renderTranscript flattens folded messages into plain text for summarization.
func renderTranscript(msgs []loadedMsg) string {
	var b strings.Builder
	for _, m := range msgs {
		c := m.content
		switch m.rec.Role {
		case "tool":
			if c.Result != nil {
				fmt.Fprintf(&b, "tool result: %s\n", truncate(c.Result.Content, 400))
			}
		case "summary":
			fmt.Fprintf(&b, "earlier summary: %s\n", c.Text)
		default:
			if c.Text != "" {
				fmt.Fprintf(&b, "%s: %s\n", m.rec.Role, truncate(c.Text, 1000))
			}
			for _, tc := range c.ToolCalls {
				fmt.Fprintf(&b, "assistant called %s(%s)\n", tc.Name, truncate(string(tc.Args), 200))
			}
		}
	}
	return b.String()
}

// statSummary is the degraded, non-LLM fallback (§9): counts and touched tools.
func statSummary(msgs []loadedMsg) string {
	roles := map[string]int{}
	tools := map[string]bool{}
	for _, m := range msgs {
		roles[m.rec.Role]++
		for _, tc := range m.content.ToolCalls {
			tools[tc.Name] = true
		}
	}
	names := make([]string, 0, len(tools))
	for n := range tools {
		names = append(names, n)
	}
	return fmt.Sprintf("(auto-generated fallback) %d earlier messages were folded: %d user, %d assistant, %d tool results; tools used: %s.",
		len(msgs), roles["user"], roles["assistant"], roles["tool"], strings.Join(names, ", "))
}

// --- triggers ----------------------------------------------------------------

// compactThreshold reads agent.compact.threshold (default 0.7).
func (e *Engine) compactThreshold(ctx context.Context) float64 {
	if v := e.setting(ctx, "agent.compact.threshold"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f < 1 {
			return f
		}
	}
	return 0.7
}

// isContextOverflow best-effort matches provider context-length errors for
// passive compaction (§9). Status-code matching lives in the adapters; text
// match is the cross-provider fallback.
func isContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	if !strings.Contains(s, "context") && !strings.Contains(s, "token") {
		return false
	}
	for _, kw := range []string{"length", "exceed", "too long", "maximum", "window", "overflow"} {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// ManualCompact is the session-toolbar "compact now" action (§9).
func (e *Engine) ManualCompact(ctx context.Context, sessID string) error {
	sess, err := e.store.GetAgentSession(ctx, sessID)
	if err != nil {
		return err
	}
	provider, err := e.resolve(ctx, sess.ProviderID)
	if err != nil {
		return err
	}
	_, err = e.compactSession(ctx, sessID, provider, sess.Model, func(name string, data map[string]any) {
		e.emit(name, data)
	})
	return err
}
