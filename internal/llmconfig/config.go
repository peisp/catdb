// Package llmconfig 是 AI Agent 的 Provider 配置管理层：把「app_settings 里的
// Provider 配置（不含密钥）」+「keyring 里的 API Key」两半合成一个可用的
// llm.Provider。它消费 internal/llm（Provider 抽象）与 internal/storage
// （SQLite 配置 + keyring 密钥），本身不感知 UI，也不出现 application.* 调用。
//
// 配置存 app_settings["agent.providers"]（JSON 数组，绝不含密钥）；默认
// provider/model 存 app_settings["agent.provider"] / ["agent.model"]；API Key
// 只进 keyring，条目名 llm:<providerID>（对齐 AGENT_DESIGN.md §3.2）。
package llmconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"catdb/internal/llm"
	// 匿名导入触发两个 adapter 的 init() 注册，llm.New 才认识这些 type。
	_ "catdb/internal/llm/anthropic"
	_ "catdb/internal/llm/openaicompat"
	"catdb/internal/storage"
)

// Provider type 常量，对齐 llm 包各 adapter 的注册名。
const (
	TypeAnthropic    = "anthropic"
	TypeOpenAICompat = "openai-compat"
)

// app_settings 键名，风格对齐 ui.locale。
const (
	keyProviders = "agent.providers"
	keyProvider  = "agent.provider"
	keyModel     = "agent.model"
)

// ProviderConfig 是一个 Provider 实例的非敏感配置（存 SQLite，绝不含密钥）。
// Models 为内置/自定义模型清单：ContextWindow 供上下文水位计算，SupportsTools
// 决定工具能力，二者对 openai-compat 自定义模型无法探测，由配置提供（§3.1）。
type ProviderConfig struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	BaseURL      string          `json:"baseURL"`
	Models       []llm.ModelInfo `json:"models"`
	DefaultModel string          `json:"defaultModel"`
}

// SecretID 返回某 Provider 在 keyring 里的密钥条目名（§3.2）。
func SecretID(providerID string) string { return "llm:" + providerID }

// Load 读回全部 Provider 配置。未配置时返回空切片（非 nil），不报错。
func Load(ctx context.Context, store *storage.Store) ([]ProviderConfig, error) {
	raw, err := store.GetSetting(ctx, keyProviders)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return []ProviderConfig{}, nil
	}
	var out []ProviderConfig
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("llmconfig: parse providers: %w", err)
	}
	if out == nil {
		out = []ProviderConfig{}
	}
	return out, nil
}

// Save 覆盖写全部 Provider 配置。绝不写入任何密钥（密钥走 keyring）。
func Save(ctx context.Context, store *storage.Store, providers []ProviderConfig) error {
	if providers == nil {
		providers = []ProviderConfig{}
	}
	data, err := json.Marshal(providers)
	if err != nil {
		return fmt.Errorf("llmconfig: marshal providers: %w", err)
	}
	return store.SetSetting(ctx, keyProviders, string(data))
}

// GetDefaults 读回默认 Provider 实例 id 与默认模型（未设为空）。
func GetDefaults(ctx context.Context, store *storage.Store) (providerID, model string, err error) {
	if providerID, err = store.GetSetting(ctx, keyProvider); err != nil {
		return "", "", err
	}
	if model, err = store.GetSetting(ctx, keyModel); err != nil {
		return "", "", err
	}
	return providerID, model, nil
}

// SetDefaults 持久化默认 Provider 实例 id 与默认模型。
func SetDefaults(ctx context.Context, store *storage.Store, providerID, model string) error {
	if err := store.SetSetting(ctx, keyProvider, providerID); err != nil {
		return err
	}
	return store.SetSetting(ctx, keyModel, model)
}

// Resolve 按 providerID 合成一个可用的 llm.Provider：读配置 + 从 keyring 取
// 密钥 → llm.New。未知 id 报错。密钥缺失不报错（用空 Key 构造，交由 adapter
// 在实际请求时暴露鉴权错误）。
func Resolve(ctx context.Context, store *storage.Store, secrets *storage.Secrets, providerID string) (llm.Provider, error) {
	return resolveWith(ctx, store, secrets.Load, providerID)
}

// resolveWith 是 Resolve 的可注入密钥读取版本，供单测绕开真实系统 keyring。
func resolveWith(ctx context.Context, store *storage.Store, loadKey func(id string) (storage.Secret, error), providerID string) (llm.Provider, error) {
	providers, err := Load(ctx, store)
	if err != nil {
		return nil, err
	}
	var pc *ProviderConfig
	for i := range providers {
		if providers[i].ID == providerID {
			pc = &providers[i]
			break
		}
	}
	if pc == nil {
		return nil, fmt.Errorf("llmconfig: unknown provider id %q", providerID)
	}
	sec, err := loadKey(SecretID(providerID))
	if err != nil && !errors.Is(err, storage.ErrSecretNotFound) {
		return nil, fmt.Errorf("llmconfig: load key %q: %w", providerID, err)
	}
	return llm.New(llm.Config{
		Type:    pc.Type,
		BaseURL: pc.BaseURL,
		APIKey:  sec.Password,
		Models:  pc.Models,
	})
}
