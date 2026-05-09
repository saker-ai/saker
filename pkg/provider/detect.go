package provider

import (
	"os"
	"strings"

	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/security"
)

// Detect selects and configures a model provider based on explicit flags and
// environment variables. Auto-detection order: DASHSCOPE_API_KEY → dashscope,
// ANTHROPIC_API_KEY → anthropic, OPENAI_API_KEY → openai, fallback → openai.
//
// Dashscope wins over the others when present because users running
// dashscope-backed deployments often also have ANTHROPIC_AUTH_TOKEN set
// (e.g. via Claude Code's settings.json env injection) for unrelated
// tooling — without this priority dashscope would never be the default
// even after explicitly setting DASHSCOPE_API_KEY. Callers can still
// force a specific provider with --provider.
func Detect(providerFlag, modelFlag, system string) (model.Provider, string) {
	provider := strings.ToLower(strings.TrimSpace(providerFlag))

	if provider == "" {
		switch {
		case os.Getenv("DASHSCOPE_API_KEY") != "":
			provider = "dashscope"
		case os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("ANTHROPIC_AUTH_TOKEN") != "":
			provider = "anthropic"
		case os.Getenv("OPENAI_API_KEY") != "":
			provider = "openai"
		default:
			provider = "openai"
		}
	}

	switch provider {
	case "dashscope":
		key := security.ResolveEnv(os.Getenv("DASHSCOPE_API_KEY"))
		m := strings.TrimSpace(modelFlag)
		if m == "" {
			m = "deepseek-v4-pro"
		}
		baseURL := strings.TrimSpace(os.Getenv("DASHSCOPE_BASE_URL"))
		if baseURL == "" {
			baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
		}
		model.SetChinaModelRegion(model.DetectChinaRegionFromBaseURL(baseURL))
		// Dashscope's OpenAI-compatible endpoint accepts `enable_thinking`
		// as an extension to toggle the reasoning trace. Injected
		// unconditionally — endpoints that don't recognize it will ignore
		// the field per their compatibility contract.
		// Reasoning models like deepseek-v4-pro spend most of their output
		// budget on the chain-of-thought stream, so the OpenAI default
		// (4096) is far too small — a single first-turn answer will hit
		// `stop_reason: length` before producing any tool call. Bump the
		// per-request cap to 131072, which fits comfortably below
		// dashscope-served deepseek-v4-pro's documented 393,216 limit
		// while leaving room for reasoning + final output + tool args.
		return &model.OpenAIProvider{
			APIKey:    key,
			BaseURL:   baseURL,
			ModelName: m,
			System:    system,
			MaxTokens: 131072,
			ExtraBody: map[string]any{
				"enable_thinking": false,
			},
		}, m
	case "openai":
		key := security.ResolveEnv(os.Getenv("OPENAI_API_KEY"))
		baseURL := os.Getenv("OPENAI_BASE_URL")
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
		return &model.AnthropicProvider{
			ModelName: m,
			System:    system,
		}, m
	}
}
