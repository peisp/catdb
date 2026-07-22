package services

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"

	"catdb/internal/llm"
	"catdb/internal/llmconfig"
	"catdb/internal/storage"
	"catdb/wailsbridge"
)

// AgentSettingsService exposes AI Agent Provider configuration to the settings
// window: Provider CRUD + keyring-backed API keys + default provider/model +
// a connectivity test. It is a thin binding — all persistence logic lives in
// internal/llmconfig; keys live in keyring (never SQLite), per CLAUDE.md #8.
type AgentSettingsService struct {
	store   *storage.Store
	secrets *storage.Secrets
}

// NewAgentSettingsService wires the SQLite config store and the keyring.
func NewAgentSettingsService(store *storage.Store, secrets *storage.Secrets) *AgentSettingsService {
	return &AgentSettingsService{store: store, secrets: secrets}
}

func (s *AgentSettingsService) ServiceName() string { return "AgentSettingsService" }

// AgentDefaults is the persisted default Provider instance + model.
type AgentDefaults struct {
	ProviderID string `json:"providerId"`
	Model      string `json:"model"`
}

// ListProviders returns all configured Provider instances (never any key).
func (s *AgentSettingsService) ListProviders(ctx context.Context) ([]llmconfig.ProviderConfig, error) {
	return llmconfig.Load(ctx, s.store)
}

// SaveProvider upserts a Provider instance. An empty ID means "create" and gets
// a fresh UUID; the saved config (with its final ID) is returned. Never touches
// the keyring — use SetProviderKey for the API key.
func (s *AgentSettingsService) SaveProvider(ctx context.Context, p llmconfig.ProviderConfig) (llmconfig.ProviderConfig, error) {
	providers, err := llmconfig.Load(ctx, s.store)
	if err != nil {
		return llmconfig.ProviderConfig{}, err
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
		providers = append(providers, p)
	} else {
		found := false
		for i := range providers {
			if providers[i].ID == p.ID {
				providers[i] = p
				found = true
				break
			}
		}
		if !found {
			providers = append(providers, p)
		}
	}
	if err := llmconfig.Save(ctx, s.store, providers); err != nil {
		return llmconfig.ProviderConfig{}, err
	}
	wailsbridge.Emit("agent:providers-changed", nil)
	return p, nil
}

// DeleteProvider removes a Provider instance, its keyring key, and clears the
// default provider/model if they pointed at it.
func (s *AgentSettingsService) DeleteProvider(ctx context.Context, id string) error {
	providers, err := llmconfig.Load(ctx, s.store)
	if err != nil {
		return err
	}
	out := make([]llmconfig.ProviderConfig, 0, len(providers))
	for _, p := range providers {
		if p.ID != id {
			out = append(out, p)
		}
	}
	if err := llmconfig.Save(ctx, s.store, out); err != nil {
		return err
	}
	if err := s.secrets.Delete(llmconfig.SecretID(id)); err != nil {
		return err
	}
	// Drop the default pointer if it referenced the deleted provider.
	if pid, _, err := llmconfig.GetDefaults(ctx, s.store); err == nil && pid == id {
		if err := llmconfig.SetDefaults(ctx, s.store, "", ""); err != nil {
			return err
		}
	}
	wailsbridge.Emit("agent:providers-changed", nil)
	return nil
}

// SetProviderKey stores (write-only) the API key for a Provider in the keyring.
// An empty key is rejected — use DeleteProvider to clear a key with its config.
func (s *AgentSettingsService) SetProviderKey(ctx context.Context, id, key string) error {
	if id == "" {
		return fmt.Errorf("agent: provider id is required")
	}
	if key == "" {
		return fmt.Errorf("agent: api key is required")
	}
	return s.secrets.Save(llmconfig.SecretID(id), storage.Secret{Password: key})
}

// HasProviderKey reports whether a non-empty API key is stored for the Provider,
// without ever revealing it — the settings page shows a "configured" state only.
func (s *AgentSettingsService) HasProviderKey(ctx context.Context, id string) (bool, error) {
	sec, err := s.secrets.Load(llmconfig.SecretID(id))
	if err != nil {
		if errors.Is(err, storage.ErrSecretNotFound) {
			return false, nil
		}
		return false, err
	}
	return sec.Password != "", nil
}

// FetchModelsRequest is the input to FetchProviderModels. Key is whatever the
// form currently has typed in, not yet saved; when empty and ID is set it
// falls back to the key already stored in keyring.
type FetchModelsRequest struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	BaseURL string `json:"baseURL"`
	Key     string `json:"key"`
}

// fetchModelsTimeout bounds the online "fetch models" request (settings page
// button) so a hung provider endpoint doesn't stall the UI indefinitely.
const fetchModelsTimeout = 30 * time.Second

// FetchProviderModels queries the provider's live model list using draft
// (possibly unsaved) config — the settings page "fetch from API" button. It
// never touches Provider.Models(), which agent loop relies on to stay the
// static, user-configured list.
func (s *AgentSettingsService) FetchProviderModels(ctx context.Context, req FetchModelsRequest) ([]llm.ModelInfo, error) {
	key := req.Key
	if key == "" && req.ID != "" {
		sec, err := s.secrets.Load(llmconfig.SecretID(req.ID))
		if err != nil && !errors.Is(err, storage.ErrSecretNotFound) {
			return nil, err
		}
		key = sec.Password
	}
	provider, err := llm.New(llm.Config{Type: req.Type, BaseURL: req.BaseURL, APIKey: key})
	if err != nil {
		return nil, err
	}
	lister, ok := provider.(llm.ModelLister)
	if !ok {
		return nil, fmt.Errorf("agent: provider type %q does not support listing models", req.Type)
	}
	ctx, cancel := context.WithTimeout(ctx, fetchModelsTimeout)
	defer cancel()
	return lister.ListModels(ctx)
}

// GetDefaults returns the default Provider instance + model.
func (s *AgentSettingsService) GetDefaults(ctx context.Context) (AgentDefaults, error) {
	pid, model, err := llmconfig.GetDefaults(ctx, s.store)
	if err != nil {
		return AgentDefaults{}, err
	}
	return AgentDefaults{ProviderID: pid, Model: model}, nil
}

// SetDefaults persists the default Provider instance + model.
func (s *AgentSettingsService) SetDefaults(ctx context.Context, providerID, model string) error {
	return llmconfig.SetDefaults(ctx, s.store, providerID, model)
}

// TestProvider probes connectivity by resolving the Provider and firing one
// minimal MaxTokens=1 "ping" stream. Success = the stream established and read
// without error. Technical errors are returned verbatim (English, per §14).
func (s *AgentSettingsService) TestProvider(ctx context.Context, id, model string) error {
	provider, err := llmconfig.Resolve(ctx, s.store, s.secrets, id)
	if err != nil {
		return err
	}
	stream, err := provider.ChatStream(ctx, llm.ChatRequest{
		Model:     model,
		Messages:  []llm.Message{{Role: llm.RoleUser, Text: "ping"}},
		MaxTokens: 1,
	})
	if err != nil {
		return err
	}
	defer stream.Close()
	// Drain to end so a mid-stream error surfaces; MaxTokens=1 keeps it tiny.
	for {
		if _, err := stream.Next(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// --- Agent runtime settings (privacy / limits / compaction / pricing) ---

// GetAgentSettings returns the Agent runtime settings, unset keys falling back
// to their defaults (AGENT_DESIGN.md §12).
func (s *AgentSettingsService) GetAgentSettings(ctx context.Context) (llmconfig.AgentSettings, error) {
	return llmconfig.LoadSettings(ctx, s.store)
}

// SetAgentSettings persists all Agent runtime settings at once (privacy switch,
// limits, compaction, per-model pricing table).
func (s *AgentSettingsService) SetAgentSettings(ctx context.Context, settings llmconfig.AgentSettings) error {
	return llmconfig.SaveSettings(ctx, s.store, settings)
}

// --- Audit (settings page "审计" section) ---

// AuditQuery scopes ListAudit / ExportAudit. SinceUnix/UntilUnix are
// epoch-seconds bounds (0 = unbounded). Offset+Limit paginate the (most-recent-
// first) result; storage itself has no OFFSET, so the service over-fetches and
// slices — audit is small local data, so this stays cheap.
type AuditQuery struct {
	ConnID    string `json:"connId"`
	SessionID string `json:"sessionId"`
	SinceUnix int64  `json:"sinceUnix"`
	UntilUnix int64  `json:"untilUnix"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}

// AuditPage is one page of audit entries plus whether more exist after it.
type AuditPage struct {
	Entries []storage.AgentAuditEntry `json:"entries"`
	HasMore bool                      `json:"hasMore"`
}

func (q AuditQuery) storageFilter(fetchLimit int) storage.AgentAuditFilter {
	f := storage.AgentAuditFilter{
		ConnID:    q.ConnID,
		SessionID: q.SessionID,
		Limit:     fetchLimit,
	}
	if q.SinceUnix > 0 {
		f.Since = time.Unix(q.SinceUnix, 0)
	}
	if q.UntilUnix > 0 {
		f.Until = time.Unix(q.UntilUnix, 0)
	}
	return f
}

// ListAudit returns one page of audit entries, most recent first.
func (s *AgentSettingsService) ListAudit(ctx context.Context, q AuditQuery) (AuditPage, error) {
	if q.Offset < 0 {
		q.Offset = 0
	}
	// Over-fetch by one past the page so HasMore is known; Limit<=0 means all.
	fetchLimit := 0
	if q.Limit > 0 {
		fetchLimit = q.Offset + q.Limit + 1
	}
	all, err := s.store.ListAgentAudit(ctx, q.storageFilter(fetchLimit))
	if err != nil {
		return AuditPage{}, err
	}
	if q.Offset >= len(all) {
		return AuditPage{Entries: []storage.AgentAuditEntry{}, HasMore: false}, nil
	}
	rest := all[q.Offset:]
	hasMore := false
	if q.Limit > 0 && len(rest) > q.Limit {
		rest = rest[:q.Limit]
		hasMore = true
	}
	return AuditPage{Entries: rest, HasMore: hasMore}, nil
}

// ClearAudit deletes audit entries created strictly before beforeUnixSec.
func (s *AgentSettingsService) ClearAudit(ctx context.Context, beforeUnixSec int64) error {
	return s.store.ClearAgentAudit(ctx, beforeUnixSec)
}

// AuditExportResult is the synchronous return of ExportAudit.
type AuditExportResult struct {
	Path string `json:"path"`
	Rows int    `json:"rows"`
}

// ExportAudit writes every audit entry matching the filter (no pagination) to
// path as JSON or CSV. Rows are written straight to disk (never crossing IPC as
// a bulk payload, 铁律 5); the front-end picks path via system.pickSaveFile.
// format is "json" or "csv".
func (s *AgentSettingsService) ExportAudit(ctx context.Context, q AuditQuery, format, path string) (AuditExportResult, error) {
	if path == "" {
		return AuditExportResult{}, fmt.Errorf("agent: export path is required")
	}
	q.Offset = 0
	q.Limit = 0
	entries, err := s.store.ListAgentAudit(ctx, q.storageFilter(0))
	if err != nil {
		return AuditExportResult{}, err
	}

	f, err := os.Create(path)
	if err != nil {
		return AuditExportResult{}, fmt.Errorf("agent: create %s: %w", path, err)
	}
	defer f.Close()

	switch format {
	case "csv":
		if err := writeAuditCSV(f, entries); err != nil {
			return AuditExportResult{}, err
		}
	default: // json
		if err := writeAuditJSON(f, entries); err != nil {
			return AuditExportResult{}, err
		}
	}
	if err := f.Close(); err != nil {
		return AuditExportResult{}, err
	}
	return AuditExportResult{Path: path, Rows: len(entries)}, nil
}

func writeAuditJSON(f *os.File, entries []storage.AgentAuditEntry) error {
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	// One JSON object per line (JSON Lines) — streams row by row, no giant
	// in-memory array marshal.
	for i := range entries {
		if err := enc.Encode(entries[i]); err != nil {
			return err
		}
	}
	return nil
}

func writeAuditCSV(f *os.File, entries []storage.AgentAuditEntry) error {
	w := csv.NewWriter(f)
	header := []string{"id", "sessionId", "connId", "sql", "class", "approval", "rows", "durationMs", "status", "error", "createdAt"}
	if err := w.Write(header); err != nil {
		return err
	}
	for _, e := range entries {
		rows, dur := "", ""
		if e.Rows != nil {
			rows = strconv.FormatInt(*e.Rows, 10)
		}
		if e.DurationMS != nil {
			dur = strconv.FormatInt(*e.DurationMS, 10)
		}
		rec := []string{
			e.ID, e.SessionID, e.ConnID, e.SQL, e.Class, e.Approval,
			rows, dur, e.Status, e.Error, e.CreatedAt.UTC().Format(time.RFC3339),
		}
		if err := w.Write(rec); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
