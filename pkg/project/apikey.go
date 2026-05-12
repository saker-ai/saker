package project

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// APIKey is a Bearer token used by the OpenAI-compatible inbound gateway
// (and other Bearer-keyed surfaces). The plaintext token is shown ONCE
// at create time and never persisted — only its SHA-256 hash lives here.
//
// Tokens use the "ak_" prefix (mirrors anthropic / openai conventions)
// followed by 32 random hex characters. Lookup is by hash; the database
// holds a unique index on Hash so concurrent reuse is impossible even
// in the astronomically unlikely event of two collisions.
type APIKey struct {
	// ID is a UUID assigned at insert time.
	ID string `gorm:"primaryKey;size:36"`

	// Hash is the SHA-256 of the plaintext token, lowercase hex. Unique.
	Hash string `gorm:"size:64;not null;uniqueIndex"`

	// Prefix is the first 8 chars of the plaintext token (after "ak_"),
	// stored so we can show "ak_a1b2c3d4..." in admin UIs without ever
	// reconstructing the full token. Not unique on its own.
	Prefix string `gorm:"size:16;not null;index"`

	// UserID identifies who owns the key. Required.
	UserID string `gorm:"size:36;not null;index"`

	// ProjectID scopes the key to a single project. Empty means "all
	// projects the user has access to" (rare; used by admin-style keys).
	ProjectID string `gorm:"size:36;index"`

	// Name is a human-readable label ("ci-pipeline", "vscode-laptop").
	// Optional; helps users keep track of which key is for what.
	Name string `gorm:"size:128"`

	// CreatedAt is set automatically at insert time.
	CreatedAt time.Time

	// LastUsedAt is touched (best-effort) on each successful auth. Nil
	// means the key has never been used.
	LastUsedAt *time.Time

	// RevokedAt is non-nil after the key was revoked. Lookups must
	// reject revoked keys at the application layer (the unique index
	// stays clean so the same plaintext can't be re-issued).
	RevokedAt *time.Time `gorm:"index"`
}

// HashAPIKey computes the canonical lowercase-hex SHA-256 of the plaintext
// token. Same function is used at create time and at lookup time.
func HashAPIKey(plain string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(plain)))
	return hex.EncodeToString(sum[:])
}

// generatePlaintextAPIKey returns an "ak_<32 hex>" token. The 32 hex chars
// give 128 bits of entropy — plenty for a Bearer token.
func generatePlaintextAPIKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("apikey: rand.Read: %w", err)
	}
	return "ak_" + hex.EncodeToString(b[:]), nil
}

// CreateAPIKeyResult is what CreateAPIKey returns: the row that was
// inserted plus the plaintext token. The plaintext is shown to the user
// once and then forgotten — it never goes back to the DB.
type CreateAPIKeyResult struct {
	APIKey    APIKey
	Plaintext string
}

// CreateAPIKey generates a new Bearer key and inserts its hash row.
// Returns the row + the plaintext token. The plaintext is the only thing
// the caller can display to the user; future lookups go through the hash.
func (s *Store) CreateAPIKey(ctx context.Context, userID, projectID, name string) (*CreateAPIKeyResult, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, errors.New("apikey: user id is required")
	}
	plain, err := generatePlaintextAPIKey()
	if err != nil {
		return nil, err
	}
	row := APIKey{
		ID:        newID(),
		Hash:      HashAPIKey(plain),
		Prefix:    plain[3:11], // skip "ak_", take first 8 hex chars
		UserID:    userID,
		ProjectID: projectID,
		Name:      strings.TrimSpace(name),
		CreatedAt: time.Now(),
	}
	if err := s.DB().WithContext(ctx).Create(&row).Error; err != nil {
		return nil, fmt.Errorf("apikey: create: %w", err)
	}
	return &CreateAPIKeyResult{APIKey: row, Plaintext: plain}, nil
}

// LookupAPIKey returns the row matching plaintext, or ErrAPIKeyNotFound
// if no such (non-revoked) key exists. Revoked keys are rejected here so
// callers don't need to re-check.
//
// The hash comparison itself runs in constant time (subtle.ConstantTimeCompare)
// to avoid leaking byte-level timing information that could let an attacker
// reconstruct a stolen-prefix key bit-by-bit. We index by Prefix (8 hex chars,
// first 32 bits of the random portion) so the candidate set is bounded — the
// expected count for any single prefix is 1, but a malicious attacker brute-
// forcing prefixes can't tell from response time which prefix happens to land
// in the table.
func (s *Store) LookupAPIKey(ctx context.Context, plain string) (*APIKey, error) {
	plain = strings.TrimSpace(plain)
	if !strings.HasPrefix(plain, "ak_") || len(plain) < 11 {
		return nil, ErrAPIKeyNotFound
	}
	prefix := plain[3:11]
	wantHash := HashAPIKey(plain)

	var rows []APIKey
	err := s.DB().WithContext(ctx).
		Where("prefix = ? AND revoked_at IS NULL", prefix).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("apikey: lookup: %w", err)
	}

	wantBytes := []byte(wantHash)
	var match *APIKey
	for i := range rows {
		// Iterate the entire candidate set rather than short-circuiting so
		// the wall-clock cost is tied to the prefix bucket size, not to
		// whether or where in the bucket the match lives.
		if subtle.ConstantTimeCompare([]byte(rows[i].Hash), wantBytes) == 1 {
			match = &rows[i]
		}
	}
	if match == nil {
		return nil, ErrAPIKeyNotFound
	}
	return match, nil
}

// TouchAPIKey best-effort updates LastUsedAt. Errors are swallowed (the
// caller has already authenticated successfully — a clock-skew or
// transient DB error here shouldn't fail the request).
func (s *Store) TouchAPIKey(ctx context.Context, id string) {
	now := time.Now()
	_ = s.DB().WithContext(ctx).Model(&APIKey{}).
		Where("id = ?", id).
		Update("last_used_at", now).Error
}

// RevokeAPIKey marks the key as revoked. Idempotent.
func (s *Store) RevokeAPIKey(ctx context.Context, id string) error {
	now := time.Now()
	res := s.DB().WithContext(ctx).Model(&APIKey{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", now)
	return res.Error
}

// ListAPIKeys returns the keys owned by userID, newest first.
func (s *Store) ListAPIKeys(ctx context.Context, userID string) ([]APIKey, error) {
	var rows []APIKey
	err := s.DB().WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// ErrAPIKeyNotFound signals the supplied Bearer token has no live row in
// the database. The OpenAI gateway translates this to HTTP 401.
var ErrAPIKeyNotFound = errors.New("apikey: not found")
