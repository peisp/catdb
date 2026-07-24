// Package agenttrace records the agent's complete model interaction — every
// ChatRequest verbatim (system prompt, message array, tool defs), every
// assembled response, tool execution, approval and compaction — as
// append-only JSONL, one file per session, for the dev-only Trace window.
//
// Dev builds only (see enabled_dev.go): traces carry full business data and
// must never exist in a production install. All Recorder methods are nil-safe
// no-ops when the subsystem is disabled or the trace dir cannot be resolved.
package agenttrace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"catdb/internal/storage"
)

// Record is one JSONL line. Kind is one of: user | request | response | tool |
// approval | plan | compact | done | error. Data is kind-specific.
type Record struct {
	Time time.Time       `json:"t"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// Recorder appends trace records. One per Engine; safe for concurrent use
// (parallel tool goroutines record too).
type Recorder struct {
	mu  sync.Mutex
	dir string
}

// NewRecorder resolves the trace directory (<app config dir>/agent-traces).
// Returns a disabled recorder when tracing is off or the dir is unavailable.
func NewRecorder() *Recorder {
	if !Enabled {
		return &Recorder{}
	}
	dir, err := traceDir()
	if err != nil {
		return &Recorder{}
	}
	return &Recorder{dir: dir}
}

// Rec appends one record to the session's trace file. Errors are swallowed —
// tracing must never affect the agent loop.
func (r *Recorder) Rec(sessID, kind string, data any) {
	if r == nil || r.dir == "" || sessID == "" {
		return
	}
	payload, err := json.Marshal(data)
	if err != nil {
		payload = []byte(fmt.Sprintf(`{"marshalError":%q}`, err.Error()))
	}
	line, err := json.Marshal(Record{Time: time.Now(), Kind: kind, Data: payload})
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(r.dir, sessID+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

// SessionInfo describes one trace file for the session list.
type SessionInfo struct {
	SessionID string    `json:"sessionId"`
	Size      int64     `json:"size"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ListSessions scans the trace dir, most recently updated first.
func ListSessions() ([]SessionInfo, error) {
	dir, err := traceDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("agenttrace: read dir: %w", err)
	}
	var out []SessionInfo
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, SessionInfo{
			SessionID: strings.TrimSuffix(name, ".jsonl"),
			Size:      info.Size(),
			UpdatedAt: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// ReadSession returns the session's raw JSONL trace. The front-end parses
// line by line (dev tooling — bulk IPC is acceptable here).
func ReadSession(sessID string) (string, error) {
	if err := checkID(sessID); err != nil {
		return "", err
	}
	dir, err := traceDir()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(dir, sessID+".jsonl"))
	if err != nil {
		return "", fmt.Errorf("agenttrace: read session: %w", err)
	}
	return string(b), nil
}

// Clear deletes every trace file.
func Clear() error {
	dir, err := traceDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("agenttrace: read dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

func traceDir() (string, error) {
	base, err := storage.AppConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "agent-traces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("agenttrace: create %s: %w", dir, err)
	}
	return dir, nil
}

// checkID rejects session IDs that could escape the trace dir (they are
// UUIDs in practice; this is a path-traversal guard for the IPC surface).
func checkID(sessID string) error {
	if sessID == "" || strings.ContainsAny(sessID, `/\`) || strings.Contains(sessID, "..") {
		return fmt.Errorf("agenttrace: invalid session id %q", sessID)
	}
	return nil
}
