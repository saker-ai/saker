package provider

import (
	"os"
	"strings"

	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/security"
)

// Detect selects and configures a model provider based on explicit flags and
// environment variables. The protocol layer recognizes exactly two wire
// formats:
//
//   - "anthropic" — Claude messages API; honors ANTHROPIC_BASE_URL and
//     ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN. Use this for the official
//     api.anthropic.com endpoint or any Claude-compatible proxy.
//   - "openai"    — OpenAI chat completions API; honors OPENAI_BASE_URL and
//     OPENAI_API_KEY. Use this for the official OpenAI endpoint, Azure
//     OpenAI, or any OpenAI-compatible vendor (DashScope, Moonshot, Zhipu,
//     DeepSeek, Together, Groq, vLLM, …) by pointing OPENAI_BASE_URL at
//     their `/v1` endpoint.
//
// Auto-detection picks "anthropic" when ANTHROPIC creds are set, otherwise
// "openai". Callers can still force a specific provider with --provider.
//
// The OpenAI branch detects Chinese-cloud base URLs (e.g. dashscope.aliyuncs.com)
// and switches the global pricing region so the cost ledger uses
// china_mainland rates instead of US.
func Detect(providerFlag, modelFlag, system string) (model.Provider, string) {
	if modelFlag == "" {
		modelFlag = os.Getenv("SAKER_MODEL")
	}
	provider := strings.ToLower(strings.TrimSpace(providerFlag))

	if provider == "" {
		switch {
		case os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("ANTHROPIC_AUTH_TOKEN") != "":
			provider = "anthropic"
		case os.Getenv("OPENAI_API_KEY") != "":
			provider = "openai"
		default:
			provider = "openai"
		}
	}

	switch provider {
	case "openai":
		key := security.ResolveEnv(os.Getenv("OPENAI_API_KEY"))
		baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
		// When OPENAI_BASE_URL points at a Chinese cloud (e.g. DashScope's
		// compatible-mode endpoint), the cost ledger needs china_mainland
		// rates rather than the OpenAI defaults. The detection is a no-op
		// for non-Chinese base URLs.
		if baseURL != "" {
			model.SetChinaModelRegion(model.DetectChinaRegionFromBaseURL(baseURL))
		}
		m := strings.TrimSpace(modelFlag)
		if m == "" {
			m = "gpt-4o"
		}
		return &model.OpenAIProvider{
			APIKey:    key,
			BaseURL:   baseURL,
			ModelName: m,
			System:    system,
		}, m
	default: // "anthropic"
		m := strings.TrimSpace(modelFlag)
		if m == "" {
			m = "claude-sonnet-4-20250514"
		}
		// ANTHROPIC_BASE_URL lets users point at a Claude-compatible proxy
		// (e.g. third-party gateways serving GLM / Qwen via Anthropic schema).
		// Without this fallback, the env var that Claude Code itself honors
		// is silently ignored here.
		baseURL := strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL"))
		return &model.AnthropicProvider{
			ModelName: m,
			BaseURL:   baseURL,
			System:    system,
		}, m
	}
}
