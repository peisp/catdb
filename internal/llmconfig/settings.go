package llmconfig

// AI Agent 运行期设置：隐私开关、循环/超时/预算限额、自动压缩、按模型费用单价表。
// 全部存 app_settings（键名风格对齐 ui.locale / agent.provider），读时带默认值。
// 与 Provider 配置一样，本文件只消费 storage 的 Get/SetSetting，不感知 UI。

import (
	"context"
	"encoding/json"
	"strconv"

	"catdb/internal/storage"
)

// app_settings 键名（对齐 AGENT_DESIGN.md §12 全表）。
const (
	keyPrivacySendRowData = "agent.privacy.sendRowData"
	keyMaxIterations      = "agent.limits.maxIterations"
	keyStmtTimeoutSec     = "agent.limits.stmtTimeoutSec"
	keyTxIdleTimeoutSec   = "agent.limits.txIdleTimeoutSec"
	keyLLMResultRows      = "agent.limits.llmResultRows"
	keySessionTokenBudget = "agent.limits.sessionTokenBudget"
	keyCompactAuto        = "agent.compact.auto"
	keyCompactThreshold   = "agent.compact.threshold"
	keyPricing            = "agent.pricing"
)

// ModelPricing 是单个模型的百万 token 单价（用于费用估算，§9）。空表 = 只显示
// token 不算费用。CacheReadPer1M 对齐 Anthropic prompt caching 的命中价。
type ModelPricing struct {
	InputPer1M     float64 `json:"inputPer1M"`
	OutputPer1M    float64 `json:"outputPer1M"`
	CacheReadPer1M float64 `json:"cacheReadPer1M"`
}

// AgentSettings 是 Agent 运行期设置的合集（不含 Provider/默认模型，那两半由
// Load/GetDefaults 管理）。Pricing 以 modelID 为键。
type AgentSettings struct {
	PrivacySendRowData bool                    `json:"privacySendRowData"`
	MaxIterations      int                     `json:"maxIterations"`
	StmtTimeoutSec     int                     `json:"stmtTimeoutSec"`
	TxIdleTimeoutSec   int                     `json:"txIdleTimeoutSec"`
	LLMResultRows      int                     `json:"llmResultRows"`
	SessionTokenBudget int                     `json:"sessionTokenBudget"`
	CompactAuto        bool                    `json:"compactAuto"`
	CompactThreshold   float64                 `json:"compactThreshold"`
	Pricing            map[string]ModelPricing `json:"pricing"`
}

// DefaultSettings 是全部键未设置时的出厂默认（AGENT_DESIGN.md §12）。
func DefaultSettings() AgentSettings {
	return AgentSettings{
		PrivacySendRowData: true,
		MaxIterations:      25,
		StmtTimeoutSec:     60,
		TxIdleTimeoutSec:   600,
		LLMResultRows:      50,
		SessionTokenBudget: 0,
		CompactAuto:        true,
		CompactThreshold:   0.7,
		Pricing:            map[string]ModelPricing{},
	}
}

// LoadSettings 读回全部 Agent 设置，未设置的键回落默认值。
func LoadSettings(ctx context.Context, store *storage.Store) (AgentSettings, error) {
	s := DefaultSettings()
	get := func(key string) (string, error) { return store.GetSetting(ctx, key) }

	if v, err := get(keyPrivacySendRowData); err != nil {
		return s, err
	} else if v != "" {
		s.PrivacySendRowData = v == "true"
	}
	for _, f := range []struct {
		key string
		dst *int
	}{
		{keyMaxIterations, &s.MaxIterations},
		{keyStmtTimeoutSec, &s.StmtTimeoutSec},
		{keyTxIdleTimeoutSec, &s.TxIdleTimeoutSec},
		{keyLLMResultRows, &s.LLMResultRows},
		{keySessionTokenBudget, &s.SessionTokenBudget},
	} {
		v, err := get(f.key)
		if err != nil {
			return s, err
		}
		if v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*f.dst = n
			}
		}
	}
	if v, err := get(keyCompactAuto); err != nil {
		return s, err
	} else if v != "" {
		s.CompactAuto = v == "true"
	}
	if v, err := get(keyCompactThreshold); err != nil {
		return s, err
	} else if v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			s.CompactThreshold = f
		}
	}
	if v, err := get(keyPricing); err != nil {
		return s, err
	} else if v != "" {
		var p map[string]ModelPricing
		if err := json.Unmarshal([]byte(v), &p); err == nil && p != nil {
			s.Pricing = p
		}
	}
	return s, nil
}

// SaveSettings 覆盖写全部 Agent 设置键。Pricing 为 nil 时按空表写入。
func SaveSettings(ctx context.Context, store *storage.Store, s AgentSettings) error {
	if s.Pricing == nil {
		s.Pricing = map[string]ModelPricing{}
	}
	pricingJSON, err := json.Marshal(s.Pricing)
	if err != nil {
		return err
	}
	pairs := []struct {
		key, val string
	}{
		{keyPrivacySendRowData, strconv.FormatBool(s.PrivacySendRowData)},
		{keyMaxIterations, strconv.Itoa(s.MaxIterations)},
		{keyStmtTimeoutSec, strconv.Itoa(s.StmtTimeoutSec)},
		{keyTxIdleTimeoutSec, strconv.Itoa(s.TxIdleTimeoutSec)},
		{keyLLMResultRows, strconv.Itoa(s.LLMResultRows)},
		{keySessionTokenBudget, strconv.Itoa(s.SessionTokenBudget)},
		{keyCompactAuto, strconv.FormatBool(s.CompactAuto)},
		{keyCompactThreshold, strconv.FormatFloat(s.CompactThreshold, 'g', -1, 64)},
		{keyPricing, string(pricingJSON)},
	}
	for _, p := range pairs {
		if err := store.SetSetting(ctx, p.key, p.val); err != nil {
			return err
		}
	}
	return nil
}
