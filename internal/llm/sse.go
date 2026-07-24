package llm

import (
	"bufio"
	"io"
	"strings"
)

// SSEScanner 手解 Server-Sent Events：逐个返回事件的 event 名与 data 载荷。
// 兼容 Anthropic（event: + data:）与 OpenAI（仅 data:）两种分帧。
type SSEScanner struct {
	sc      *bufio.Scanner
	name    string
	data    []byte
	hasData bool
}

// NewSSEScanner 基于 r 建一个扫描器。用 bufio.Scanner 但放大缓冲到 1MB，
// 以容纳较长的 JSON data 行。
func NewSSEScanner(r io.Reader) *SSEScanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	return &SSEScanner{sc: sc}
}

// Next 返回下一个完整 SSE 事件。空 data 且无 event 名的分隔被跳过；流结束返回 io.EOF。
func (s *SSEScanner) Next() (name string, data []byte, err error) {
	s.name, s.data, s.hasData = "", nil, false
	for s.sc.Scan() {
		line := s.sc.Text()
		if line == "" {
			if s.hasData || s.name != "" {
				return s.name, s.data, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // 注释/心跳
		}
		if v, ok := strings.CutPrefix(line, "event:"); ok {
			s.name = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "data:"); ok {
			v = strings.TrimPrefix(v, " ")
			if s.hasData {
				s.data = append(s.data, '\n')
			}
			s.data = append(s.data, v...)
			s.hasData = true
		}
	}
	if err := s.sc.Err(); err != nil {
		return "", nil, err
	}
	if s.hasData || s.name != "" { // 末尾事件后无空行时的兜底
		return s.name, s.data, nil
	}
	return "", nil, io.EOF
}
