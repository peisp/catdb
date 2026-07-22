package openaicompat

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
	body := `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":4}}}

data: [DONE]

`
	srv := sseServer(t, body)
	st, _ := newProvider(t, srv.URL).ChatStream(context.Background(), llm.ChatRequest{Model: "m", MaxTokens: 100})
	defer st.Close()
	got := collect(t, st)
	want := []llm.Event{
		llm.TextDelta{Text: "Hel"},
		llm.TextDelta{Text: "lo"},
		llm.Stop{Reason: llm.StopEndTurn},
		llm.Usage{InputTokens: 11, OutputTokens: 2, CacheReadTokens: 4},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events mismatch:\n got=%#v\nwant=%#v", got, want)
	}
}

func TestStreamToolCall(t *testing.T) {
	body := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"lo"}}]},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"c\":\"SF\"}"}}]},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":6}}

data: [DONE]

`
	srv := sseServer(t, body)
	st, _ := newProvider(t, srv.URL).ChatStream(context.Background(), llm.ChatRequest{Model: "m", MaxTokens: 100})
	defer st.Close()
	got := collect(t, st)
	want := []llm.Event{
		llm.ToolCallStart{ID: "call_1", Name: "get_weather"},
		llm.ToolCallDelta{ID: "call_1", ArgsFragment: `{"lo`},
		llm.ToolCallDelta{ID: "call_1", ArgsFragment: `c":"SF"}`},
		llm.Stop{Reason: llm.StopToolUse},
		llm.Usage{InputTokens: 9, OutputTokens: 6},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events mismatch:\n got=%#v\nwant=%#v", got, want)
	}
	var frag string
	for _, ev := range got {
		if d, ok := ev.(llm.ToolCallDelta); ok {
			frag += d.ArgsFragment
		}
	}
	if frag != `{"loc":"SF"}` {
		t.Fatalf("aggregated args = %q", frag)
	}
}

func TestStreamThinking(t *testing.T) {
	body := `data: {"choices":[{"index":0,"delta":{"reasoning_content":"hmm"},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"reasoning_content":"..."},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`
	srv := sseServer(t, body)
	st, _ := newProvider(t, srv.URL).ChatStream(context.Background(), llm.ChatRequest{Model: "m", MaxTokens: 100})
	defer st.Close()
	got := collect(t, st)
	want := []llm.Event{
		llm.ThinkingDelta{Text: "hmm"},
		llm.ThinkingDelta{Text: "..."},
		llm.TextDelta{Text: "done"},
		llm.Stop{Reason: llm.StopEndTurn},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events mismatch:\n got=%#v\nwant=%#v", got, want)
	}
}

func TestMapFinishReason(t *testing.T) {
	cases := map[string]llm.StopReason{
		"stop":       llm.StopEndTurn,
		"tool_calls": llm.StopToolUse,
		"length":     llm.StopMaxTokens,
		"":           llm.StopEndTurn,
	}
	for in, want := range cases {
		if got := mapFinishReason(in); got != want {
			t.Errorf("mapFinishReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCancelInterruptsNext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fl := w.(http.Flusher)
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n")
		fl.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	st, err := newProvider(t, srv.URL).ChatStream(ctx, llm.ChatRequest{Model: "m", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.Next(); err != nil {
		t.Fatalf("first Next: %v", err)
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
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	st, err := newProvider(t, srv.URL).ChatStream(context.Background(), llm.ChatRequest{Model: "m", MaxTokens: 100})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	defer st.Close()
	if got := collect(t, st); len(got) == 0 {
		t.Fatal("no events after retry")
	}
	if n != 2 {
		t.Fatalf("server hit %d times, want 2", n)
	}
}

func TestRetryExhausted(t *testing.T) {
	llm.RetryBaseDelay = time.Millisecond
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := newProvider(t, srv.URL).ChatStream(context.Background(), llm.ChatRequest{Model: "m", MaxTokens: 100}); err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if n != 3 {
		t.Fatalf("server hit %d times, want 3", n)
	}
}

func TestListModels(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"object":"list","data":[
			{"id":"gpt-4o"},
			{"id":"or-model","context_length":32000},
			{"id":"groq-model","context_window":8192},
			{"id":"mistral-model","max_context_length":32768,"capabilities":{"function_calling":false}},
			{"id":"vllm-model","max_model_len":4096},
			{"id":"or-tools-model","context_length":16000,"supported_parameters":["temperature","tools"]},
			{"id":"or-no-tools-model","context_length":16000,"supported_parameters":["temperature"]}
		]}`)
	}))
	defer srv.Close()

	models, err := newProvider(t, srv.URL).ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	want := []llm.ModelInfo{
		{ID: "gpt-4o", ContextWindow: 0, SupportsTools: true},
		{ID: "or-model", ContextWindow: 32000, SupportsTools: true},
		{ID: "groq-model", ContextWindow: 8192, SupportsTools: true},
		{ID: "mistral-model", ContextWindow: 32768, SupportsTools: false},
		{ID: "vllm-model", ContextWindow: 4096, SupportsTools: true},
		{ID: "or-tools-model", ContextWindow: 16000, SupportsTools: true},
		{ID: "or-no-tools-model", ContextWindow: 16000, SupportsTools: false},
	}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
	if gotAuth != "Bearer k" {
		t.Errorf("Authorization header = %q", gotAuth)
	}
}

func TestListModelsNoKey(t *testing.T) {
	var gotAuth string
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, sawAuth = r.Header.Get("Authorization"), r.Header.Get("Authorization") != ""
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"object":"list","data":[]}`)
	}))
	defer srv.Close()

	p, err := New(llm.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ListModels(context.Background()); err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if sawAuth {
		t.Errorf("Authorization header should be absent when key empty, got %q", gotAuth)
	}
}

func TestListModelsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "boom")
	}))
	defer srv.Close()

	_, err := newProvider(t, srv.URL).ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error on non-2xx response")
	}
}

func TestBaseURLRequired(t *testing.T) {
	if _, err := New(llm.Config{}); err == nil {
		t.Fatal("expected error when BaseURL empty")
	}
}

func TestBuildBody(t *testing.T) {
	req := llm.ChatRequest{
		Model:     "deepseek",
		System:    "sys",
		MaxTokens: 128,
		Tools: []llm.ToolDef{
			{Name: "a", Description: "da", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Text: "hi"},
			{Role: llm.RoleAssistant, Text: "sure", ToolCalls: []llm.ToolCall{{ID: "t1", Name: "a", Args: json.RawMessage(`{"x":1}`)}}},
			{Role: llm.RoleTool, ToolResult: &llm.ToolResult{CallID: "t1", Content: "42"}},
		},
	}
	b := buildBody(req)

	if !b.Stream || b.StreamOptions == nil || !b.StreamOptions.IncludeUsage {
		t.Errorf("stream_options.include_usage not set: %#v", b.StreamOptions)
	}
	if b.MaxTokens != 128 {
		t.Errorf("max_tokens = %d", b.MaxTokens)
	}
	// system 作为首条 role:system 消息
	if b.Messages[0].Role != "system" || b.Messages[0].Content == nil || *b.Messages[0].Content != "sys" {
		t.Fatalf("first message not system: %#v", b.Messages[0])
	}
	if b.Messages[1].Role != "user" {
		t.Fatalf("want user, got %s", b.Messages[1].Role)
	}
	// assistant tool_calls 格式
	asst := b.Messages[2]
	if asst.Role != "assistant" || len(asst.ToolCalls) != 1 {
		t.Fatalf("assistant tool_calls wrong: %#v", asst)
	}
	tc := asst.ToolCalls[0]
	if tc.ID != "t1" || tc.Type != "function" || tc.Function.Name != "a" || tc.Function.Arguments != `{"x":1}` {
		t.Fatalf("tool_call wrong: %#v", tc)
	}
	// tool 结果 → role:tool + tool_call_id
	tr := b.Messages[3]
	if tr.Role != "tool" || tr.ToolCallID != "t1" || tr.Content == nil || *tr.Content != "42" {
		t.Fatalf("tool result message wrong: %#v", tr)
	}
	// tools → function schema
	if len(b.Tools) != 1 || b.Tools[0].Type != "function" || b.Tools[0].Function.Name != "a" {
		t.Fatalf("tools wrong: %#v", b.Tools)
	}
}
