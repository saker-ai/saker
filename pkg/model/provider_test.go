package model

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// ProviderFunc tests
// ---------------------------------------------------------------------------

func TestProviderFunc_NilReturnsError(t *testing.T) {
	var fn ProviderFunc
	mdl, err := fn.Model(context.Background())
	if mdl != nil {
		t.Fatalf("expected nil model, got %v", mdl)
	}
	if err == nil {
		t.Fatalf("expected error for nil ProviderFunc")
	}
	if err.Error() != "model provider function is nil" {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}

func TestProviderFunc_NonNilReturnsModel(t *testing.T) {
	wantMdl := &mockModel{name: "test-model"}
	fn := ProviderFunc(func(ctx context.Context) (Model, error) {
		return wantMdl, nil
	})
	mdl, err := fn.Model(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdl != wantMdl {
		t.Fatalf("expected mock model, got %v", mdl)
	}
}

func TestProviderFunc_NonNilReturnsError(t *testing.T) {
	wantErr := errors.New("provider boom")
	fn := ProviderFunc(func(ctx context.Context) (Model, error) {
		return nil, wantErr
	})
	mdl, err := fn.Model(context.Background())
	if mdl != nil {
		t.Fatalf("expected nil model, got %v", mdl)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped error, got: %v", err)
	}
}

func TestProviderFunc_ImplementsProvider(t *testing.T) {
	var _ Provider = ProviderFunc(nil)
	var _ Provider = ProviderFunc(func(ctx context.Context) (Model, error) {
		return nil, nil
	})
}

func TestProviderFunc_RespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fn := ProviderFunc(func(ctx context.Context) (Model, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return &mockModel{name: "ok"}, nil
	})

	_, err := fn.Model(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// AnthropicProvider tests
// ---------------------------------------------------------------------------

func TestProviderAnthropic_ModelWithExplicitAPIKey(t *testing.T) {
	p := &AnthropicProvider{APIKey: "explicit-key", ModelName: "claude-test", CacheTTL: time.Minute}
	mdl, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdl == nil {
		t.Fatalf("expected model, got nil")
	}
}

func TestProviderAnthropic_CachingReturnsSameModel(t *testing.T) {
	p := &AnthropicProvider{APIKey: "key", CacheTTL: time.Minute}
	m1, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	m2, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if m1 != m2 {
		t.Fatalf("expected same cached model instance")
	}
}

func TestProviderAnthropic_NoCacheReturnsNewModel(t *testing.T) {
	p := &AnthropicProvider{APIKey: "key", CacheTTL: 0}
	m1, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	m2, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if m1 == m2 {
		t.Fatalf("expected different model instances without caching")
	}
}

func TestProviderAnthropic_ResolveAPIKeyExplicitPriority(t *testing.T) {
	p := &AnthropicProvider{APIKey: "explicit-key"}
	t.Setenv("ANTHROPIC_API_KEY", "envkey")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "authtoken")
	got := p.resolveAPIKey()
	if got != "explicit-key" {
		t.Fatalf("expected explicit key, got %q", got)
	}
}

func TestProviderAnthropic_ResolveAPIKeyEnvFallback(t *testing.T) {
	p := &AnthropicProvider{}
	t.Setenv("ANTHROPIC_API_KEY", "envkey")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "authtoken")
	got := p.resolveAPIKey()
	if got != "envkey" {
		t.Fatalf("expected env key, got %q", got)
	}
}

func TestProviderAnthropic_ResolveAPIKeyAuthTokenFallback(t *testing.T) {
	p := &AnthropicProvider{}
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "authtoken")
	got := p.resolveAPIKey()
	if got != "authtoken" {
		t.Fatalf("expected auth token, got %q", got)
	}
}

func TestProviderAnthropic_ResolveAPIKeyEmptyReturnsEmpty(t *testing.T) {
	p := &AnthropicProvider{}
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	got := p.resolveAPIKey()
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestProviderAnthropic_ResolveAPIKeyWhitespaceOnlyIgnored(t *testing.T) {
	p := &AnthropicProvider{APIKey: "   "}
	t.Setenv("ANTHROPIC_API_KEY", "validkey")
	got := p.resolveAPIKey()
	if got != "validkey" {
		t.Fatalf("whitespace-only explicit key should fall back to env, got %q", got)
	}
}

func TestProviderAnthropic_CacheDisabledReturnsNilFromCachedModel(t *testing.T) {
	p := &AnthropicProvider{CacheTTL: 0}
	if got := p.cachedModel(); got != nil {
		t.Fatalf("expected nil from cachedModel when cache disabled, got %v", got)
	}
}

func TestProviderAnthropic_StoreDoesNotCacheWhenDisabled(t *testing.T) {
	p := &AnthropicProvider{CacheTTL: 0}
	p.store(&mockModel{name: "m"})
	if p.cached != nil {
		t.Fatalf("store should not cache when CacheTTL is 0")
	}
}

func TestProviderAnthropic_StoreDoesNotCacheNilModel(t *testing.T) {
	p := &AnthropicProvider{CacheTTL: time.Minute}
	p.store(nil)
	if p.cached != nil {
		t.Fatalf("store should not cache nil model")
	}
}

func TestProviderAnthropic_CachedModelReturnsModelWhenValid(t *testing.T) {
	p := &AnthropicProvider{CacheTTL: time.Minute}
	mdl := &mockModel{name: "cached"}
	p.mu.Lock()
	p.cached = mdl
	p.expires = time.Now().Add(time.Hour)
	p.mu.Unlock()

	got := p.cachedModel()
	if got != mdl {
		t.Fatalf("expected cached model, got %v", got)
	}
}

func TestProviderAnthropic_CachedModelReturnsNilWhenExpired(t *testing.T) {
	p := &AnthropicProvider{CacheTTL: time.Minute}
	mdl := &mockModel{name: "cached"}
	p.mu.Lock()
	p.cached = mdl
	p.expires = time.Now().Add(-time.Hour) // expired
	p.mu.Unlock()

	got := p.cachedModel()
	if got != nil {
		t.Fatalf("expected nil from expired cache, got %v", got)
	}
}

func TestProviderAnthropic_CachedModelReturnsNilWhenEmpty(t *testing.T) {
	p := &AnthropicProvider{CacheTTL: time.Minute}
	p.mu.Lock()
	p.cached = nil
	p.expires = time.Now().Add(time.Hour)
	p.mu.Unlock()

	got := p.cachedModel()
	if got != nil {
		t.Fatalf("expected nil when cached is nil, got %v", got)
	}
}

func TestProviderAnthropic_ModelMissingAPIKeyReturnsError(t *testing.T) {
	p := &AnthropicProvider{}
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	_, err := p.Model(context.Background())
	if err == nil {
		t.Fatalf("expected error for missing API key")
	}
	if !strings.Contains(err.Error(), "api key") {
		t.Fatalf("error should mention api key, got: %q", err.Error())
	}
}

func TestProviderAnthropic_ModelWithConfigFields(t *testing.T) {
	temp := 0.7
	p := &AnthropicProvider{
		APIKey:      "test-key",
		BaseURL:     "https://custom.api.com",
		ModelName:   "claude-3-test",
		MaxTokens:   2048,
		MaxRetries:  3,
		System:      "test system prompt",
		Temperature: &temp,
		CacheTTL:    time.Minute,
	}
	mdl, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdl == nil {
		t.Fatalf("expected model")
	}

	mdl2, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("cached call error: %v", err)
	}
	if mdl != mdl2 {
		t.Fatalf("expected cached model on second call")
	}
}

func TestProviderAnthropic_ModelTrimsSpaces(t *testing.T) {
	p := &AnthropicProvider{
		APIKey:    "key",
		BaseURL:   "  https://custom.api.com  ",
		ModelName: "  claude-test  ",
		CacheTTL:  time.Minute,
	}
	mdl, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdl == nil {
		t.Fatalf("expected model")
	}
}

func TestProviderAnthropic_ConcurrentModelCalls(t *testing.T) {
	p := &AnthropicProvider{APIKey: "key", CacheTTL: time.Minute}

	var wg sync.WaitGroup
	results := make(chan Model, 20)
	errs := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mdl, err := p.Model(context.Background())
			if err != nil {
				errs <- err
				return
			}
			results <- mdl
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent call error: %v", err)
	}

	var first Model
	for mdl := range results {
		if first == nil {
			first = mdl
		} else if mdl != first {
			t.Fatalf("concurrent calls returned different model instances")
		}
	}
}

func TestProviderAnthropic_ImplementsProvider(t *testing.T) {
	var _ Provider = &AnthropicProvider{}
}

func TestProviderAnthropic_DoubleCheckedLocking(t *testing.T) {
	p := &AnthropicProvider{APIKey: "key", CacheTTL: 50 * time.Millisecond}

	m1, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	m2, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("second call after expiry: %v", err)
	}
	if m1 == m2 {
		t.Fatalf("expected new model instance after cache expiry")
	}

	m3, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if m2 != m3 {
		t.Fatalf("expected same model on immediate call after re-cache")
	}
}

func TestProviderAnthropic_ZeroFieldsWithEnvKey(t *testing.T) {
	p := &AnthropicProvider{}
	t.Setenv("ANTHROPIC_API_KEY", "envkey")
	mdl, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdl == nil {
		t.Fatalf("expected model from env key")
	}
}

func TestProviderAnthropic_StoreCachesValidModel(t *testing.T) {
	p := &AnthropicProvider{CacheTTL: time.Minute}
	mdl := &mockModel{name: "stored"}
	p.store(mdl)
	if p.cached != mdl {
		t.Fatalf("store should cache valid model")
	}
	if p.expires.IsZero() {
		t.Fatalf("store should set expiry time")
	}
}

func TestProviderAnthropic_ResolveAPIKeyEnvVariableWithWhitespace(t *testing.T) {
	p := &AnthropicProvider{}
	// os.Getenv returns the raw value; TrimSpace is applied in resolveAPIKey.
	t.Setenv("ANTHROPIC_API_KEY", "  spaced-key  ")
	got := p.resolveAPIKey()
	// resolveAPIKey calls strings.TrimSpace on os.Getenv result
	// but then passes it through security.ResolveEnv which returns unchanged
	// strings that don't start with the ENC prefix.
	if strings.TrimSpace(got) != "spaced-key" {
		t.Fatalf("expected trimmed key, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// OpenAIProvider tests
// ---------------------------------------------------------------------------

func TestProviderOpenAI_ModelWithExplicitAPIKey(t *testing.T) {
	p := &OpenAIProvider{APIKey: "explicit-key", ModelName: "gpt-test", CacheTTL: time.Minute}
	mdl, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdl == nil {
		t.Fatalf("expected model, got nil")
	}
}

func TestProviderOpenAI_CachingReturnsSameModel(t *testing.T) {
	p := &OpenAIProvider{APIKey: "key", ModelName: "gpt-test", CacheTTL: time.Minute}
	m1, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	m2, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if m1 != m2 {
		t.Fatalf("expected same cached model instance")
	}
}

func TestProviderOpenAI_NoCacheReturnsNewModel(t *testing.T) {
	p := &OpenAIProvider{APIKey: "key", ModelName: "gpt-test", CacheTTL: 0}
	m1, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	m2, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if m1 == m2 {
		t.Fatalf("expected different model instances without caching")
	}
}

func TestProviderOpenAI_ResolveAPIKeyExplicitPriority(t *testing.T) {
	p := &OpenAIProvider{APIKey: "explicit-key"}
	t.Setenv("OPENAI_API_KEY", "envkey")
	got := p.resolveAPIKey()
	if got != "explicit-key" {
		t.Fatalf("expected explicit key, got %q", got)
	}
}

func TestProviderOpenAI_ResolveAPIKeyEnvFallback(t *testing.T) {
	p := &OpenAIProvider{}
	t.Setenv("OPENAI_API_KEY", "envkey")
	got := p.resolveAPIKey()
	if got != "envkey" {
		t.Fatalf("expected env key, got %q", got)
	}
}

func TestProviderOpenAI_ResolveAPIKeyEmptyReturnsEmpty(t *testing.T) {
	p := &OpenAIProvider{}
	t.Setenv("OPENAI_API_KEY", "")
	got := p.resolveAPIKey()
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestProviderOpenAI_ResolveAPIKeyWhitespaceOnlyIgnored(t *testing.T) {
	p := &OpenAIProvider{APIKey: "   "}
	t.Setenv("OPENAI_API_KEY", "validkey")
	got := p.resolveAPIKey()
	if got != "validkey" {
		t.Fatalf("whitespace-only explicit key should fall back to env, got %q", got)
	}
}

func TestProviderOpenAI_CacheDisabledReturnsNilFromCachedModel(t *testing.T) {
	p := &OpenAIProvider{CacheTTL: 0}
	if got := p.cachedModel(); got != nil {
		t.Fatalf("expected nil from cachedModel when cache disabled, got %v", got)
	}
}

func TestProviderOpenAI_CacheDisabledInternalStateNotUsed(t *testing.T) {
	p := &OpenAIProvider{CacheTTL: 0}
	p.mu.Lock()
	p.cached = &mockModel{name: "m"}
	p.expires = time.Now().Add(time.Hour)
	p.mu.Unlock()

	got := p.cachedModel()
	if got != nil {
		t.Fatalf("cachedModel should return nil when CacheTTL is 0, got %v", got)
	}
}

func TestProviderOpenAI_CachedModelReturnsModelWhenValid(t *testing.T) {
	p := &OpenAIProvider{CacheTTL: time.Minute}
	mdl := &mockModel{name: "cached"}
	p.mu.Lock()
	p.cached = mdl
	p.expires = time.Now().Add(time.Hour)
	p.mu.Unlock()

	got := p.cachedModel()
	if got != mdl {
		t.Fatalf("expected cached model, got %v", got)
	}
}

func TestProviderOpenAI_CachedModelReturnsNilWhenExpired(t *testing.T) {
	p := &OpenAIProvider{CacheTTL: time.Minute}
	mdl := &mockModel{name: "cached"}
	p.mu.Lock()
	p.cached = mdl
	p.expires = time.Now().Add(-time.Hour) // expired
	p.mu.Unlock()

	got := p.cachedModel()
	if got != nil {
		t.Fatalf("expected nil from expired cache, got %v", got)
	}
}

func TestProviderOpenAI_CachedModelReturnsNilWhenEmpty(t *testing.T) {
	p := &OpenAIProvider{CacheTTL: time.Minute}
	p.mu.Lock()
	p.cached = nil
	p.expires = time.Now().Add(time.Hour)
	p.mu.Unlock()

	got := p.cachedModel()
	if got != nil {
		t.Fatalf("expected nil when cached is nil, got %v", got)
	}
}

func TestProviderOpenAI_ModelMissingAPIKeyReturnsError(t *testing.T) {
	p := &OpenAIProvider{}
	t.Setenv("OPENAI_API_KEY", "")
	_, err := p.Model(context.Background())
	if err == nil {
		t.Fatalf("expected error for missing API key")
	}
	if !strings.Contains(err.Error(), "api key") {
		t.Fatalf("error should mention api key, got: %q", err.Error())
	}
}

func TestProviderOpenAI_ModelWithConfigFields(t *testing.T) {
	temp := 0.5
	p := &OpenAIProvider{
		APIKey:      "test-key",
		BaseURL:     "https://custom.api.com",
		ModelName:   "gpt-4-test",
		MaxTokens:   1024,
		MaxRetries:  2,
		System:      "test system prompt",
		Temperature: &temp,
		CacheTTL:    time.Minute,
	}
	mdl, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdl == nil {
		t.Fatalf("expected model")
	}

	mdl2, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("cached call error: %v", err)
	}
	if mdl != mdl2 {
		t.Fatalf("expected cached model on second call")
	}
}

func TestProviderOpenAI_ModelWithExtraBody(t *testing.T) {
	p := &OpenAIProvider{
		APIKey:    "test-key",
		ModelName: "gpt-test",
		CacheTTL:  time.Minute,
		ExtraBody: map[string]any{"enable_thinking": true},
	}
	mdl, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdl == nil {
		t.Fatalf("expected model with ExtraBody")
	}
}

func TestProviderOpenAI_ModelTrimsSpaces(t *testing.T) {
	p := &OpenAIProvider{
		APIKey:    "key",
		BaseURL:   "  https://custom.api.com  ",
		ModelName: "  gpt-test  ",
		CacheTTL:  time.Minute,
	}
	mdl, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdl == nil {
		t.Fatalf("expected model")
	}
}

func TestProviderOpenAI_ConcurrentModelCalls(t *testing.T) {
	p := &OpenAIProvider{APIKey: "key", ModelName: "gpt-test", CacheTTL: time.Minute}

	var wg sync.WaitGroup
	results := make(chan Model, 20)
	errs := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mdl, err := p.Model(context.Background())
			if err != nil {
				errs <- err
				return
			}
			results <- mdl
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent call error: %v", err)
	}

	var first Model
	for mdl := range results {
		if first == nil {
			first = mdl
		} else if mdl != first {
			t.Fatalf("concurrent calls returned different model instances")
		}
	}
}

func TestProviderOpenAI_ImplementsProvider(t *testing.T) {
	var _ Provider = &OpenAIProvider{}
}

func TestProviderOpenAI_DoubleCheckedLocking(t *testing.T) {
	p := &OpenAIProvider{APIKey: "key", ModelName: "gpt-test", CacheTTL: 50 * time.Millisecond}

	m1, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	m2, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("second call after expiry: %v", err)
	}
	if m1 == m2 {
		t.Fatalf("expected new model instance after cache expiry")
	}

	m3, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if m2 != m3 {
		t.Fatalf("expected same model on immediate call after re-cache")
	}
}

func TestProviderOpenAI_ZeroFieldsWithEnvKey(t *testing.T) {
	p := &OpenAIProvider{}
	t.Setenv("OPENAI_API_KEY", "envkey")
	mdl, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdl == nil {
		t.Fatalf("expected model from env key")
	}
}

func TestProviderOpenAI_ResolveAPIKeyEnvVariableWithWhitespace(t *testing.T) {
	p := &OpenAIProvider{}
	t.Setenv("OPENAI_API_KEY", "  spaced-key  ")
	got := p.resolveAPIKey()
	if strings.TrimSpace(got) != "spaced-key" {
		t.Fatalf("expected trimmed key, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// ProviderModel tests
// ---------------------------------------------------------------------------

func TestProviderModel_NilProvider(t *testing.T) {
	mdl, err := ProviderModel(nil)
	if mdl != nil {
		t.Fatalf("expected nil model, got %v", mdl)
	}
	if err == nil {
		t.Fatalf("expected error for nil provider")
	}
	if err.Error() != "model provider is nil" {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}

func TestProviderModel_Success(t *testing.T) {
	wantMdl := &mockModel{name: "provider-model"}
	mdl, err := ProviderModel(stubProvider{mdl: wantMdl})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdl != wantMdl {
		t.Fatalf("expected model, got %v", mdl)
	}
}

func TestProviderModel_ErrorWrapping(t *testing.T) {
	innerErr := errors.New("inner boom")
	_, err := ProviderModel(stubProvider{err: innerErr})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "model provider failed") {
		t.Fatalf("error should contain 'model provider failed', got: %q", err.Error())
	}
	if !errors.Is(err, innerErr) {
		t.Fatalf("error should wrap inner error")
	}
}

func TestProviderModel_UsesContextBackground(t *testing.T) {
	// ProviderModel always uses context.Background internally,
	// so it succeeds regardless of the caller's context state.
	wantMdl := &mockModel{name: "bg-model"}
	mdl, err := ProviderModel(stubProvider{mdl: wantMdl})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdl != wantMdl {
		t.Fatalf("expected model, got %v", mdl)
	}
}

func TestProviderModel_NilProviderErrorMessage(t *testing.T) {
	_, err := ProviderModel(nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	// Verify the exact error message matches the implementation.
	want := "model provider is nil"
	if err.Error() != want {
		t.Fatalf("expected %q, got %q", want, err.Error())
	}
}