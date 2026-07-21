// Package llmtest 提供脚本化的假 llm.Provider，供 loop 引擎单测回放事件序列。
package llmtest

import (
	"context"
	"fmt"
	"io"

	"catdb/internal/llm"
)

// Provider 是脚本化假 Provider：按 ChatStream 调用次数回放对应轮次的事件序列。
type Provider struct {
	name    string
	scripts [][]llm.Event
	models  []llm.ModelInfo

	// Requests 记录每次 ChatStream 收到的请求，供断言。
	Requests []llm.ChatRequest
	calls    int
}

// New 构造一个假 Provider。每个 script 是一轮的完整事件序列（第 i 次调用回放第 i 个）。
func New(name string, scripts ...[]llm.Event) *Provider {
	return &Provider{name: name, scripts: scripts}
}

// SetModels 设置 Models 返回的清单。
func (p *Provider) SetModels(models ...llm.ModelInfo) { p.models = models }

func (p *Provider) Name() string { return p.name }

func (p *Provider) Models(ctx context.Context) ([]llm.ModelInfo, error) { return p.models, nil }

// ChatStream 记录请求并回放对应轮次；超出脚本数量返回错误。
func (p *Provider) ChatStream(ctx context.Context, req llm.ChatRequest) (llm.Stream, error) {
	i := p.calls
	p.calls++
	p.Requests = append(p.Requests, req)
	if i >= len(p.scripts) {
		return nil, fmt.Errorf("llmtest: no script for call #%d", i)
	}
	return &scriptStream{ctx: ctx, events: p.scripts[i]}, nil
}

type scriptStream struct {
	ctx    context.Context
	events []llm.Event
	i      int
}

func (s *scriptStream) Next() (llm.Event, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if s.i >= len(s.events) {
		return nil, io.EOF
	}
	ev := s.events[s.i]
	s.i++
	return ev, nil
}

func (s *scriptStream) Close() error { return nil }
