package model

import (
	"strings"
	"sync"
)

// knownContextWindows maps model name prefixes to context window sizes (tokens).
// Lookup uses longest-prefix matching, so "gpt-4o" matches before "gpt-4".
// Data sourced from OpenRouter API and provider documentation.
var knownContextWindows = map[string]int{
	// Anthropic Claude
	"claude-3.5-sonnet": 200_000,
	"claude-3.5-haiku":  200_000,
	"claude-3-5-sonnet": 200_000, // alternate naming
	"claude-3-5-haiku":  200_000,
	"claude-3.7-sonnet": 200_000,
	"claude-3-opus":     200_000,
	"claude-3-haiku":    200_000,
	"claude-3-sonnet":   200_000,
	"claude-sonnet-4":   200_000,
	"claude-sonnet-4.5": 1_000_000,
	"claude-sonnet-4.6": 1_000_000,
	"claude-opus-4":     200_000,
	"claude-opus-4.1":   200_000,
	"claude-opus-4.5":   200_000,
	"claude-opus-4.6":   1_000_000,
	"claude-haiku-4":    200_000,
	"claude-haiku-4.5":  200_000,

	// OpenAI GPT
	"gpt-5":         400_000,
	"gpt-4.1":       1_047_576,
	"gpt-4o":        128_000,
	"gpt-4-turbo":   128_000,
	"gpt-4-0125":    128_000,
	"gpt-4-1106":    128_000,
	"gpt-4":         8_192,
	"gpt-3.5-turbo": 16_385,

	// OpenAI reasoning models
	"o1":      200_000,
	"o3":      200_000,
	"o4-mini": 200_000,

	// DashScope / Qwen (via OpenAI-compatible API)
	"qwen-max":      32_768,
	"qwen-plus":     1_000_000,
	"qwen-turbo":    131_072,
	"qwen-vl-max":   131_072,
	"qwen-vl-plus":  131_072,
	"qwen-long":     10_000_000,
	"qwen2.5-vl":    128_000,
	"qwen2.5-coder": 32_768,
	"qwen2.5":       32_768,
	"qwen3.5":       262_144,
	"qwen3-coder":   262_144,
	"qwen3-max":     262_144,
	"qwen3-vl":      131_072,
	"qwen3-235b":    131_072,
	"qwen3-30b":     40_960,
	"qwen3-32b":     40_960,
	"qwen3-14b":     40_960,
	"qwen3-8b":      40_960,
	"qwen3":         40_960, // fallback for qwen3 open-weight models
	"qwq-32b":       131_072,

	// DeepSeek
	"deepseek-chat-v3":  163_840,
	"deepseek-chat":     163_840,
	"deepseek-coder":    64_000,
	"deepseek-reasoner": 64_000,
	"deepseek-r1-0528":  163_840,
	"deepseek-r1":       64_000,
	"deepseek-v3":       163_840,
	"deepseek-v4-pro":   1_000_000,
	"deepseek-v4-flash": 1_000_000,
	"deepseek-v4":       1_000_000,

	// Google Gemini
	"gemini-3":         1_048_576,
	"gemini-2.5-pro":   1_048_576,
	"gemini-2.5-flash": 1_048_576,
	"gemini-2.0-flash": 1_048_576,
	"gemini-2":         1_048_576,
	"gemini-1.5-pro":   2_000_000,
	"gemini-1.5-flash": 1_000_000,

	// Meta Llama
	"llama-4-maverick": 1_048_576,
	"llama-4-scout":    327_680,
	"llama-4":          1_048_576,
	"llama-3.3":        131_072,
	"llama-3.2":        131_072,
	"llama-3.1":        131_072,

	// Mistral
	"codestral":      256_000,
	"devstral":       262_144,
	"mistral-large":  128_000,
	"mistral-medium": 131_072,
	"mistral-small":  128_000,
	"mistral-nemo":   131_072,
	"mistral-saba":   32_768,
	"ministral":      262_144,

	// Kimi / Moonshot
	"kimi-k2.6":        262_144,
	"kimi-k2.5":        262_144,
	"kimi-k2":          131_072,
	"moonshot-v1-128k": 128_000,
	"moonshot-v1-32k":  32_000,
	"moonshot-v1-8k":   8_000,

	// Cohere
	"command-r": 128_000,

	// GLM (Zhipu AI)
	"glm-5.1":      202_745,
	"glm-5-turbo":  202_752,
	"glm-5v-turbo": 202_752,
	"glm-5":        202_752,
	"glm-4.7":      169_984,
	"glm-4.6":      169_984,
	"glm-4.6v":     131_072,
	"glm-4.5":      131_072,
	"glm-4-32b":    128_000,
	"glm-4":        128_000,

	// MiniMax
	"minimax-m2.7": 204_800,
	"minimax-m2.5": 196_608,
	"minimax-m2":   196_608,
	"minimax-m1":   1_000_000,
	"minimax-01":   1_000_192,

	// Baidu ERNIE
	"ernie-4.5": 128_000,

	// ByteDance Seed / Doubao
	"seed-2.0": 262_144,
	"seed-1.6": 262_144,
	"doubao":   128_000,

	// StepFun
	"step-3.5": 262_144,
	"step-2":   128_000,
	"step-1":   128_000,

	// Qwen 3.5+ (extended context)
	"qwen3.6": 1_000_000,

	// Yi (01.AI)
	"yi-lightning": 16_384,
	"yi-large":     32_768,

	// InternLM
	"internlm": 32_768,
}

var (
	customMu      sync.RWMutex
	customWindows map[string]int
)

// LookupContextWindow returns the context window size (tokens) for a known
// model name. Uses longest-prefix matching against the built-in registry
// and any entries added via RegisterContextWindow. Returns 0 if the model
// is not recognized.
func LookupContextWindow(modelName string) int {
	name := strings.TrimSpace(strings.ToLower(modelName))
	if name == "" {
		return 0
	}

	// Check custom overrides first (exact then prefix).
	customMu.RLock()
	if tokens := lookupInMap(customWindows, name); tokens > 0 {
		customMu.RUnlock()
		return tokens
	}
	customMu.RUnlock()

	// Check built-in registry.
	if tokens := lookupInMap(knownContextWindows, name); tokens > 0 {
		return tokens
	}

	// Fallback to embedded LiteLLM data.
	if tokens := LookupLiteLLMContextWindow(name); tokens > 0 {
		return tokens
	}

	// Fallback to China model catalog.
	if china, ok := LookupChinaModelInfo(name); ok && china.MaxInputTokens > 0 {
		return china.MaxInputTokens
	}
	return 0
}

// RegisterContextWindow adds or overrides a model's context window size.
// The prefix is matched case-insensitively against model names.
func RegisterContextWindow(prefix string, tokens int) {
	prefix = strings.TrimSpace(strings.ToLower(prefix))
	if prefix == "" || tokens <= 0 {
		return
	}
	customMu.Lock()
	if customWindows == nil {
		customWindows = make(map[string]int)
	}
	customWindows[prefix] = tokens
	customMu.Unlock()
}

func lookupInMap(m map[string]int, name string) int {
	if len(m) == 0 {
		return 0
	}
	// Exact match.
	if tokens, ok := m[name]; ok {
		return tokens
	}
	// Longest prefix match.
	bestLen := 0
	bestTokens := 0
	for prefix, tokens := range m {
		if strings.HasPrefix(name, prefix) && len(prefix) > bestLen {
			bestLen = len(prefix)
			bestTokens = tokens
		}
	}
	return bestTokens
}
