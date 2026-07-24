package openaicompat

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

	toolIndex map[int]string // tool_calls delta 的 index → id
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
		_, data, err := s.sc.Next()
		if err != nil {
			return nil, err
		}
		if string(data) == "[DONE]" {
			s.done = true
			continue
		}
		if len(data) == 0 {
			continue
		}
		if err := s.handle(data); err != nil {
			return nil, err
		}
	}
}

type chunk struct {
	Choices []struct {
		Delta struct {
			Content          string          `json:"content"`
			ReasoningContent string          `json:"reasoning_content"`
			ToolCalls        []deltaToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

type deltaToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (s *stream) handle(data []byte) error {
	var c chunk
	if err := json.Unmarshal(data, &c); err != nil {
		return fmt.Errorf("openaicompat: decode chunk: %w", err)
	}
	if c.Error != nil {
		return fmt.Errorf("openaicompat: stream error: %s: %s", c.Error.Type, c.Error.Message)
	}
	for _, ch := range c.Choices {
		if ch.Delta.ReasoningContent != "" {
			s.pending = append(s.pending, llm.ThinkingDelta{Text: ch.Delta.ReasoningContent})
		}
		if ch.Delta.Content != "" {
			s.pending = append(s.pending, llm.TextDelta{Text: ch.Delta.Content})
		}
		for _, tc := range ch.Delta.ToolCalls {
			id, seen := s.toolIndex[tc.Index]
			if !seen {
				id = tc.ID
				s.toolIndex[tc.Index] = id
				s.pending = append(s.pending, llm.ToolCallStart{ID: id, Name: tc.Function.Name})
			}
			if tc.Function.Arguments != "" {
				s.pending = append(s.pending, llm.ToolCallDelta{ID: id, ArgsFragment: tc.Function.Arguments})
			}
		}
		if ch.FinishReason != nil {
			s.pending = append(s.pending, llm.Stop{Reason: mapFinishReason(*ch.FinishReason)})
		}
	}
	if c.Usage != nil {
		s.pending = append(s.pending, llm.Usage{
			InputTokens:     c.Usage.PromptTokens,
			OutputTokens:    c.Usage.CompletionTokens,
			CacheReadTokens: c.Usage.PromptTokensDetails.CachedTokens,
		})
	}
	return nil
}

func mapFinishReason(r string) llm.StopReason {
	switch r {
	case "tool_calls":
		return llm.StopToolUse
	case "length":
		return llm.StopMaxTokens
	default: // stop / 其他
		return llm.StopEndTurn
	}
}
