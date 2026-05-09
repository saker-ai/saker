package provider

import (
	"testing"

	"github.com/cinience/saker/pkg/model"
)

// TestDetect_DashscopeDefaults locks in the dashscope auto-config: the
// default model is deepseek-v4-pro and ExtraBody carries enable_thinking=false
// to skip the chain-of-thought stream that otherwise eats the entire output
// budget on tool-using benchmarks. Both are user-visible defaults — bumping
// either should be a deliberate decision, not a silent regression.
func TestDetect_DashscopeDefaults(t *testing.T) {
	// Avoid t.Parallel here: env-var-dependent siblings aren't sharing state
	// today but t.Setenv would prevent it anyway.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("DASHSCOPE_API_KEY", "test-key")
	t.Setenv("DASHSCOPE_BASE_URL", "")

	provider, modelName := Detect("", "", "")
	if modelName != "deepseek-v4-pro" {
		t.Fatalf("default dashscope model = %q, want deepseek-v4-pro", modelName)
	}
	op, ok := provider.(*model.OpenAIProvider)
	if !ok {
		t.Fatalf("dashscope must produce *OpenAIProvider, got %T", provider)
	}
	if got, ok := op.ExtraBody["enable_thinking"]; !ok || got != false {
		t.Fatalf("ExtraBody[enable_thinking] = %v (present=%v), want explicit false", got, ok)
	}
	// Reasoning models burn the output budget on chain-of-thought; the
	// OpenAI-compat default of 4096 truncates the very first reply with
	// stop_reason=length before any tool call lands. Lock the bumped cap
	// so the regression doesn't silently come back.
	if op.MaxTokens != 131072 {
		t.Fatalf("MaxTokens = %d, want 131072 (room for reasoning + final answer + tool args)", op.MaxTokens)
	}
}

// TestDetect_DashscopeExplicitModelOverride confirms the default isn't
// applied when the caller passes a model flag — the explicit value wins so
// users can run any dashscope-served model while still getting the
// enable_thinking extension applied.
func TestDetect_DashscopeExplicitModelOverride(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("DASHSCOPE_API_KEY", "test-key")

	_, modelName := Detect("dashscope", "qwen3-max", "")
	if modelName != "qwen3-max" {
		t.Fatalf("override model = %q, want qwen3-max", modelName)
	}
}

// TestDetect_DashscopeWinsOverAnthropic locks in the auto-detection
// priority: when both DASHSCOPE_API_KEY and ANTHROPIC_AUTH_TOKEN are set
// (the typical case for users who have Claude Code's settings.json env
// injection in place), dashscope must win. Without this guarantee the
// dashscope default would be impossible to reach via env vars alone and
// users would be forced to pass --provider every invocation.
func TestDetect_DashscopeWinsOverAnthropic(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "anth-token")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("DASHSCOPE_API_KEY", "dash-key")

	prov, modelName := Detect("", "", "")
	if _, ok := prov.(*model.OpenAIProvider); !ok {
		t.Fatalf("expected dashscope (*OpenAIProvider) to win, got %T", prov)
	}
	if modelName != "deepseek-v4-pro" {
		t.Fatalf("model = %q, want deepseek-v4-pro", modelName)
	}
}

// TestDetect_AnthropicWhenNoDashscope guards against accidental over-rotation
// of the priority order: with only ANTHROPIC creds set, the anthropic
// branch must still be selected.
func TestDetect_AnthropicWhenNoDashscope(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "anth-token")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("DASHSCOPE_API_KEY", "")

	prov, _ := Detect("", "", "")
	if _, ok := prov.(*model.AnthropicProvider); !ok {
		t.Fatalf("expected *AnthropicProvider, got %T", prov)
	}
}
