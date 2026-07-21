package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"catdb/internal/llm"
)

// sseServer 返回一个把 body 作为 SSE 一次性写完的测试服务器。
func sseServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func collect(t *testing.T, st llm.Stream) []llm.Event {
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

func newProvider(t *testing.T, url string) *Provider {
	t.Helper()
	p, err := New(llm.Config{BaseURL: url, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStreamText(t *testing.T) {
	body := `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":5,"cache_creation_input_tokens":2}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}

event: message_stop
data: {"type":"message_stop"}

`
	srv := sseServer(t, body)
	st, err := newProvider(t, srv.URL).ChatStream(context.Background(), llm.ChatRequest{Model: "m", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got := collect(t, st)
	want := []llm.Event{
		llm.TextDelta{Text: "Hello"},
		llm.TextDelta{Text: " world"},
		llm.Usage{InputTokens: 10, OutputTokens: 7, CacheReadTokens: 5, CacheWriteTokens: 2},
		llm.Stop{Reason: llm.StopEndTurn},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events mismatch:\n got=%#v\nwant=%#v", got, want)
	}
}

func TestStreamToolCall(t *testing.T) {
	body := `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":8}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"loc"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"ation\":\"SF\"}"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}

event: message_stop
data: {"type":"message_stop"}

`
	srv := sseServer(t, body)
	st, _ := newProvider(t, srv.URL).ChatStream(context.Background(), llm.ChatRequest{Model: "m", MaxTokens: 100})
	defer st.Close()
	got := collect(t, st)
	want := []llm.Event{
		llm.ToolCallStart{ID: "toolu_1", Name: "get_weather"},
		llm.ToolCallDelta{ID: "toolu_1", ArgsFragment: `{"loc`},
		llm.ToolCallDelta{ID: "toolu_1", ArgsFragment: `ation":"SF"}`},
		llm.Usage{InputTokens: 8, OutputTokens: 12},
		llm.Stop{Reason: llm.StopToolUse},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events mismatch:\n got=%#v\nwant=%#v", got, want)
	}
	// 分片聚合应还原完整参数。
	var frag string
	for _, ev := range got {
		if d, ok := ev.(llm.ToolCallDelta); ok {
			frag += d.ArgsFragment
		}
	}
	if frag != `{"location":"SF"}` {
		t.Fatalf("aggregated args = %q", frag)
	}
}

func TestStreamThinking(t *testing.T) {
	body := `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":3}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" think"}}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}

event: message_stop
data: {"type":"message_stop"}

`
	srv := sseServer(t, body)
	st, _ := newProvider(t, srv.URL).ChatStream(context.Background(), llm.ChatRequest{Model: "m", MaxTokens: 100})
	defer st.Close()
	got := collect(t, st)
	want := []llm.Event{
		llm.ThinkingDelta{Text: "let me"},
		llm.ThinkingDelta{Text: " think"},
		llm.TextDelta{Text: "answer"},
		llm.Usage{InputTokens: 3, OutputTokens: 4},
		llm.Stop{Reason: llm.StopEndTurn},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events mismatch:\n got=%#v\nwant=%#v", got, want)
	}
}

func TestMapStopReason(t *testing.T) {
	cases := map[string]llm.StopReason{
		"end_turn":      llm.StopEndTurn,
		"tool_use":      llm.StopToolUse,
		"max_tokens":    llm.StopMaxTokens,
		"stop_sequence": llm.StopEndTurn,
		"":              llm.StopEndTurn,
	}
	for in, want := range cases {
		if got := mapStopReason(in); got != want {
			t.Errorf("mapStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCancelInterruptsNext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fl := w.(http.Flusher)
		io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		fl.Flush()
		<-r.Context().Done() // 挂住直到客户端取消
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	st, err := newProvider(t, srv.URL).ChatStream(ctx, llm.ChatRequest{Model: "m", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ev, err := st.Next() // 应先拿到一个 TextDelta
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if _, ok := ev.(llm.TextDelta); !ok {
		t.Fatalf("first event = %#v", ev)
	}
	cancel()
	if _, err := st.Next(); err == nil || err == io.EOF {
		t.Fatalf("Next after cancel: err = %v, want non-nil non-EOF", err)
	}
}

func TestRetryThenSuccess(t *testing.T) {
	llm.RetryBaseDelay = time.Millisecond
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	st, err := newProvider(t, srv.URL).ChatStream(context.Background(), llm.ChatRequest{Model: "m", MaxTokens: 100})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	defer st.Close()
	got := collect(t, st)
	if n != 2 {
		t.Fatalf("server hit %d times, want 2 (one retry)", n)
	}
	if len(got) == 0 {
		t.Fatal("no events after retry")
	}
}

func TestRetryExhausted(t *testing.T) {
	llm.RetryBaseDelay = time.Millisecond
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := newProvider(t, srv.URL).ChatStream(context.Background(), llm.ChatRequest{Model: "m", MaxTokens: 100})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if n != 3 {
		t.Fatalf("server hit %d times, want 3 attempts", n)
	}
}

func TestBuildBody(t *testing.T) {
	temp := 0.5
	req := llm.ChatRequest{
		Model:       "claude",
		System:      "sys",
		MaxTokens:   200,
		Temperature: &temp,
		Tools: []llm.ToolDef{
			{Name: "a", Description: "da", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "b", Description: "db", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Text: "hi"},
			{Role: llm.RoleAssistant, Text: "sure", ToolCalls: []llm.ToolCall{{ID: "t1", Name: "a", Args: json.RawMessage(`{"x":1}`)}}},
			{Role: llm.RoleTool, ToolResult: &llm.ToolResult{CallID: "t1", Content: "42", IsError: false}},
			{Role: llm.RoleTool, ToolResult: &llm.ToolResult{CallID: "t2", Content: "bad", IsError: true}},
		},
	}
	b := buildBody(req)

	if !b.Stream {
		t.Error("stream should be true")
	}
	if b.Temperature == nil || *b.Temperature != 0.5 {
		t.Error("temperature not passed through")
	}
	// system 顶层字段 + cache_control
	if len(b.System) != 1 || b.System[0].Text != "sys" || b.System[0].CacheControl == nil {
		t.Errorf("system block wrong: %#v", b.System)
	}
	// 仅最后一个 tool 打 cache_control
	if b.Tools[0].CacheControl != nil {
		t.Error("first tool should not have cache_control")
	}
	if b.Tools[1].CacheControl == nil {
		t.Error("last tool should have cache_control")
	}
	// 两条 tool 结果并入同一 user 消息
	if len(b.Messages) != 3 {
		t.Fatalf("want 3 anthropic messages (user/assistant/user), got %d: %#v", len(b.Messages), b.Messages)
	}
	if b.Messages[0].Role != "user" || b.Messages[1].Role != "assistant" || b.Messages[2].Role != "user" {
		t.Fatalf("roles wrong: %v %v %v", b.Messages[0].Role, b.Messages[1].Role, b.Messages[2].Role)
	}
	// assistant 的 tool 调用 → tool_use block
	asst := b.Messages[1]
	if len(asst.Content) != 2 || asst.Content[0].Type != "text" || asst.Content[1].Type != "tool_use" || asst.Content[1].ID != "t1" {
		t.Fatalf("assistant content wrong: %#v", asst.Content)
	}
	// tool 结果 → user 消息内 tool_result block
	last := b.Messages[2]
	if len(last.Content) != 2 {
		t.Fatalf("want 2 tool_result blocks merged, got %d", len(last.Content))
	}
	if last.Content[0].Type != "tool_result" || last.Content[0].ToolUseID != "t1" || last.Content[0].Content != "42" {
		t.Errorf("tool_result[0] wrong: %#v", last.Content[0])
	}
	if !last.Content[1].IsError {
		t.Error("tool_result[1] should be is_error")
	}
	// 历史最后一块打 cache_control
	if last.Content[len(last.Content)-1].CacheControl == nil {
		t.Error("last content block should have cache_control")
	}
}
