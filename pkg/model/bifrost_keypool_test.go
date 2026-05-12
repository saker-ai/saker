// bifrost_keypool_test.go: covers the multi-key pool plumbing introduced for
// settings.Failover.PrimaryKeyPool — verifies that buildPrimaryKeys produces
// the expected schemas.Key slice (weights, model whitelists, typed configs)
// and that GetKeysForProvider returns a defensive copy callers can mutate
// without disturbing the registered set.
package model

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestBuildPrimaryKeys_SingleKeyDefault(t *testing.T) {
	keys := buildPrimaryKeys("primary-token", BifrostConfig{})
	if len(keys) != 1 {
		t.Fatalf("expected 1 key (no AdditionalKeys), got %d", len(keys))
	}
	if keys[0].Value.Val != "primary-token" {
		t.Errorf("expected primary token, got %q", keys[0].Value.Val)
	}
	if keys[0].Weight != 1 {
		t.Errorf("expected default weight 1, got %v", keys[0].Weight)
	}
	if keys[0].ID != "saker-default" {
		t.Errorf("expected ID saker-default, got %q", keys[0].ID)
	}
	if len(keys[0].Models) != 0 {
		t.Errorf("expected empty WhiteList for primary, got %v", keys[0].Models)
	}
}

func TestBuildPrimaryKeys_MultiKey(t *testing.T) {
	cfg := BifrostConfig{
		AdditionalKeys: []ProviderKeySpec{
			{APIKey: "k2", Weight: 2.5, Models: []string{"claude-sonnet-4-20250514"}},
			{APIKey: "k3"}, // weight 0 → defaults to 1
			{APIKey: ""},   // empty → skipped
		},
	}
	keys := buildPrimaryKeys("primary", cfg)
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys (primary + 2 valid extras), got %d", len(keys))
	}
	if keys[0].Value.Val != "primary" {
		t.Errorf("position 0 must be primary, got %q", keys[0].Value.Val)
	}
	if keys[1].Value.Val != "k2" || keys[1].Weight != 2.5 {
		t.Errorf("k2 mismatch: value=%q weight=%v", keys[1].Value.Val, keys[1].Weight)
	}
	if got := []string(keys[1].Models); len(got) != 1 || got[0] != "claude-sonnet-4-20250514" {
		t.Errorf("k2 whitelist mismatch: %v", got)
	}
	if keys[2].Value.Val != "k3" || keys[2].Weight != 1 {
		t.Errorf("k3 fallback weight mismatch: value=%q weight=%v", keys[2].Value.Val, keys[2].Weight)
	}
	if keys[1].ID == keys[2].ID {
		t.Errorf("expected unique IDs for siblings, got duplicate %q", keys[1].ID)
	}
}

func TestBuildPrimaryKeys_TypedConfigOnPrimaryOnly(t *testing.T) {
	region := schemas.EnvVar{Val: "us-east-1"}
	cfg := BifrostConfig{
		BedrockKeyConfig: &schemas.BedrockKeyConfig{Region: &region},
		AdditionalKeys: []ProviderKeySpec{
			{APIKey: "extra"},
		},
	}
	keys := buildPrimaryKeys("primary", cfg)
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if keys[0].BedrockKeyConfig == nil {
		t.Errorf("typed config missing on primary key")
	}
	if keys[1].BedrockKeyConfig != nil {
		t.Errorf("typed config should not propagate to sibling key")
	}
}

func TestGetKeysForProvider_DefensiveCopy(t *testing.T) {
	account := &bifrostAccount{
		providers: map[schemas.ModelProvider]*providerEntry{
			schemas.Anthropic: {
				keys: []schemas.Key{
					{ID: "a", Value: schemas.EnvVar{Val: "v1"}, Weight: 1},
					{ID: "b", Value: schemas.EnvVar{Val: "v2"}, Weight: 1},
				},
			},
		},
	}
	got, err := account.GetKeysForProvider(context.Background(), schemas.Anthropic)
	if err != nil {
		t.Fatalf("GetKeysForProvider: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(got))
	}
	// Mutate the returned slice — registered keys must remain intact.
	got[0].Value.Val = "tampered"
	if account.providers[schemas.Anthropic].keys[0].Value.Val != "v1" {
		t.Errorf("registered key mutated by caller; expected defensive copy")
	}
}

