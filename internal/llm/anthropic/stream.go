package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"catdb/internal/llm"
)

type stream struct {
	resp    *http.Response
	sc      *llm.SSEScanner
	pending []llm.Event
	done    bool

	toolIndex  map[int]string // content block index → tool_use id
	inInput    int            // message_start 的输入用量
	cacheRead  int
	cacheWrite int
}

func newStream(resp *http.Response) *stream {
	return &stream{
		resp:      resp,
		sc:        llm.NewSSEScanner(resp.Body),
		toolIndex: map[int]string{},
	}
}

func (s *stream) Close() error { return s.resp.Body.Close() }

func (s *stream) Next() (llm.Event, error) {
	for {
		if len(s.pending) > 0 {
			ev := s.pending[0]
			s.pending = s.pending[1:]
			return ev, nil
		}
		if s.done {
			return nil, io.EOF
		}
		name, data, err := s.sc.Next()
		if err != nil {
			return nil, err // io.EOF / ctx 取消 / 读错误
		}
		if err := s.handle(name, data); err != nil {
			return nil, err
		}
	}
}

// SSE data 载荷结构（只取用得到的字段）。
type ssePayload struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		Usage sseUsage `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage sseUsage `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type sseUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func (s *stream) handle(name string, data []byte) error {
	// event 名与 data.type 一致，优先按 event 名分派；ping 无 data。
	if name == "ping" {
		return nil
	}
	var p ssePayload
	if len(data) > 0 {
		if err := json.Unmarshal(data, &p); err != nil {
			return fmt.Errorf("anthropic: decode %s: %w", name, err)
		}
	}
	typ := name
	if typ == "" {
		typ = p.Type
	}
	switch typ {
	case "message_start":
		s.inInput = p.Message.Usage.InputTokens
		s.cacheRead = p.Message.Usage.CacheReadInputTokens
		s.cacheWrite = p.Message.Usage.CacheCreationInputTokens
	case "content_block_start":
		if p.ContentBlock.Type == "tool_use" {
			s.toolIndex[p.Index] = p.ContentBlock.ID
			s.pending = append(s.pending, llm.ToolCallStart{ID: p.ContentBlock.ID, Name: p.ContentBlock.Name})
		}
	case "content_block_delta":
		switch p.Delta.Type {
		case "text_delta":
			s.pending = append(s.pending, llm.TextDelta{Text: p.Delta.Text})
		case "thinking_delta":
			s.pending = append(s.pending, llm.ThinkingDelta{Text: p.Delta.Thinking})
		case "input_json_delta":
			s.pending = append(s.pending, llm.ToolCallDelta{ID: s.toolIndex[p.Index], ArgsFragment: p.Delta.PartialJSON})
		}
	case "message_delta":
		s.pending = append(s.pending, llm.Usage{
			InputTokens:      s.inInput,
			OutputTokens:     p.Usage.OutputTokens,
			CacheReadTokens:  s.cacheRead,
			CacheWriteTokens: s.cacheWrite,
		})
		s.pending = append(s.pending, llm.Stop{Reason: mapStopReason(p.Delta.StopReason)})
	case "message_stop":
		s.done = true
	case "error":
		return fmt.Errorf("anthropic: stream error: %s: %s", p.Error.Type, p.Error.Message)
	}
	return nil
}

func mapStopReason(r string) llm.StopReason {
	switch r {
	case "tool_use":
		return llm.StopToolUse
	case "max_tokens":
		return llm.StopMaxTokens
	default: // end_turn / stop_sequence / 其他
		return llm.StopEndTurn
	}
}
