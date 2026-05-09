package apps

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateAPIKey_RoundTrip(t *testing.T) {
	t.Parallel()
	plaintext, hash, prefix, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if !strings.HasPrefix(plaintext, "ak_") {
		t.Fatalf("plaintext must start with ak_, got %q", plaintext)
	}
	if len(plaintext) != 35 { // "ak_" + 32 hex chars
		t.Fatalf("plaintext length: got %d, want 35", len(plaintext))
	}
	if prefix != plaintext[:8] {
		t.Fatalf("prefix mismatch: got %q, want %q", prefix, plaintext[:8])
	}
	keys := &KeysFile{
		ApiKeys: []ApiKey{{ID: "1", Hash: hash, Prefix: prefix}},
	}
	got, ok := ValidateAPIKey(keys, plaintext)
	if !ok || got == nil {
		t.Fatal("ValidateAPIKey: expected match")
	}
}

func TestValidateAPIKey_BearerPrefix(t *testing.T) {
	t.Parallel()
	plaintext, hash, prefix, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	keys := &KeysFile{
		ApiKeys: []ApiKey{{ID: "1", Hash: hash, Prefix: prefix}},
	}
	cases := []string{
		plaintext,
		"Bearer " + plaintext,
		"bearer " + plaintext,
		"BEARER " + plaintext,
		"Bearer  " + plaintext, // extra space, TrimSpace handles it
	}
	for _, hdr := range cases {
		got, ok := ValidateAPIKey(keys, hdr)
		if !ok || got == nil {
			t.Errorf("expected match for header %q", hdr)
		}
	}
}

func TestValidateAPIKey_Mismatch(t *testing.T) {
	t.Parallel()
	_, hash, prefix, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	keys := &KeysFile{
		ApiKeys: []ApiKey{{ID: "1", Hash: hash, Prefix: prefix}},
	}
	got, ok := ValidateAPIKey(keys, "ak_wrongkeyxxxxxxxxxxxxxxxxxxxxxxxx")
	if ok || got != nil {
		t.Fatal("expected no match for wrong key")
	}
}

func TestValidateAPIKey_LastUsedAt(t *testing.T) {
	t.Parallel()
	plaintext, hash, prefix, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	keys := &KeysFile{
		ApiKeys: []ApiKey{{ID: "1", Hash: hash, Prefix: prefix}},
	}
	before := time.Now()
	got, ok := ValidateAPIKey(keys, plaintext)
	after := time.Now()
	if !ok || got == nil {
		t.Fatal("expected match")
	}
	if got.LastUsedAt == nil {
		t.Fatal("LastUsedAt not set")
	}
	if got.LastUsedAt.Before(before) || got.LastUsedAt.After(after) {
		t.Fatalf("LastUsedAt %v out of range [%v, %v]", *got.LastUsedAt, before, after)
	}
}

func TestValidateAPIKey_Expired(t *testing.T) {
	t.Parallel()
	plaintext, hash, prefix, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	keys := &KeysFile{
		ApiKeys: []ApiKey{{ID: "1", Hash: hash, Prefix: prefix, ExpiresAt: &past}},
	}
	got, ok := ValidateAPIKey(keys, plaintext)
	if ok || got != nil {
		t.Fatal("expected expired key to be rejected")
	}
	// Bcrypt must be skipped entirely so LastUsedAt is not touched.
	if keys.ApiKeys[0].LastUsedAt != nil {
		t.Fatal("LastUsedAt must remain nil for expired key")
	}
}

func TestValidateAPIKey_FutureExpiry(t *testing.T) {
	t.Parallel()
	plaintext, hash, prefix, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	future := time.Now().Add(time.Hour)
	keys := &KeysFile{
		ApiKeys: []ApiKey{{ID: "1", Hash: hash, Prefix: prefix, ExpiresAt: &future}},
	}
	got, ok := ValidateAPIKey(keys, plaintext)
	if !ok || got == nil {
		t.Fatal("expected key with future expiry to validate")
	}
}

func TestValidateAPIKey_NilExpiryNeverExpires(t *testing.T) {
	t.Parallel()
	plaintext, hash, prefix, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	keys := &KeysFile{
		ApiKeys: []ApiKey{{ID: "1", Hash: hash, Prefix: prefix, ExpiresAt: nil}},
	}
	got, ok := ValidateAPIKey(keys, plaintext)
	if !ok || got == nil {
		t.Fatal("expected key with nil expiry to validate")
	}
}

func TestGenerateShareToken_Length(t *testing.T) {
	t.Parallel()
	tok, err := GenerateShareToken()
	if err != nil {
		t.Fatalf("GenerateShareToken: %v", err)
	}
	// base64url no-padding of 24 bytes = 32 chars
	if len(tok) != 32 {
		t.Fatalf("token length: got %d, want 32", len(tok))
	}
}

func TestValidateShareToken_Expired(t *testing.T) {
	t.Parallel()
	tok, err := GenerateShareToken()
	if err != nil {
		t.Fatalf("GenerateShareToken: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	keys := &KeysFile{
		ShareTokens: []ShareToken{{Token: tok, CreatedAt: time.Now(), ExpiresAt: &past}},
	}
	got, ok := ValidateShareToken(keys, tok)
	if ok || got != nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestValidateShareToken_RateLimit(t *testing.T) {
	t.Parallel()
	tok, err := GenerateShareToken()
	if err != nil {
		t.Fatalf("GenerateShareToken: %v", err)
	}
	// Use a unique token per test run so the package-level sync.Map doesn't
	// carry state from other parallel tests.
	keys := &KeysFile{
		ShareTokens: []ShareToken{{Token: tok, CreatedAt: time.Now(), RateLimit: 2}},
	}
	// First two calls should succeed.
	if _, ok := ValidateShareToken(keys, tok); !ok {
		t.Fatal("call 1: expected ok")
	}
	if _, ok := ValidateShareToken(keys, tok); !ok {
		t.Fatal("call 2: expected ok")
	}
	// Third call within the same second must be rate-limited.
	if _, ok := ValidateShareToken(keys, tok); ok {
		t.Fatal("call 3: expected rate limit rejection")
	}
	// Manually drain the bucket by resetting its hits to the past so the
	// window slides. We reach into the sync.Map directly since there's no
	// exported reset API (this is an internal test).
	if v, loaded := defaultRateLimitMgr.limiters.Load(tok); loaded {
		bucket := v.(*tokenBucket)
		bucket.mu.Lock()
		past := time.Now().Add(-2 * time.Minute)
		for i := range bucket.hits {
			bucket.hits[i] = past
		}
		bucket.mu.Unlock()
	}
	// Should succeed again after the window cleared.
	if _, ok := ValidateShareToken(keys, tok); !ok {
		t.Fatal("call 4 after window cleared: expected ok")
	}
}

func TestValidateShareToken_NoRateLimit(t *testing.T) {
	t.Parallel()
	tok, err := GenerateShareToken()
	if err != nil {
		t.Fatalf("GenerateShareToken: %v", err)
	}
	keys := &KeysFile{
		ShareTokens: []ShareToken{{Token: tok, CreatedAt: time.Now(), RateLimit: 0}},
	}
	for i := range 10 {
		if _, ok := ValidateShareToken(keys, tok); !ok {
			t.Fatalf("call %d: expected ok with no rate limit", i+1)
		}
	}
}
