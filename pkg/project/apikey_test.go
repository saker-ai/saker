package project

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHashAPIKey_Stable(t *testing.T) {
	a := HashAPIKey("ak_abc")
	b := HashAPIKey("  ak_abc  ")
	if a != b {
		t.Errorf("HashAPIKey should trim whitespace before hashing: %q vs %q", a, b)
	}
	if len(a) != 64 {
		t.Errorf("hash length = %d, want 64 hex chars", len(a))
	}
}

func TestCreateAPIKey_RoundTrip(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()

	res, err := s.CreateAPIKey(ctx, "user-1", "proj-1", "ci-pipeline")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if !strings.HasPrefix(res.Plaintext, "ak_") {
		t.Errorf("plaintext should start with ak_, got %q", res.Plaintext)
	}
	if res.APIKey.Hash != HashAPIKey(res.Plaintext) {
		t.Errorf("stored hash != HashAPIKey(plaintext)")
	}
	if res.APIKey.Prefix == "" || len(res.APIKey.Prefix) != 8 {
		t.Errorf("prefix should be 8 hex chars, got %q", res.APIKey.Prefix)
	}
	if res.APIKey.UserID != "user-1" || res.APIKey.ProjectID != "proj-1" {
		t.Errorf("ownership fields wrong: %+v", res.APIKey)
	}
}

func TestCreateAPIKey_RequiresUser(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	if _, err := s.CreateAPIKey(context.Background(), "", "", ""); err == nil {
		t.Error("expected error when user id missing")
	}
}

func TestLookupAPIKey_FoundAndMissing(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()

	res, err := s.CreateAPIKey(ctx, "user-1", "proj-1", "ci")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.LookupAPIKey(ctx, res.Plaintext)
	if err != nil {
		t.Fatalf("Lookup ok key: %v", err)
	}
	if got.ID != res.APIKey.ID {
		t.Errorf("looked up wrong row: got %q want %q", got.ID, res.APIKey.ID)
	}

	if _, err := s.LookupAPIKey(ctx, "ak_deadbeefdeadbeef00000000"); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Errorf("missing key: got %v, want ErrAPIKeyNotFound", err)
	}
	if _, err := s.LookupAPIKey(ctx, ""); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Errorf("empty key: got %v, want ErrAPIKeyNotFound", err)
	}
	if _, err := s.LookupAPIKey(ctx, "not_an_ak_key"); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Errorf("bad-prefix key: got %v, want ErrAPIKeyNotFound", err)
	}
}

func TestLookupAPIKey_RevokedRejected(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()

	res, err := s.CreateAPIKey(ctx, "user-1", "", "ci")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.RevokeAPIKey(ctx, res.APIKey.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.LookupAPIKey(ctx, res.Plaintext); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Errorf("expected revoked key to be rejected, got %v", err)
	}
}

func TestRevokeAPIKey_Idempotent(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()
	res, err := s.CreateAPIKey(ctx, "user-1", "", "ci")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.RevokeAPIKey(ctx, res.APIKey.ID); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	if err := s.RevokeAPIKey(ctx, res.APIKey.ID); err != nil {
		t.Fatalf("second revoke must not error: %v", err)
	}
}

func TestTouchAPIKey_UpdatesLastSeen(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()
	res, err := s.CreateAPIKey(ctx, "user-1", "", "ci")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.APIKey.LastUsedAt != nil {
		t.Error("expected LastUsedAt nil at create time")
	}
	s.TouchAPIKey(ctx, res.APIKey.ID)
	// Re-fetch via list
	rows, err := s.ListAPIKeys(ctx, "user-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].LastUsedAt == nil {
		t.Errorf("expected LastUsedAt to be set, got %+v", rows)
	}
	if time.Since(*rows[0].LastUsedAt) > 5*time.Second {
		t.Errorf("LastUsedAt unexpectedly old: %s", *rows[0].LastUsedAt)
	}
}

func TestListAPIKeys_NewestFirst(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()
	r1, _ := s.CreateAPIKey(ctx, "u", "", "k1")
	time.Sleep(10 * time.Millisecond)
	r2, _ := s.CreateAPIKey(ctx, "u", "", "k2")
	rows, err := s.ListAPIKeys(ctx, "u")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].ID != r2.APIKey.ID || rows[1].ID != r1.APIKey.ID {
		t.Errorf("expected newest first, got [%s, %s]", rows[0].Name, rows[1].Name)
	}
}

// TestLookupAPIKey_PrefixCollisionsResolveByHash forces two keys to share
// the same Prefix bucket and confirms the constant-time hash compare picks
// the right one rather than the first row in the bucket. We craft both
// plaintexts to share the first 8 hex chars after "ak_" so the candidate
// bucket contains 2 rows.
func TestLookupAPIKey_PrefixCollisionsResolveByHash(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()

	// Two distinct plaintexts that share the same 8-hex-char prefix.
	// The body after the prefix is intentionally different so the SHA-256
	// hashes diverge.
	plain1 := "ak_aaaaaaaa00000000000000000000000000"
	plain2 := "ak_aaaaaaaaffffffffffffffffffffffffff"
	if plain1[3:11] != plain2[3:11] {
		t.Fatalf("test setup: plaintexts must share prefix")
	}

	row1 := &APIKey{
		ID: "row-1", Hash: HashAPIKey(plain1), Prefix: plain1[3:11],
		UserID: "u", Name: "k1",
	}
	row2 := &APIKey{
		ID: "row-2", Hash: HashAPIKey(plain2), Prefix: plain2[3:11],
		UserID: "u", Name: "k2",
	}
	if err := s.DB().Create(row1).Error; err != nil {
		t.Fatalf("create row1: %v", err)
	}
	if err := s.DB().Create(row2).Error; err != nil {
		t.Fatalf("create row2: %v", err)
	}

	got1, err := s.LookupAPIKey(ctx, plain1)
	if err != nil {
		t.Fatalf("lookup plain1: %v", err)
	}
	if got1.ID != "row-1" {
		t.Errorf("collision bucket picked wrong row for plain1: got %q, want row-1", got1.ID)
	}
	got2, err := s.LookupAPIKey(ctx, plain2)
	if err != nil {
		t.Fatalf("lookup plain2: %v", err)
	}
	if got2.ID != "row-2" {
		t.Errorf("collision bucket picked wrong row for plain2: got %q, want row-2", got2.ID)
	}
}
