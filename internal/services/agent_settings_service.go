package services

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	"catdb/internal/llm"
	"catdb/internal/llmconfig"
	"catdb/internal/storage"
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
