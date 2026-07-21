package llmtest

import (
	"context"
	"errors"
	"io"
	"testing"

	"catdb/internal/llm"
)

func drain(t *testing.T, st llm.Stream) []llm.Event {
	t.Helper()
	var out []llm.Event
	for {
		ev, err := st.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, ev)
	}
}

func TestReplayPerCall(t *testing.T) {
	round1 := []llm.Event{llm.TextDelta{Text: "a"}, llm.Stop{Reason: llm.StopToolUse}}
	round2 := []llm.Event{llm.TextDelta{Text: "b"}, llm.Stop{Reason: llm.StopEndTurn}}
	p := New("fake", round1, round2)

	ctx := context.Background()
	st1, err := p.ChatStream(ctx, llm.ChatRequest{Model: "m", System: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := drain(t, st1); len(got) != 2 {
		t.Fatalf("round1 events = %d", len(got))
	}

	st2, err := p.ChatStream(ctx, llm.ChatRequest{Model: "m", System: "s2"})
	if err != nil {
		t.Fatal(err)
	}
	got := drain(t, st2)
	if len(got) != 2 || got[0].(llm.TextDelta).Text != "b" {
		t.Fatalf("round2 events wrong: %#v", got)
	}

	// 记录了两次请求
	if len(p.Requests) != 2 || p.Requests[0].System != "s1" || p.Requests[1].System != "s2" {
		t.Fatalf("requests not recorded: %#v", p.Requests)
	}
}

func TestExhaustedScripts(t *testing.T) {
	p := New("fake", []llm.Event{llm.Stop{Reason: llm.StopEndTurn}})
	if _, err := p.ChatStream(context.Background(), llm.ChatRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ChatStream(context.Background(), llm.ChatRequest{}); err == nil {
		t.Fatal("expected error when scripts exhausted")
	}
}

func TestCtxCancel(t *testing.T) {
	p := New("fake", []llm.Event{llm.TextDelta{Text: "x"}})
	ctx, cancel := context.WithCancel(context.Background())
	st, _ := p.ChatStream(ctx, llm.ChatRequest{})
	cancel()
	if _, err := st.Next(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Next after cancel: err = %v, want context.Canceled", err)
	}
}

func TestModels(t *testing.T) {
	p := New("fake")
	p.SetModels(llm.ModelInfo{ID: "m1", ContextWindow: 1000, SupportsTools: true})
	ms, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].ID != "m1" {
		t.Fatalf("models = %#v", ms)
	}
}
