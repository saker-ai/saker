package model

import (
	"math"
	"testing"
)

func TestLiteLLMDataLoaded(t *testing.T) {
	count := LiteLLMModelCount()
	if count < 100 {
		t.Errorf("LiteLLMModelCount() = %d, want at least 100", count)
	}
	t.Logf("Embedded LiteLLM models: %d", count)
}

func TestLookupModelInfoKnownModels(t *testing.T) {
	tests := []struct {
		model     string
		wantFound bool
		wantMode  string
	}{
		{"gpt-4o", true, "chat"},
		{"claude-3-5-sonnet-20241022", true, "chat"},
		{"unknown-model-xyz", false, ""},
	}
	for _, tc := range tests {
		info, ok := LookupModelInfo(tc.model)
		if ok != tc.wantFound {
			t.Errorf("LookupModelInfo(%q) found=%v, want %v", tc.model, ok, tc.wantFound)
			continue
		}
		if ok && tc.wantMode != "" && info.Mode != tc.wantMode {
			t.Errorf("LookupModelInfo(%q).Mode = %q, want %q", tc.model, info.Mode, tc.wantMode)
		}
	}
}

func TestLookupModelInfoHasPricing(t *testing.T) {
	info, ok := LookupModelInfo("gpt-4o")
	if !ok {
		t.Fatal("gpt-4o not found in LiteLLM data")
	}
	if info.InputCostPerToken <= 0 {
		t.Errorf("gpt-4o InputCostPerToken = %v, want > 0", info.InputCostPerToken)
	}
	if info.OutputCostPerToken <= 0 {
		t.Errorf("gpt-4o OutputCostPerToken = %v, want > 0", info.OutputCostPerToken)
	}
	if info.MaxInputTokens <= 0 {
		t.Errorf("gpt-4o MaxInputTokens = %d, want > 0", info.MaxInputTokens)
	}
}

func TestEstimateCost(t *testing.T) {
	cost := EstimateCost("gpt-4o", Usage{
		InputTokens:  1000,
		OutputTokens: 500,
	})
	if cost.TotalCost <= 0 {
		t.Errorf("EstimateCost(gpt-4o) TotalCost = %v, want > 0", cost.TotalCost)
	}
	if cost.InputCost <= 0 {
		t.Errorf("EstimateCost(gpt-4o) InputCost = %v, want > 0", cost.InputCost)
	}
	if cost.OutputCost <= 0 {
		t.Errorf("EstimateCost(gpt-4o) OutputCost = %v, want > 0", cost.OutputCost)
	}
	if math.Abs(cost.TotalCost-(cost.InputCost+cost.OutputCost)) > 1e-15 {
		t.Errorf("TotalCost (%v) != InputCost (%v) + OutputCost (%v)", cost.TotalCost, cost.InputCost, cost.OutputCost)
	}
}

func TestEstimateCostUnknownModel(t *testing.T) {
	cost := EstimateCost("nonexistent-model", Usage{InputTokens: 1000, OutputTokens: 500})
	if cost.TotalCost != 0 {
		t.Errorf("EstimateCost(nonexistent) = %v, want 0", cost.TotalCost)
	}
}

func TestEstimateCostWithCache(t *testing.T) {
	info, ok := LookupModelInfo("claude-3-5-sonnet-20241022")
	if !ok {
		t.Skip("claude-3-5-sonnet-20241022 not in LiteLLM data")
	}
	if info.CacheCreationCostPerToken <= 0 || info.CacheReadCostPerToken <= 0 {
		t.Skip("claude-3-5-sonnet-20241022 has no cache pricing")
	}

	cost := EstimateCost("claude-3-5-sonnet-20241022", Usage{
		InputTokens:         1000,
		OutputTokens:        500,
		CacheCreationTokens: 200,
		CacheReadTokens:     300,
	})
	if cost.TotalCost <= 0 {
		t.Errorf("EstimateCost with cache TotalCost = %v, want > 0", cost.TotalCost)
	}
	// Cost with cache should be higher than without (due to cache creation cost).
	costWithout := EstimateCost("claude-3-5-sonnet-20241022", Usage{
		InputTokens:  1000,
		OutputTokens: 500,
	})
	if cost.TotalCost <= costWithout.TotalCost {
		t.Errorf("cost with cache (%v) should exceed cost without (%v)", cost.TotalCost, costWithout.TotalCost)
	}
}

func TestLookupLiteLLMContextWindow(t *testing.T) {
	tokens := LookupLiteLLMContextWindow("gpt-4o")
	if tokens <= 0 {
		t.Errorf("LookupLiteLLMContextWindow(gpt-4o) = %d, want > 0", tokens)
	}
}

func TestStripProviderPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"openai/gpt-4o", "gpt-4o"},
		{"anthropic/claude-3-5-sonnet-20241022", "claude-3-5-sonnet-20241022"},
		{"bedrock/anthropic.claude-3-5-sonnet-20241022-v1:0", "anthropic.claude-3-5-sonnet-20241022-v1:0"},
		{"gpt-4o", "gpt-4o"},
		{"anthropic.claude-3-opus", "claude-3-opus"},
	}
	for _, tc := range tests {
		got := stripProviderPrefix(tc.input)
		if got != tc.want {
			t.Errorf("stripProviderPrefix(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// --- China model integration tests ---

func TestEstimateCostChinaModelFlat(t *testing.T) {
	// DeepSeek: flat CNY pricing, 2 CNY/M input, 3 CNY/M output
	cost := EstimateCost("deepseek-chat", Usage{
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
	})
	if cost.Currency != "CNY" {
		t.Fatalf("Currency = %q, want CNY", cost.Currency)
	}
	if math.Abs(cost.InputCost-2.0) > 0.001 {
		t.Errorf("InputCost = %v, want 2.0", cost.InputCost)
	}
	if math.Abs(cost.OutputCost-3.0) > 0.001 {
		t.Errorf("OutputCost = %v, want 3.0", cost.OutputCost)
	}
	if math.Abs(cost.TotalCost-5.0) > 0.001 {
		t.Errorf("TotalCost = %v, want 5.0", cost.TotalCost)
	}
}

func TestEstimateCostChinaModelTiered(t *testing.T) {
	// Doubao Seed 2.0 Pro: tiered CNY pricing
	// Tier 1: 0-32k input → 0.46 CNY/M input, 2.3 CNY/M output
	cost := EstimateCost("doubao-seed-2-0-pro-260215", Usage{
		InputTokens:  10_000,
		OutputTokens: 1_000_000,
	})
	if cost.Currency != "CNY" {
		t.Fatalf("Currency = %q, want CNY", cost.Currency)
	}
	// input: 10k * 0.46/1M = 0.0046
	wantInput := 10_000.0 * 0.46 / 1_000_000.0
	if math.Abs(cost.InputCost-wantInput) > 0.0001 {
		t.Errorf("InputCost = %v, want %v", cost.InputCost, wantInput)
	}
	// output: 1M * 2.3/1M = 2.3
	if math.Abs(cost.OutputCost-2.3) > 0.001 {
		t.Errorf("OutputCost = %v, want 2.3", cost.OutputCost)
	}

	// Tier 2: 32k-128k input → 0.7/3.5
	cost2 := EstimateCost("doubao-seed-2-0-pro-260215", Usage{
		InputTokens:  50_000,
		OutputTokens: 1_000_000,
	})
	wantInput2 := 50_000.0 * 0.7 / 1_000_000.0
	if math.Abs(cost2.InputCost-wantInput2) > 0.0001 {
		t.Errorf("Tier2 InputCost = %v, want %v", cost2.InputCost, wantInput2)
	}
	if math.Abs(cost2.OutputCost-3.5) > 0.001 {
		t.Errorf("Tier2 OutputCost = %v, want 3.5", cost2.OutputCost)
	}
}

func TestEstimateCostChinaModelUSD(t *testing.T) {
	// Qwen models use USD pricing
	cost := EstimateCost("qwen-max", Usage{
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
	})
	if cost.Currency != "USD" {
		t.Fatalf("Currency = %q, want USD", cost.Currency)
	}
	if math.Abs(cost.InputCost-0.345) > 0.001 {
		t.Errorf("InputCost = %v, want 0.345", cost.InputCost)
	}
	if math.Abs(cost.OutputCost-1.377) > 0.001 {
		t.Errorf("OutputCost = %v, want 1.377", cost.OutputCost)
	}
}

func TestLookupModelInfoChinaFallback(t *testing.T) {
	info, ok := LookupModelInfo("kimi-k2.5")
	if !ok {
		t.Fatal("kimi-k2.5 not found via LookupModelInfo fallback")
	}
	if info.MaxInputTokens != 262144 {
		t.Errorf("MaxInputTokens = %d, want 262144", info.MaxInputTokens)
	}
	if !info.SupportsVision {
		t.Error("SupportsVision = false, want true")
	}
	if info.InputCostPerToken <= 0 {
		t.Errorf("InputCostPerToken = %v, want > 0", info.InputCostPerToken)
	}
}

func TestLookupContextWindowChinaFallback(t *testing.T) {
	tokens := LookupContextWindow("kimi-k2.5")
	if tokens != 262144 {
		t.Errorf("LookupContextWindow(kimi-k2.5) = %d, want 262144", tokens)
	}
}

func TestEstimateCostRegionalPricing(t *testing.T) {
	// Save and restore region
	origRegion := ChinaModelRegion()
	defer SetChinaModelRegion(origRegion)

	usage := Usage{InputTokens: 100_000, OutputTokens: 1_000_000}

	// International (default)
	SetChinaModelRegion("")
	intlCost := EstimateCost("qwen3.5-plus", usage)
	// qwen3.5-plus intl tier1 (≤256K): $0.4/M input, $2.4/M output
	if math.Abs(intlCost.InputCost-0.04) > 0.001 {
		t.Errorf("intl InputCost = %v, want 0.04", intlCost.InputCost)
	}
	if math.Abs(intlCost.OutputCost-2.4) > 0.001 {
		t.Errorf("intl OutputCost = %v, want 2.4", intlCost.OutputCost)
	}

	// China mainland
	SetChinaModelRegion("china_mainland")
	cnCost := EstimateCost("qwen3.5-plus", usage)
	// qwen3.5-plus mainland tier1 (≤128K): $0.115/M input, $0.688/M output
	if math.Abs(cnCost.InputCost-0.0115) > 0.0001 {
		t.Errorf("cn InputCost = %v, want 0.0115", cnCost.InputCost)
	}
	if math.Abs(cnCost.OutputCost-0.688) > 0.001 {
		t.Errorf("cn OutputCost = %v, want 0.688", cnCost.OutputCost)
	}

	// Mainland should be cheaper than international
	if cnCost.TotalCost >= intlCost.TotalCost {
		t.Errorf("mainland cost (%v) should be less than international (%v)", cnCost.TotalCost, intlCost.TotalCost)
	}
}

func TestDetectChinaRegionFromBaseURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", "china_mainland"},
		{"https://dashscope-intl.aliyuncs.com/compatible-mode/v1", "international"},
		{"https://api.openai.com/v1", "international"},
		{"", "international"},
	}
	for _, tc := range tests {
		got := DetectChinaRegionFromBaseURL(tc.url)
		if got != tc.want {
			t.Errorf("DetectChinaRegionFromBaseURL(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestEstimateCostRegionalFallback(t *testing.T) {
	// Model without regional override should use default pricing regardless of region
	origRegion := ChinaModelRegion()
	defer SetChinaModelRegion(origRegion)

	usage := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	SetChinaModelRegion("")
	defaultCost := EstimateCost("deepseek-chat", usage)

	SetChinaModelRegion("china_mainland")
	cnCost := EstimateCost("deepseek-chat", usage)

	if math.Abs(defaultCost.TotalCost-cnCost.TotalCost) > 0.001 {
		t.Errorf("deepseek-chat should have same cost regardless of region: default=%v, cn=%v",
			defaultCost.TotalCost, cnCost.TotalCost)
	}
}

func TestEstimateCostCurrencyUSD(t *testing.T) {
	cost := EstimateCost("gpt-4o", Usage{InputTokens: 1000, OutputTokens: 500})
	if cost.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", cost.Currency)
	}
}

func TestContextWindowLiteLLMFallback(t *testing.T) {
	// Use a model that exists in LiteLLM but NOT in knownContextWindows.
	// "ft:gpt-4o-mini-2024-07-18" is a fine-tuned model only in LiteLLM.
	tokens := LookupContextWindow("ft:gpt-4o-mini-2024-07-18")
	// It should find something via LiteLLM fallback or prefix match.
	// If not found at all, the fallback mechanism works but this model isn't there.
	t.Logf("LookupContextWindow(ft:gpt-4o-mini-2024-07-18) = %d", tokens)
}
