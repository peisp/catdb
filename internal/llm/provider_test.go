package llm_test

import (
	"testing"

	"catdb/internal/llm"

	// 匿名导入让 New 支持这两个类型（对齐驱动注册心智）。
	_ "catdb/internal/llm/anthropic"
	_ "catdb/internal/llm/openaicompat"
)

func TestNewDispatch(t *testing.T) {
	p, err := llm.New(llm.Config{Type: "anthropic", APIKey: "k"})
	if err != nil {
		t.Fatalf("anthropic: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("name = %q", p.Name())
	}

	p2, err := llm.New(llm.Config{Type: "openai-compat", BaseURL: "http://x"})
	if err != nil {
		t.Fatalf("openai-compat: %v", err)
	}
	if p2.Name() != "openai-compat" {
		t.Errorf("name = %q", p2.Name())
	}
}

func TestNewUnknownType(t *testing.T) {
	if _, err := llm.New(llm.Config{Type: "nope"}); err == nil {
		t.Fatal("expected error for unknown provider type")
	}
}

func TestNewOpenAICompatRequiresBaseURL(t *testing.T) {
	if _, err := llm.New(llm.Config{Type: "openai-compat"}); err == nil {
		t.Fatal("expected error when BaseURL missing")
	}
}
