// Package anthropic 是 Anthropic Messages API 的 llm.Provider adapter。
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"catdb/internal/llm"
)

const defaultBaseURL = "https://api.anthropic.com"
const apiVersion = "2023-06-01"

func init() {
	llm.Register("anthropic", func(cfg llm.Config) (llm.Provider, error) { return New(cfg) })
}

// Provider 对接 Anthropic Messages API。
type Provider struct {
	baseURL string
	apiKey  string
	models  []llm.ModelInfo
	client  *http.Client
}

// New 构造一个 Anthropic Provider。BaseURL 为空时用官方地址。
func New(cfg llm.Config) (*Provider, error) {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	return &Provider{
		baseURL: base,
		apiKey:  cfg.APIKey,
		models:  cfg.Models,
		client:  http.DefaultClient,
	}, nil
}

func (p *Provider) Name() string { return "anthropic" }

// Models 返回构造时配置的清单（本 adapter 不做在线列举）。
func (p *Provider) Models(ctx context.Context) ([]llm.ModelInfo, error) {
	return p.models, nil
}

// defaultContextWindow 是 Anthropic 模型的当前全系默认上下文窗口——
// /v1/models 不返回该字段，用户可在设置页表单里改。
const defaultContextWindow = 200000

// maxListModelsPages 是 ListModels 分页翻页的防御性上限。
const maxListModelsPages = 10

type listModelsEntry struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type listModelsResponse struct {
	Data    []listModelsEntry `json:"data"`
	HasMore bool              `json:"has_more"`
	LastID  string            `json:"last_id"`
}

// ListModels 请求 GET /v1/models，翻页取完全部模型。
func (p *Provider) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	var out []llm.ModelInfo
	afterID := ""
	for page := 0; page < maxListModelsPages; page++ {
		q := url.Values{"limit": {"1000"}}
		if afterID != "" {
			q.Set("after_id", afterID)
		}
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/v1/models?"+q.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("anthropic: list models: %w", err)
		}
		r.Header.Set("x-api-key", p.apiKey)
		r.Header.Set("anthropic-version", apiVersion)
		resp, err := p.client.Do(r)
		if err != nil {
			return nil, fmt.Errorf("anthropic: list models: %w", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
			resp.Body.Close()
			return nil, fmt.Errorf("anthropic: list models: HTTP %d: %s", resp.StatusCode, body)
		}
		var parsed listModelsResponse
		err = json.NewDecoder(resp.Body).Decode(&parsed)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("anthropic: list models: decode response: %w", err)
		}
		for _, m := range parsed.Data {
			out = append(out, llm.ModelInfo{ID: m.ID, ContextWindow: defaultContextWindow, SupportsTools: true})
		}
		if !parsed.HasMore || parsed.LastID == "" {
			break
		}
		afterID = parsed.LastID
	}
	return out, nil
}

// ChatStream 发起流式 Messages 请求。
func (p *Provider) ChatStream(ctx context.Context, req llm.ChatRequest) (llm.Stream, error) {
	raw, err := json.Marshal(buildBody(req))
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	buildReq := func() (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		r.Header.Set("content-type", "application/json")
		r.Header.Set("x-api-key", p.apiKey)
		r.Header.Set("anthropic-version", apiVersion)
		return r, nil
	}
	resp, err := llm.PostStream(ctx, p.client, buildReq)
	if err != nil {
		return nil, err
	}
	return newStream(resp), nil
}

// ---- 请求体构造 ----

type cacheControl struct {
	Type string `json:"type"`
}

var ephemeral = &cacheControl{Type: "ephemeral"}

type reqBody struct {
	Model       string     `json:"model"`
	MaxTokens   int        `json:"max_tokens"`
	Stream      bool       `json:"stream"`
	System      []sysBlock `json:"system,omitempty"`
	Messages    []message  `json:"messages"`
	Tools       []tool     `json:"tools,omitempty"`
	Temperature *float64   `json:"temperature,omitempty"`
}

type sysBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type tool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

type message struct {
	Role    string    `json:"role"`
	Content []content `json:"content"`
}

type content struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      string          `json:"content,omitempty"`
	IsError      bool            `json:"is_error,omitempty"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

func buildBody(req llm.ChatRequest) reqBody {
	b := reqBody{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Stream:      true,
		Messages:    toMessages(req.Messages),
		Temperature: req.Temperature,
	}
	if req.System != "" {
		// system 打 cache_control：agent loop 每轮全量重发，缓存是费用侧最大杠杆。
		b.System = []sysBlock{{Type: "text", Text: req.System, CacheControl: ephemeral}}
	}
	for i, t := range req.Tools {
		at := tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
		if len(at.InputSchema) == 0 {
			at.InputSchema = json.RawMessage(`{"type":"object"}`)
		}
		if i == len(req.Tools)-1 {
			at.CacheControl = ephemeral // 工具清单前缀缓存
		}
		b.Tools = append(b.Tools, at)
	}
	// 历史消息最后一块打 cache_control，缓存整个历史前缀。
	if n := len(b.Messages); n > 0 {
		last := &b.Messages[n-1]
		if m := len(last.Content); m > 0 {
			last.Content[m-1].CacheControl = ephemeral
		}
	}
	return b
}

// toMessages 把统一消息转成 Anthropic 消息，并按映射后的 role 合并相邻消息
// （tool 结果映射为 user role，多条工具结果须并入同一 user 消息内）。
func toMessages(msgs []llm.Message) []message {
	var out []message
	appendBlocks := func(role string, blocks []content) {
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, blocks...)
			return
		}
		out = append(out, message{Role: role, Content: blocks})
	}
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleAssistant:
			var blocks []content
			if m.Text != "" {
				blocks = append(blocks, content{Type: "text", Text: m.Text})
			}
			for _, tc := range m.ToolCalls {
				input := tc.Args
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, content{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
			}
			appendBlocks("assistant", blocks)
		case llm.RoleTool:
			if m.ToolResult == nil {
				continue
			}
			appendBlocks("user", []content{{
				Type:      "tool_result",
				ToolUseID: m.ToolResult.CallID,
				Content:   m.ToolResult.Content,
				IsError:   m.ToolResult.IsError,
			}})
		default: // user
			appendBlocks("user", []content{{Type: "text", Text: m.Text}})
		}
	}
	return out
}
