package model

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubProvider struct {
	mdl Model
	err error
}

func (s stubProvider) Model(context.Context) (Model, error) {
	return s.mdl, s.err
}

func TestProviderFuncNil(t *testing.T) {
	var fn ProviderFunc
	if _, err := fn.Model(context.Background()); err == nil {
		t.Fatalf("expected error for nil provider func")
	}
}

func TestAnthropicProviderCaching(t *testing.T) {
	p := &AnthropicProvider{APIKey: "key", ModelName: "claude-test", CacheTTL: time.Minute}
	m1, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	m2, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if m1 != m2 {
		t.Fatalf("expected cached model")
	}
}

func TestAnthropicProviderResolveAPIKey(t *testing.T) {
	p := &AnthropicProvider{}
	t.Setenv("ANTHROPIC_API_KEY", "envkey")
	if got := p.resolveAPIKey(); got != "envkey" {
		t.Fatalf("expected env key, got %q", got)
	}
}

func TestAnthropicProviderResolveAPIKeyPriority(t *testing.T) {
	p := &AnthropicProvider{APIKey: "explicit"}
	t.Setenv("ANTHROPIC_API_KEY", "envkey")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "auth")
	if got := p.resolveAPIKey(); got != "explicit" {
		t.Fatalf("expected explicit key, got %q", got)
	}

	p.APIKey = ""
	if got := p.resolveAPIKey(); got != "envkey" {
		t.Fatalf("expected env key, got %q", got)
	}

	t.Setenv("ANTHROPIC_API_KEY", "")
	if got := p.resolveAPIKey(); got != "auth" {
		t.Fatalf("expected auth token, got %q", got)
	}
}

func TestProviderModelNil(t *testing.T) {
	_, err := ProviderModel(nil)
	if err == nil {
		t.Fatalf("expected error for nil provider")
	}
}

func TestProviderModelSuccess(t *testing.T) {
	ok, err := ProviderModel(stubProvider{mdl: &bifrostModel{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok == nil {
		t.Fatalf("expected model")
	}
}

func TestAnthropicProviderCacheDisabled(t *testing.T) {
	p := &AnthropicProvider{CacheTTL: 0}
	if got := p.cachedModel(); got != nil {
		t.Fatalf("expected nil cached model when cache disabled")
	}
	p.store(&bifrostModel{})
	if got := p.cachedModel(); got != nil {
		t.Fatalf("expected nil cached model when cache disabled")
	}
}

func TestAnthropicProviderCacheExpiry(t *testing.T) {
	p := &AnthropicProvider{CacheTTL: time.Millisecond}
	p.store(&bifrostModel{})
	p.expires = time.Now().Add(-time.Minute)
	if got := p.cachedModel(); got != nil {
		t.Fatalf("expected expired cache to return nil")
	}
}

func TestAnthropicProviderModelMissingAPIKey(t *testing.T) {
	p := &AnthropicProvider{}
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	if _, err := p.Model(context.Background()); err == nil {
		t.Fatalf("expected error for missing api key")
	}
}

func TestProviderModelError(t *testing.T) {
	_, err := ProviderModel(stubProvider{err: errors.New("boom")})
	if err == nil {
		t.Fatalf("expected error on provider failure")
	}
}
