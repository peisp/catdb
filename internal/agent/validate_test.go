package agent

import (
	"context"
	"strings"
	"testing"

	"catdb/internal/llm"
	"catdb/internal/llm/llmtest"
	"catdb/internal/storage"
)

func TestValidateDelivery(t *testing.T) {
	cases := []struct {
		name   string
		mode   string
		text   string
		ranSQL bool
		ok     bool
	}{
		{"ask fenced sql", "ask", "查询如下：\n```sql\nSELECT * FROM t WHERE id=1\n```", false, true},
		{"ask prose only", "ask", "orders 表有 3 列，主键是 id。", false, true},
		{"ask clarification", "ask", "需要更多信息：请确认是哪张表？", false, true},
		{"ask bare sql", "ask", "你可以执行：\nSELECT * FROM orders WHERE id = 1", false, false},
		{"ask bare sql lowercase", "ask", "select name from users where age > 18", false, false},
		{"ask sql inside generic fence ok", "ask", "```\nSELECT * FROM t WHERE id=1\n```", false, true},
		{"ask empty", "ask", "   ", false, false},
		{"agent sql without execution", "agent", "执行这个即可：\n```sql\nDELETE FROM t WHERE id=1\n```", false, false},
		{"agent sql after execution", "agent", "已执行：\n```sql\nDELETE FROM t WHERE id=1\n```\n影响 1 行。", true, true},
		{"agent prose answer", "agent", "共 42 条记录。", false, true},
		{"agent empty", "agent", "", false, false},
	}
	for _, c := range cases {
		v := validateDelivery(c.mode, c.text, c.ranSQL)
		if v.OK != c.ok {
			t.Errorf("%s: OK=%v want %v (missing=%q)", c.name, v.OK, c.ok, v.Missing)
		}
	}
}

func TestDeliveryRepairRetry(t *testing.T) {
	// Round 1: bare SQL outside a fence (ask mode) → repair message → round 2
	// delivers properly fenced SQL.
	p := llmtest.New("fake",
		[]llm.Event{
			llm.TextDelta{Text: "可以这样查：\nSELECT name FROM users WHERE id = 1"},
			llm.Stop{Reason: llm.StopEndTurn},
		},
		[]llm.Event{
			llm.TextDelta{Text: "```sql\nSELECT name FROM users WHERE id = 1\n```"},
			llm.Stop{Reason: llm.StopEndTurn},
		},
	)
	e, store, log := newTestEngine(t, p)
	sess := newTestSession(t, store)

	if err := e.Send(context.Background(), sess.ID, "查名字", nil); err != nil {
		t.Fatal(err)
	}
	if len(p.Requests) != 2 {
		t.Fatalf("want a repair round, got %d requests", len(p.Requests))
	}
	// The repair message reached the model as a system-generated user turn.
	req2 := p.Requests[1]
	last := req2.Messages[len(req2.Messages)-1]
	if !strings.Contains(last.Text, "delivery check") {
		t.Fatalf("repair message missing: %q", last.Text)
	}
	// Final done has no delivery warning.
	for _, ev := range log.events {
		if ev.Name == "agent:done" && ev.Data["deliveryWarning"] == true {
			t.Fatal("valid final answer must not carry deliveryWarning")
		}
	}
}

func TestDeliveryWarningAfterRepairCap(t *testing.T) {
	bad := []llm.Event{
		llm.TextDelta{Text: "SELECT x FROM t WHERE 1=1 直接执行"},
		llm.Stop{Reason: llm.StopEndTurn},
	}
	p := llmtest.New("fake", bad, bad, bad, bad)
	e, store, log := newTestEngine(t, p)
	sess := newTestSession(t, store)
	if err := e.Send(context.Background(), sess.ID, "查 x", nil); err != nil {
		t.Fatal(err)
	}
	// 1 original + 2 repairs, then deliver with warning — never discard.
	if len(p.Requests) != 3 {
		t.Fatalf("want 3 rounds (cap 2 repairs), got %d", len(p.Requests))
	}
	var warned bool
	for _, ev := range log.events {
		if ev.Name == "agent:done" && ev.Data["deliveryWarning"] == true {
			warned = true
		}
	}
	if !warned {
		t.Fatal("done must carry deliveryWarning after repair cap")
	}
}

func TestPrivacyOffWithholdsRowsFromModel(t *testing.T) {
	p := llmtest.New("fake",
		toolRound("run_sql", `{"db":"shop","sql":"SELECT id, name FROM users"}`),
		endRound("共 2 行。"),
	)
	e, store, sessID, _, log := newAgentEngine(t, p, "dev", []string{"select"}, nil)
	if err := store.SetSetting(context.Background(), "agent.privacy.sendRowData", "false"); err != nil {
		t.Fatal(err)
	}
	if err := e.Send(context.Background(), sessID, "查用户", nil); err != nil {
		t.Fatal(err)
	}
	// Model view: shape only, no cell values.
	r := lastToolResult(t, store, sessID)
	if r.IsError || strings.Contains(r.Content, `"a"`) || !strings.Contains(r.Content, "rowCount") {
		t.Fatalf("model must not see row data with privacy off: %+v", r)
	}
	// User path still gets the real rows.
	var gotRows bool
	for _, ev := range log.events {
		if ev.Name == "agent:result" {
			if rows, ok := ev.Data["rows"].([][]any); ok && len(rows) == 2 {
				gotRows = true
			}
		}
	}
	if !gotRows {
		t.Fatal("user path must still receive rows")
	}
}

func TestToollessModelDegradation(t *testing.T) {
	p := llmtest.New("fake", []llm.Event{
		llm.TextDelta{Text: "orders 表见上（含注释）。"},
		llm.Stop{Reason: llm.StopEndTurn},
	})
	p.SetModels(llm.ModelInfo{ID: "m1", ContextWindow: 8000, SupportsTools: false})
	e, store, _ := newTestEngine(t, p)
	sess := newTestSession(t, store)
	// Give the session a current DB so the overview includes tables.
	if err := store.UpdateAgentSessionMeta(context.Background(), sess.ID, storage.AgentSessionMeta{
		Mode: "ask", Grants: sess.Grants, ProviderID: sess.ProviderID, Model: sess.Model, CurrentDB: "shop",
	}); err != nil {
		t.Fatal(err)
	}

	if err := e.Send(context.Background(), sess.ID, "orders 是什么", nil); err != nil {
		t.Fatal(err)
	}
	req := p.Requests[0]
	if len(req.Tools) != 0 {
		t.Fatalf("tool-less model must get no tools, got %d", len(req.Tools))
	}
	if !strings.Contains(req.System, "Schema overview") || !strings.Contains(req.System, "orders") {
		t.Fatalf("schema overview missing from system prompt:\n%s", req.System)
	}
}

func TestToollessAgentModeRejected(t *testing.T) {
	p := llmtest.New("fake")
	p.SetModels(llm.ModelInfo{ID: "m1", SupportsTools: false})
	e, store, sessID, _, _ := newAgentEngine(t, p, "dev", []string{"select"}, nil)
	_ = store
	err := e.Send(context.Background(), sessID, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "agent.model-no-tools") {
		t.Fatalf("want model-no-tools error, got %v", err)
	}
}
