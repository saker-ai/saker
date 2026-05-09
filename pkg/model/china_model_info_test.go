package model

import "testing"

func TestChinaModelCatalogLoaded(t *testing.T) {
	count := ChinaModelCount()
	if count < 10 {
		t.Fatalf("ChinaModelCount() = %d, want at least 10", count)
	}
}

func TestLookupChinaModelInfoByName(t *testing.T) {
	info, ok := LookupChinaModelInfo("kimi-k2.5")
	if !ok {
		t.Fatal("kimi-k2.5 not found in China model catalog")
	}
	if info.Vendor != "Moonshot AI" {
		t.Fatalf("Vendor = %q, want %q", info.Vendor, "Moonshot AI")
	}
	if info.Pricing.Currency != "CNY" {
		t.Fatalf("Pricing.Currency = %q, want %q", info.Pricing.Currency, "CNY")
	}
	if info.Pricing.Default.InputPerMillion <= 0 {
		t.Fatalf("InputPerMillion = %v, want > 0", info.Pricing.Default.InputPerMillion)
	}
	if info.SourceURL == "" {
		t.Fatal("SourceURL is empty")
	}
}

func TestLookupChinaModelInfoByAlias(t *testing.T) {
	info, ok := LookupChinaModelInfo("MiniMax-M2.5")
	if !ok {
		t.Fatal("MiniMax-M2.5 not found via alias lookup")
	}
	if info.Vendor != "MiniMax" {
		t.Fatalf("Vendor = %q, want %q", info.Vendor, "MiniMax")
	}
	if info.Pricing.Default.CacheReadPerMillion <= 0 {
		t.Fatalf("CacheReadPerMillion = %v, want > 0", info.Pricing.Default.CacheReadPerMillion)
	}
}

func TestLookupChinaModelInfoTieredPricing(t *testing.T) {
	info, ok := LookupChinaModelInfo("qwen-plus")
	if !ok {
		t.Fatal("qwen-plus not found in China model catalog")
	}
	if len(info.Pricing.Tiers) == 0 {
		t.Fatal("qwen-plus should have tiered pricing")
	}
	if info.Pricing.Tiers[0].InputPerMillion <= 0 {
		t.Fatalf("first tier InputPerMillion = %v, want > 0", info.Pricing.Tiers[0].InputPerMillion)
	}
}
