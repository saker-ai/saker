package model

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed data/model_prices_and_context_window.json
var litellmRawData []byte

// ModelInfo holds parsed model metadata from LiteLLM's
// model_prices_and_context_window.json.
type ModelInfo struct {
	MaxInputTokens  int    `json:"max_input_tokens"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	MaxTokens       int    `json:"max_tokens"`
	Mode            string `json:"mode"`
	Provider        string `json:"litellm_provider"`

	// Pricing (per token)
	InputCostPerToken  float64 `json:"input_cost_per_token"`
	OutputCostPerToken float64 `json:"output_cost_per_token"`

	// Cache pricing
	CacheCreationCostPerToken float64 `json:"cache_creation_input_token_cost"`
	CacheReadCostPerToken     float64 `json:"cache_read_input_token_cost"`

	// Capabilities
	SupportsVision          bool `json:"supports_vision"`
	SupportsFunctionCalling bool `json:"supports_function_calling"`
	SupportsPromptCaching   bool `json:"supports_prompt_caching"`
	SupportsResponseSchema  bool `json:"supports_response_schema"`
	SupportsReasoning       bool `json:"supports_reasoning"`
}

// ModelCost holds calculated costs for a model completion.
type ModelCost struct {
	InputCost  float64
	OutputCost float64
	TotalCost  float64
	Currency   string // "USD" (default) or "CNY"
}

var (
	litellmOnce   sync.Once
	litellmModels map[string]ModelInfo // full key "provider/model" → info
	litellmByName map[string]ModelInfo // bare model name → info (deduped, largest context wins)
)

func ensureLiteLLMLoaded() {
	litellmOnce.Do(func() {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(litellmRawData, &raw); err != nil {
			litellmModels = make(map[string]ModelInfo)
			litellmByName = make(map[string]ModelInfo)
			return
		}

		models := make(map[string]ModelInfo, len(raw))
		for key, data := range raw {
			if key == "sample_spec" {
				continue
			}
			var info ModelInfo
			if err := json.Unmarshal(data, &info); err != nil {
				continue
			}
			models[key] = info
		}
		litellmModels = models
		litellmByName = buildNameIndex(models)
	})
}

// buildNameIndex strips provider prefixes and deduplicates by keeping
// the entry with the largest max_input_tokens for each bare model name.
func buildNameIndex(models map[string]ModelInfo) map[string]ModelInfo {
	index := make(map[string]ModelInfo, len(models))
	for key, info := range models {
		name := stripProviderPrefix(key)
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if existing, ok := index[name]; ok {
			// Keep the entry with the larger context window.
			if info.MaxInputTokens <= existing.MaxInputTokens {
				continue
			}
		}
		index[name] = info
	}
	return index
}

// stripProviderPrefix removes common provider prefixes like
// "anthropic/", "openai/", "bedrock/anthropic.", etc.
func stripProviderPrefix(key string) string {
	// Handle slash-separated: "openai/gpt-4o" → "gpt-4o"
	if idx := strings.LastIndex(key, "/"); idx >= 0 {
		return key[idx+1:]
	}
	// Handle dot-separated provider: "anthropic.claude-3" → "claude-3"
	// Only strip if the prefix looks like a known provider pattern.
	for _, prefix := range []string{
		"anthropic.", "openai.", "bedrock.", "vertex_ai.",
		"azure.", "cohere.", "mistral.", "deepseek.", "google.",
		"fireworks_ai.", "together_ai.", "groq.", "cerebras.",
	} {
		if strings.HasPrefix(key, prefix) {
			return key[len(prefix):]
		}
	}
	return key
}

// LookupModelInfo returns full model metadata for the given model name.
// It searches the embedded LiteLLM data by bare model name (provider
// prefix stripped), then falls back to the China model catalog.
// Returns false if the model is not found in either source.
func LookupModelInfo(modelName string) (ModelInfo, bool) {
	ensureLiteLLMLoaded()
	name := strings.ToLower(strings.TrimSpace(modelName))
	if name == "" {
		return ModelInfo{}, false
	}

	// Exact match on bare name.
	if info, ok := litellmByName[name]; ok {
		return info, true
	}

	// Exact match on full key (with provider prefix).
	if info, ok := litellmModels[name]; ok {
		return info, true
	}

	// Longest prefix match on bare name index.
	bestLen := 0
	var bestInfo ModelInfo
	found := false
	for prefix, info := range litellmByName {
		if strings.HasPrefix(name, prefix) && len(prefix) > bestLen {
			bestLen = len(prefix)
			bestInfo = info
			found = true
		}
	}
	if found {
		return bestInfo, true
	}

	// Fallback: convert from China model catalog.
	if china, ok := LookupChinaModelInfo(modelName); ok {
		return chinaToModelInfo(china), true
	}
	return ModelInfo{}, false
}

// chinaToModelInfo converts a ChinaModelInfo to a ModelInfo.
// Pricing is converted from per-million-tokens to per-token.
// Uses regional pricing if a region is set.
func chinaToModelInfo(c ChinaModelInfo) ModelInfo {
	price, _ := c.Pricing.resolvedPricing()
	divisor := 1_000_000.0

	return ModelInfo{
		MaxInputTokens:          c.MaxInputTokens,
		MaxOutputTokens:         c.MaxOutputTokens,
		Mode:                    c.Mode,
		Provider:                c.Provider,
		InputCostPerToken:       price.InputPerMillion / divisor,
		OutputCostPerToken:      price.OutputPerMillion / divisor,
		CacheReadCostPerToken:   price.CacheReadPerMillion / divisor,
		SupportsVision:          c.SupportsVision,
		SupportsFunctionCalling: c.SupportsFunctionCalling,
		SupportsPromptCaching:   c.SupportsPromptCaching,
		SupportsResponseSchema:  c.SupportsResponseSchema,
		SupportsReasoning:       c.SupportsReasoning,
	}
}

// LookupLiteLLMContextWindow returns the max_input_tokens from embedded
// LiteLLM data for the given model name. Returns 0 if not found.
func LookupLiteLLMContextWindow(modelName string) int {
	info, ok := LookupModelInfo(modelName)
	if !ok {
		return 0
	}
	if info.MaxInputTokens > 0 {
		return info.MaxInputTokens
	}
	return info.MaxTokens
}

// EstimateCost calculates the cost for a model completion based on
// embedded pricing data and the given token usage.
// For China models with tiered pricing, it selects the tier matching
// the input token count. Currency is set to "CNY" or "USD" accordingly.
func EstimateCost(modelName string, usage Usage) ModelCost {
	// Try China model catalog first for accurate regional pricing.
	if china, ok := LookupChinaModelInfo(modelName); ok {
		return estimateChinaCost(china, usage)
	}

	info, ok := LookupModelInfo(modelName)
	if !ok {
		return ModelCost{}
	}

	inputCost := float64(usage.InputTokens) * info.InputCostPerToken
	outputCost := float64(usage.OutputTokens) * info.OutputCostPerToken

	// Add cache costs if applicable.
	if usage.CacheCreationTokens > 0 && info.CacheCreationCostPerToken > 0 {
		inputCost += float64(usage.CacheCreationTokens) * info.CacheCreationCostPerToken
	}
	if usage.CacheReadTokens > 0 && info.CacheReadCostPerToken > 0 {
		inputCost += float64(usage.CacheReadTokens) * info.CacheReadCostPerToken
	}

	return ModelCost{
		InputCost:  inputCost,
		OutputCost: outputCost,
		TotalCost:  inputCost + outputCost,
		Currency:   "USD",
	}
}

// estimateChinaCost calculates cost using China model pricing.
// Supports flat default pricing, tiered pricing, and regional overrides.
func estimateChinaCost(info ChinaModelInfo, usage Usage) ModelCost {
	price, tiers := info.Pricing.resolvedPricing()
	divisor := 1_000_000.0

	// Use tiered pricing if available and input tokens are specified.
	if len(tiers) > 0 && usage.InputTokens > 0 {
		for _, tier := range tiers {
			if usage.InputTokens >= tier.MinInputTokens &&
				(tier.MaxInputTokens == 0 || usage.InputTokens < tier.MaxInputTokens) {
				price.InputPerMillion = tier.InputPerMillion
				price.OutputPerMillion = tier.OutputPerMillion
				if tier.OutputThinkingPerMillion > 0 {
					price.OutputThinkingPerMillion = tier.OutputThinkingPerMillion
				}
				break
			}
		}
	}

	inputCost := float64(usage.InputTokens) * price.InputPerMillion / divisor
	outputCost := float64(usage.OutputTokens) * price.OutputPerMillion / divisor

	// Add cache costs if applicable.
	if usage.CacheReadTokens > 0 && price.CacheReadPerMillion > 0 {
		inputCost += float64(usage.CacheReadTokens) * price.CacheReadPerMillion / divisor
	}
	if usage.CacheCreationTokens > 0 && price.CacheWritePerMillion > 0 {
		inputCost += float64(usage.CacheCreationTokens) * price.CacheWritePerMillion / divisor
	}
	if price.CacheHitPerMillion > 0 && usage.CacheReadTokens > 0 {
		inputCost += float64(usage.CacheReadTokens) * price.CacheHitPerMillion / divisor
	}

	return ModelCost{
		InputCost:  inputCost,
		OutputCost: outputCost,
		TotalCost:  inputCost + outputCost,
		Currency:   info.Pricing.Currency,
	}
}

// LiteLLMModelCount returns the number of models in the embedded data.
// Useful for diagnostics and testing.
func LiteLLMModelCount() int {
	ensureLiteLLMLoaded()
	return len(litellmModels)
}
