package provider

import (
	"testing"

	"github.com/cinience/saker/pkg/model"
)

// TestDetect_AnthropicWinsAutoDetect locks in the auto-detection priority:
// when ANTHROPIC creds are set, the anthropic branch is picked even if
// OPENAI_API_KEY is also present. This matches the Claude Code env layout
// users typically have via .bashrc.
func TestDetect_AnthropicWinsAutoDetect(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "anth-token")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("SAKER_MODEL", "")

	prov, modelName := Detect("", "", "")
	if _, ok := prov.(*model.AnthropicProvider); !ok {
		t.Fatalf("expected *AnthropicProvider, got %T", prov)
	}
	if modelName != "claude-sonnet-4-20250514" {
		t.Fatalf("default anthropic model = %q, want claude-sonnet-4-20250514", modelName)
	}
}

// TestDetect_OpenAIFallback covers the no-anthropic path: OPENAI_API_KEY
// alone selects the openai branch, and the absence of any creds also lands
// on openai by default.
func TestDetect_OpenAIFallback(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("SAKER_MODEL", "")

	prov, modelName := Detect("", "", "")
	if _, ok := prov.(*model.OpenAIProvider); !ok {
		t.Fatalf("expected *OpenAIProvider, got %T", prov)
	}
	if modelName != "gpt-4o" {
		t.Fatalf("default openai model = %q, want gpt-4o", modelName)
	}
}

// TestDetect_SakerModelEnvOverridesDefault verifies that SAKER_MODEL is
// used when no --model flag is passed, so .env-based configuration works.
func TestDetect_SakerModelEnvOverridesDefault(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "anth-token")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("SAKER_MODEL", "glm-5.1")

	prov, modelName := Detect("", "", "")
	if _, ok := prov.(*model.AnthropicProvider); !ok {
		t.Fatalf("expected *AnthropicProvider, got %T", prov)
	}
	if modelName != "glm-5.1" {
		t.Fatalf("model = %q, want glm-5.1 from SAKER_MODEL env", modelName)
	}
}

// TestDetect_AnthropicHonorsBaseURL pins the env-var fallback that Claude
// Code itself respects: when ANTHROPIC_BASE_URL is set, the AnthropicProvider
// must carry it so requests reach the configured proxy/gateway instead of
// the official api.anthropic.com endpoint. This unblocks running
// Anthropic-schema proxies that serve non-Anthropic models like glm-5.1.
func TestDetect_AnthropicHonorsBaseURL(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "anth-token")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_BASE_URL", "https://proxy.example.com/anthropic/v1")

	prov, _ := Detect("anthropic", "glm-5.1", "")
	ap, ok := prov.(*model.AnthropicProvider)
	if !ok {
		t.Fatalf("expected *AnthropicProvider, got %T", prov)
	}
	if ap.BaseURL != "https://proxy.example.com/anthropic/v1" {
		t.Fatalf("AnthropicProvider.BaseURL = %q, want proxy URL from ANTHROPIC_BASE_URL", ap.BaseURL)
	}
	if ap.ModelName != "glm-5.1" {
		t.Fatalf("AnthropicProvider.ModelName = %q, want glm-5.1", ap.ModelName)
	}
}

// TestDetect_AnthropicBaseURLEmptyByDefault confirms the env fallback only
// kicks in when ANTHROPIC_BASE_URL is set — no surprise leak from random
// stale env on systems that haven't opted into a proxy.
func TestDetect_AnthropicBaseURLEmptyByDefault(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "anth-token")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")

	prov, _ := Detect("anthropic", "claude-test", "")
	ap, ok := prov.(*model.AnthropicProvider)
	if !ok {
		t.Fatalf("expected *AnthropicProvider, got %T", prov)
	}
	if ap.BaseURL != "" {
		t.Fatalf("AnthropicProvider.BaseURL = %q, want empty when env unset", ap.BaseURL)
	}
}

// TestDetect_OpenAIHonorsBaseURL covers DashScope / Moonshot / Zhipu / any
// OpenAI-compatible vendor: the OpenAIProvider must carry OPENAI_BASE_URL
// so requests reach the configured endpoint rather than api.openai.com.
func TestDetect_OpenAIHonorsBaseURL(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "ds-key")
	t.Setenv("OPENAI_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1")

	prov, _ := Detect("openai", "glm-5.1", "")
	op, ok := prov.(*model.OpenAIProvider)
	if !ok {
		t.Fatalf("expected *OpenAIProvider, got %T", prov)
	}
	if op.BaseURL != "https://dashscope.aliyuncs.com/compatible-mode/v1" {
		t.Fatalf("OpenAIProvider.BaseURL = %q, want DashScope endpoint from OPENAI_BASE_URL", op.BaseURL)
	}
	if op.ModelName != "glm-5.1" {
		t.Fatalf("OpenAIProvider.ModelName = %q, want glm-5.1", op.ModelName)
	}
}

// TestDetect_OpenAIChinaRegionDetection pins the side-effect of the openai
// branch: when OPENAI_BASE_URL points at a Chinese cloud, the cost ledger
// must switch to china_mainland pricing. Without this, DashScope-via-openai
// users see US-rate cost estimates.
func TestDetect_OpenAIChinaRegionDetection(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "ds-key")
	t.Setenv("OPENAI_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1")

	// Reset to a known empty value so we can assert the call mutated it.
	model.SetChinaModelRegion("")
	t.Cleanup(func() { model.SetChinaModelRegion("") })

	_, _ = Detect("openai", "glm-5.1", "")
	if got := model.ChinaModelRegion(); got != "china_mainland" {
		t.Fatalf("ChinaModelRegion after Detect = %q, want china_mainland", got)
	}
}
