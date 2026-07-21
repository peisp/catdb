// Package openaicompat 是 OpenAI Chat Completions 兼容 API 的 llm.Provider adapter，
// 用于对接 DeepSeek / Qwen / Kimi / Ollama / vLLM 等（配自定义 base_url）。
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"catdb/internal/llm"
)

func init() {
	llm.Register("openai-compat", func(cfg llm.Config) (llm.Provider, error) { return New(cfg) })
}

// Provider 对接 OpenAI 兼容的 /chat/completions。
type Provider struct {
	baseURL string
	apiKey  string
	models  []llm.ModelInfo
	client  *http.Client
}

// New 构造一个 openai-compat Provider。BaseURL 必填（对接第三方服务的通道）。
func New(cfg llm.Config) (*Provider, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("openaicompat: BaseURL is required")
	}
	return &Provider{
		baseURL: cfg.BaseURL,
		apiKey:  cfg.APIKey,
		models:  cfg.Models,
		client:  http.DefaultClient,
	}, nil
}

func (p *Provider) Name() string { return "openai-compat" }

// Models 返回构造时配置的清单（本 adapter 不做在线列举）。
func (p *Provider) Models(ctx context.Context) ([]llm.ModelInfo, error) {
	return p.models, nil
}

// ChatStream 发起流式 chat completions 请求。
func (p *Provider) ChatStream(ctx context.Context, req llm.ChatRequest) (llm.Stream, error) {
	raw, err := json.Marshal(buildBody(req))
	if err != nil {
		return nil, fmt.Errorf("openaicompat: marshal request: %w", err)
	}
	buildReq := func() (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		r.Header.Set("content-type", "application/json")
		if p.apiKey != "" {
			r.Header.Set("Authorization", "Bearer "+p.apiKey)
		}
		return r, nil
	}
	resp, err := llm.PostStream(ctx, p.client, buildReq)
	if err != nil {
		return nil, err
	}
	return newStream(resp), nil
}

// ---- 请求体构造 ----

type reqBody struct {
	Model         string      `json:"model"`
	Stream        bool        `json:"stream"`
	StreamOptions *streamOpts `json:"stream_options,omitempty"`
	Messages      []message   `json:"messages"`
	Tools         []tool      `json:"tools,omitempty"`
	MaxTokens     int         `json:"max_tokens,omitempty"`
	Temperature   *float64    `json:"temperature,omitempty"`
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function funcCall `json:"function"`
}

type funcCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type tool struct {
	Type     string  `json:"type"`
	Function funcDef `json:"function"`
}

type funcDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func buildBody(req llm.ChatRequest) reqBody {
	b := reqBody{
		Model:         req.Model,
		Stream:        true,
		StreamOptions: &streamOpts{IncludeUsage: true},
		Messages:      toMessages(req),
		MaxTokens:     req.MaxTokens,
		Temperature:   req.Temperature,
	}
	for _, t := range req.Tools {
		b.Tools = append(b.Tools, tool{
			Type:     "function",
			Function: funcDef{Name: t.Name, Description: t.Description, Parameters: t.InputSchema},
		})
	}
	return b
}

func toMessages(req llm.ChatRequest) []message {
	var out []message
	if req.System != "" {
		out = append(out, message{Role: "system", Content: strPtr(req.System)})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case llm.RoleAssistant:
			msg := message{Role: "assistant"}
			if m.Text != "" {
				msg.Content = strPtr(m.Text)
			}
			for _, tc := range m.ToolCalls {
				args := string(tc.Args)
				if args == "" {
					args = "{}"
				}
				msg.ToolCalls = append(msg.ToolCalls, toolCall{
					ID:       tc.ID,
					Type:     "function",
					Function: funcCall{Name: tc.Name, Arguments: args},
				})
			}
			out = append(out, msg)
		case llm.RoleTool:
			if m.ToolResult == nil {
				continue
			}
			out = append(out, message{
				Role:       "tool",
				ToolCallID: m.ToolResult.CallID,
				Content:    strPtr(m.ToolResult.Content),
			})
		default: // user
			out = append(out, message{Role: "user", Content: strPtr(m.Text)})
		}
	}
	return out
}

func strPtr(s string) *string { return &s }
