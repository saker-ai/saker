package apps

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// GenerateAPIKey returns the plaintext key (shown to the user once), its
// bcrypt hash, and the 8-char display prefix that is safe to store and show.
// Plaintext format: "ak_" + 32 hex chars (35 chars total).
func GenerateAPIKey() (plaintext, hash, prefix string, err error) {
	b := make([]byte, 16)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	plaintext = "ak_" + hex.EncodeToString(b)
	hash, prefix, err = HashAPIKey(plaintext)
	return plaintext, hash, prefix, err
}

// HashAPIKey is exported for tests so they can produce deterministic fixtures.
// prefix is the first 8 characters of the plaintext (e.g. "ak_1a2b3c").
func HashAPIKey(plaintext string) (hash, prefix string, err error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", "", err
	}
	prefix = plaintext
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return string(hashed), prefix, nil
}

// ValidateAPIKey parses an Authorization header ("Bearer <key>" or plain
// "<key>") and returns the matching ApiKey when bcrypt verifies. Returns
// (nil, false) on any mismatch. The returned pointer's LastUsedAt is set
// to now; the caller must persist the KeysFile to make it durable.
func ValidateAPIKey(keys *KeysFile, header string) (*ApiKey, bool) {
	if keys == nil || header == "" {
		return nil, false
	}
	// Strip optional "Bearer " prefix (case-insensitive).
	key := header
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		key = header[7:]
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false
	}
	now := time.Now().UTC()
	for i := range keys.ApiKeys {
		ak := &keys.ApiKeys[i]
		// ExpiresAt is opt-in; nil → never expires. Skip the bcrypt
		// comparison entirely when expired so a leaked-and-rotated key
		// can't keep authenticating until UpdateLastUsed re-saves it.
		if ak.ExpiresAt != nil && !ak.ExpiresAt.After(now) {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(ak.Hash), []byte(key)) == nil {
			ak.LastUsedAt = &now
			return ak, true
		}
	}
	return nil, false
}

// GenerateShareToken returns a 32-char URL-safe random token (24 random bytes
// base64url-encoded without padding).
func GenerateShareToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// tokenBucket is a simple sliding-window rate limiter for a single token.
// It keeps the timestamps of the last RateLimit hits within the past minute.
type tokenBucket struct {
	mu        sync.Mutex
	hits      []time.Time
	rateLimit int
}

func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	// Evict hits older than 1 minute.
	i := 0
	for i < len(b.hits) && b.hits[i].Before(cutoff) {
		i++
	}
	b.hits = b.hits[i:]
	if len(b.hits) >= b.rateLimit {
		return false
	}
	b.hits = append(b.hits, now)
	return true
}

// rateLimiters maps token string → *tokenBucket.
var rateLimiters sync.Map

// ValidateShareToken returns the matching ShareToken when found, not expired,
// and (when RateLimit > 0) within the per-minute budget. Returns (nil, false)
// on any mismatch.
func ValidateShareToken(keys *KeysFile, token string) (*ShareToken, bool) {
	if keys == nil || token == "" {
		return nil, false
	}
	for i := range keys.ShareTokens {
		st := &keys.ShareTokens[i]
		if st.Token != token {
			continue
		}
		// Check expiry.
		if st.ExpiresAt != nil && time.Now().After(*st.ExpiresAt) {
			return nil, false
		}
		// Check rate limit.
		if st.RateLimit > 0 {
			v, _ := rateLimiters.LoadOrStore(token, &tokenBucket{rateLimit: st.RateLimit})
			bucket := v.(*tokenBucket)
			if !bucket.allow() {
				return nil, false
			}
		}
		return st, true
	}
	return nil, false
}
