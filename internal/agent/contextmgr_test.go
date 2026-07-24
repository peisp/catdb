package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"catdb/internal/llm"
	"catdb/internal/llm/llmtest"
	"catdb/internal/storage"
)

// seedRounds writes n user/assistant(+tool) rounds into a session.
func seedRounds(t *testing.T, store *storage.Store, sessID string, n int, withTools bool) {
	t.Helper()
	ctx := context.Background()
	add := func(role string, c msgContent) {
		t.Helper()
		if _, err := store.AppendAgentMessage(ctx, storage.AgentMessage{
			SessionID: sessID, Role: role, Content: mustContent(c),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		add("user", msgContent{Text: fmt.Sprintf("question %d", i)})
		if withTools {
			add("assistant", msgContent{ToolCalls: []storedCall{{ID: fmt.Sprintf("c%d", i), Name: "list_tables"}}})
			add("tool", msgContent{Result: &storedResult{CallID: fmt.Sprintf("c%d", i), Content: strings.Repeat("x", 600)}})
		}
		add("assistant", msgContent{Text: fmt.Sprintf("answer %d", i)})
	}
}

func TestChooseFoldRangeInvariants(t *testing.T) {
	p := llmtest.New("fake")
	_, store, _ := func() (*Engine, *storage.Store, *eventLog) { e, s, l := newTestEngine(t, p); return e, s, l }()
	sess := newTestSession(t, store)
	seedRounds(t, store, sess.ID, 8, true)

	e, _, _ := newTestEngine(t, p)
	e.store = store
	msgs, err := e.loadLogical(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	end := chooseFoldRange(msgs)
	if end == 0 {
		t.Fatal("8 rounds must be foldable")
	}
	// Anchor survives.
	if msgs[0].rec.Role != "user" || !strings.Contains(msgs[0].content.Text, "question 0") {
		t.Fatalf("anchor = %+v", msgs[0])
	}
	// Boundary never lands between an assistant call and its tool result.
	if msgs[end].rec.Role == "tool" {
		t.Fatalf("fold boundary splits a tool pair: %+v", msgs[end].rec)
	}
	// Last 5 user rounds stay outside the fold.
	users := 0
	for i := end; i < len(msgs); i++ {
		if msgs[i].rec.Role == "user" {
			users++
		}
	}
	if users < keepTailRounds {
		t.Fatalf("tail keeps %d rounds, want >= %d", users, keepTailRounds)
	}
}

func TestFoldStopsAtPinnedPlan(t *testing.T) {
	p := llmtest.New("fake")
	_, store, _ := newTestEngine(t, p)
	sess := newTestSession(t, store)
	ctx := context.Background()
	add := func(role string, c msgContent) {
		if _, err := store.AppendAgentMessage(ctx, storage.AgentMessage{SessionID: sess.ID, Role: role, Content: mustContent(c)}); err != nil {
			t.Fatal(err)
		}
	}
	add("user", msgContent{Text: "task"})
	add("assistant", msgContent{Text: "a1"})
	add("assistant", msgContent{ToolCalls: []storedCall{{ID: "p1", Name: "submit_plan"}}})
	add("tool", msgContent{Result: &storedResult{CallID: "p1", Content: "plan approved"}})
	seedRounds(t, store, sess.ID, 7, false)

	e, _, _ := newTestEngine(t, p)
	e.store = store
	n, err := e.compactSession(ctx, sess.ID, p, "m1", func(string, map[string]any) {})
	if err != nil || n == 0 {
		t.Fatalf("compact: n=%d err=%v", n, err)
	}
	// Folding happened AROUND the plan: the plan call/result rows must still be
	// live (non-compacted) after the fold.
	all, _ := store.ListAgentMessages(ctx, sess.ID)
	var planLive, planSeen bool
	for _, m := range all {
		var c msgContent
		_ = json.Unmarshal([]byte(m.Content), &c)
		for _, tc := range c.ToolCalls {
			if tc.Name == "submit_plan" {
				planSeen = true
				planLive = !m.Compacted
			}
		}
	}
	if !planSeen || !planLive {
		t.Fatalf("pinned plan must survive folding (seen=%v live=%v)", planSeen, planLive)
	}
}

func TestEvictOldToolResults(t *testing.T) {
	big := strings.Repeat("y", 1000)
	msgs := []llm.Message{
		{Role: llm.RoleUser, Text: "q1"},
		{Role: llm.RoleTool, ToolResult: &llm.ToolResult{CallID: "a", Content: big}},
	}
	// 6 recent rounds keep the tail protected.
	for i := 0; i < 6; i++ {
		msgs = append(msgs,
			llm.Message{Role: llm.RoleUser, Text: fmt.Sprintf("q%d", i+2)},
			llm.Message{Role: llm.RoleTool, ToolResult: &llm.ToolResult{CallID: fmt.Sprintf("t%d", i), Content: big}},
		)
	}
	out := evictOldToolResults(msgs, keepTailRounds)
	if !strings.Contains(out[1].ToolResult.Content, "evicted") {
		t.Fatal("old tool result must be evicted")
	}
	if strings.Contains(out[len(out)-1].ToolResult.Content, "evicted") {
		t.Fatal("recent tool result must stay intact")
	}
	// Originals untouched (request copy only).
	if msgs[1].ToolResult.Content != big {
		t.Fatal("eviction mutated the original slice")
	}
}

func TestCompactSessionWithLLMSummary(t *testing.T) {
	longSummary := strings.Repeat("关键事实。", 30)
	p := llmtest.New("fake", []llm.Event{
		llm.TextDelta{Text: longSummary},
		llm.Stop{Reason: llm.StopEndTurn},
	})
	e, store, log := newTestEngine(t, p)
	sess := newTestSession(t, store)
	seedRounds(t, store, sess.ID, 9, true)

	n, err := e.compactSession(context.Background(), sess.ID, p, "m1",
		func(name string, data map[string]any) { e.emit(name, data) })
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected folding")
	}
	if log.count("agent:compacted") != 1 {
		t.Fatal("agent:compacted not emitted")
	}
	// Rebuilt context: anchor first, then the summary, no compacted leftovers.
	rebuilt, err := e.loadHistory(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt[0].Role != llm.RoleUser || !strings.Contains(rebuilt[0].Text, "question 0") {
		t.Fatalf("anchor lost: %+v", rebuilt[0])
	}
	if !strings.Contains(rebuilt[1].Text, summaryPreamble[:20]) || !strings.Contains(rebuilt[1].Text, "关键事实") {
		t.Fatalf("summary not injected second: %q", rebuilt[1].Text)
	}
	// Chat panel history still shows everything (compaction is LLM-facing only).
	all, _ := store.ListAgentMessages(context.Background(), sess.ID)
	if len(all) < 9*3 {
		t.Fatalf("persisted history shrank: %d", len(all))
	}
}

func TestSummaryFallsBackToStatistical(t *testing.T) {
	// Provider with no scripts errors on ChatStream → statistical fallback.
	p := llmtest.New("fake")
	e, store, _ := newTestEngine(t, p)
	sess := newTestSession(t, store)
	seedRounds(t, store, sess.ID, 9, true)

	n, err := e.compactSession(context.Background(), sess.ID, p, "m1",
		func(name string, data map[string]any) {})
	if err != nil || n == 0 {
		t.Fatalf("compact: n=%d err=%v", n, err)
	}
	rebuilt, _ := e.loadHistory(context.Background(), sess.ID)
	if !strings.Contains(rebuilt[1].Text, "fallback") || !strings.Contains(rebuilt[1].Text, "list_tables") {
		t.Fatalf("statistical fallback missing: %q", rebuilt[1].Text)
	}
}

func TestSecondFoldSwallowsFirstSummary(t *testing.T) {
	long := strings.Repeat("s", 200)
	p := llmtest.New("fake",
		[]llm.Event{llm.TextDelta{Text: "first " + long}, llm.Stop{Reason: llm.StopEndTurn}},
		[]llm.Event{llm.TextDelta{Text: "second " + long}, llm.Stop{Reason: llm.StopEndTurn}},
	)
	e, store, _ := newTestEngine(t, p)
	sess := newTestSession(t, store)
	seedRounds(t, store, sess.ID, 9, false)
	noop := func(string, map[string]any) {}

	if n, err := e.compactSession(context.Background(), sess.ID, p, "m1", noop); err != nil || n == 0 {
		t.Fatalf("first fold: %d %v", n, err)
	}
	seedRounds(t, store, sess.ID, 7, false)
	if n, err := e.compactSession(context.Background(), sess.ID, p, "m1", noop); err != nil || n == 0 {
		t.Fatalf("second fold: %d %v", n, err)
	}
	rebuilt, _ := e.loadHistory(context.Background(), sess.ID)
	// Exactly one live summary (the second), positioned after the anchor; the
	// first summary was folded into it.
	var summaries int
	for _, m := range rebuilt {
		if strings.HasPrefix(m.Text, summaryPreamble[:20]) {
			summaries++
		}
	}
	if summaries != 1 || !strings.Contains(rebuilt[1].Text, "second") {
		t.Fatalf("want single live summary 'second', got %d (msg1=%q)", summaries, rebuilt[1].Text)
	}
}

func TestIsContextOverflow(t *testing.T) {
	cases := map[string]bool{
		"maximum context length exceeded":         true,
		"prompt is too long: tokens > window":     true,
		"input tokens exceed the model's maximum": true,
		"connection refused":                      false,
		"rate limit exceeded":                     false,
		"invalid api key":                         false,
	}
	for msg, want := range cases {
		if got := isContextOverflow(errors.New(msg)); got != want {
			t.Errorf("isContextOverflow(%q) = %v, want %v", msg, got, want)
		}
	}
	if isContextOverflow(nil) {
		t.Error("nil must be false")
	}
}
