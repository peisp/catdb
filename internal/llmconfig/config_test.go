package llmconfig

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"catdb/internal/llm"
	"catdb/internal/storage"
)

func testStore(t *testing.T) *storage.Store {
	t.Helper()
	st, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestLoadSaveRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)

	// Empty store → empty slice, no error.
	got, err := Load(ctx, store)
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty providers, got %d", len(got))
	}

	want := []ProviderConfig{
		{
			ID:      "p1",
			Name:    "Claude",
			Type:    TypeAnthropic,
			BaseURL: "",
			Models: []llm.ModelInfo{
				{ID: "claude-sonnet", ContextWindow: 200000, SupportsTools: true},
			},
			DefaultModel: "claude-sonnet",
		},
		{
			ID:      "p2",
			Name:    "DeepSeek",
			Type:    TypeOpenAICompat,
			BaseURL: "https://api.deepseek.com",
			Models: []llm.ModelInfo{
				{ID: "deepseek-chat", ContextWindow: 64000, SupportsTools: true},
			},
			DefaultModel: "deepseek-chat",
		},
	}
	if err := Save(ctx, store, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err = Load(ctx, store)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestDefaultsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)

	pid, model, err := GetDefaults(ctx, store)
	if err != nil {
		t.Fatalf("GetDefaults empty: %v", err)
	}
	if pid != "" || model != "" {
		t.Fatalf("expected empty defaults, got %q/%q", pid, model)
	}
	if err := SetDefaults(ctx, store, "p1", "claude-sonnet"); err != nil {
		t.Fatalf("SetDefaults: %v", err)
	}
	pid, model, err = GetDefaults(ctx, store)
	if err != nil {
		t.Fatalf("GetDefaults: %v", err)
	}
	if pid != "p1" || model != "claude-sonnet" {
		t.Fatalf("defaults mismatch: %q/%q", pid, model)
	}
}

func TestResolveUnknownID(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)

	loadKey := func(string) (storage.Secret, error) { return storage.Secret{}, storage.ErrSecretNotFound }
	if _, err := resolveWith(ctx, store, loadKey, "nope"); err == nil {
		t.Fatal("expected error for unknown provider id, got nil")
	}
}

func TestResolveMissingKey(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)

	// A valid anthropic config; New tolerates an empty API key (auth surfaces
	// only on the actual request), so a missing keyring entry must not fail
	// Resolve — it just yields a Provider carrying an empty key.
	if err := Save(ctx, store, []ProviderConfig{{
		ID:   "p1",
		Name: "Claude",
		Type: TypeAnthropic,
		Models: []llm.ModelInfo{
			{ID: "claude-sonnet", ContextWindow: 200000, SupportsTools: true},
		},
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loadKey := func(id string) (storage.Secret, error) {
		if id != SecretID("p1") {
			t.Fatalf("unexpected secret id %q", id)
		}
		return storage.Secret{}, storage.ErrSecretNotFound
	}
	p, err := resolveWith(ctx, store, loadKey, "p1")
	if err != nil {
		t.Fatalf("resolveWith missing key: %v", err)
	}
	if p == nil {
		t.Fatal("expected a provider, got nil")
	}
	if p.Name() != TypeAnthropic {
		t.Fatalf("provider type = %q, want %q", p.Name(), TypeAnthropic)
	}
}

func TestResolveWithKey(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)

	if err := Save(ctx, store, []ProviderConfig{{
		ID:      "p2",
		Name:    "DeepSeek",
		Type:    TypeOpenAICompat,
		BaseURL: "https://api.deepseek.com",
		Models:  []llm.ModelInfo{{ID: "deepseek-chat", ContextWindow: 64000, SupportsTools: true}},
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loadKey := func(string) (storage.Secret, error) { return storage.Secret{Password: "sk-test"}, nil }
	p, err := resolveWith(ctx, store, loadKey, "p2")
	if err != nil {
		t.Fatalf("resolveWith: %v", err)
	}
	if p.Name() != TypeOpenAICompat {
		t.Fatalf("provider type = %q, want %q", p.Name(), TypeOpenAICompat)
	}
}
