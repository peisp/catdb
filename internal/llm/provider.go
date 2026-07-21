// Package llm is catdb 内置 AI Agent 的 LLM Provider 抽象层。
//
// 它把「消息 + 工具调用 + 流式」收敛成一组统一类型：供应商差异（system 位置、
// 工具调用格式、SSE 分帧、role 约束、prompt caching）全部收在各 adapter 内，
// agent 层只见统一的 Event 流。本包不感知数据库与 UI，也不碰 keyring/storage——
// API Key 由调用方经 Config 传入。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// Provider 是一个已配置好的 LLM 供应商实例。
type Provider interface {
	// Name 返回供应商类型标识，如 "anthropic" | "openai-compat"。
	Name() string
	// Models 可列则列；不可列时返回构造时配置的内置清单。
	Models(ctx context.Context) ([]ModelInfo, error)
	// ChatStream 发起一次流式补全。ctx 取消即中断流。
	ChatStream(ctx context.Context, req ChatRequest) (Stream, error)
}

// ModelInfo 描述一个模型。ContextWindow 供上下文水位计算，SupportsTools 决定
// 是否降级为纯文本补全——二者对 openai-compat 下的自定义模型无法探测，由配置提供。
type ModelInfo struct {
	ID            string
	ContextWindow int
	SupportsTools bool
}

// Role 是消息角色。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 是一轮对话中的一条消息，可承载纯文本、assistant 的工具调用、或工具结果。
//
//   - user：Text 为用户输入。
//   - assistant：Text 为正文（可空），ToolCalls 为发起的工具调用。
//   - tool：ToolResult 为某次工具调用的结果。
type Message struct {
	Role       Role
	Text       string
	ToolCalls  []ToolCall
	ToolResult *ToolResult
}

// ToolCall 是 assistant 发起的一次工具调用。Args 是参数 JSON（可为空，视作 {}）。
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// ToolResult 是一次工具调用的返回。CallID 对应发起调用的 ToolCall.ID。
type ToolResult struct {
	CallID  string
	Content string
	IsError bool
}

// ToolDef 是暴露给模型的工具声明。InputSchema 是参数的 JSON Schema。
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ChatRequest 是一次补全请求。System 为顶层系统提示词，Temperature 为空表示不指定。
type ChatRequest struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolDef
	MaxTokens   int
	Temperature *float64
}

// Stream 是拉模式事件流：Next 阻塞到下一事件，io.EOF 表示流结束，ctx 取消即中断。
type Stream interface {
	Next() (Event, error)
	Close() error
}

// Event 是流事件变体，用 type switch 消费。
type Event interface{ isEvent() }

// TextDelta 是正文增量。
type TextDelta struct{ Text string }

// ThinkingDelta 是思考过程增量（Anthropic extended thinking / DeepSeek
// reasoning_content；不支持的模型无此事件）。
type ThinkingDelta struct{ Text string }

// ToolCallStart 标记一次工具调用开始。
type ToolCallStart struct {
	ID   string
	Name string
}

// ToolCallDelta 是某次工具调用参数 JSON 的增量片段。
type ToolCallDelta struct {
	ID           string
	ArgsFragment string
}

// Usage 是用量，含缓存命中细分。
type Usage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
}

// Stop 标记本轮结束及其原因。
type Stop struct{ Reason StopReason }

// StopReason 是统一的停止原因枚举。
type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopToolUse   StopReason = "tool_use"
	StopMaxTokens StopReason = "max_tokens"
)

func (TextDelta) isEvent()     {}
func (ThinkingDelta) isEvent() {}
func (ToolCallStart) isEvent() {}
func (ToolCallDelta) isEvent() {}
func (Usage) isEvent()         {}
func (Stop) isEvent()          {}

// Config 是构造 Provider 的配置。Type 为 "anthropic" | "openai-compat"。
// APIKey 由调用方传入，本包不碰 keyring/storage。Models 是内置模型清单。
type Config struct {
	Type    string
	BaseURL string
	APIKey  string
	Models  []ModelInfo
}

// Factory 从 Config 构造一个 Provider。
type Factory func(Config) (Provider, error)

var registry = map[string]Factory{}

// Register 登记一个 provider 类型的工厂。各 adapter 在自己的 init() 里调用；
// 调用方按需匿名导入 adapter 包即可让 New 支持该类型（对齐驱动注册心智）。
func Register(typ string, f Factory) { registry[typ] = f }

// New 按 cfg.Type 构造 Provider。未注册的类型返回错误。
func New(cfg Config) (Provider, error) {
	f, ok := registry[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("llm: unknown provider type %q", cfg.Type)
	}
	return f(cfg)
}
