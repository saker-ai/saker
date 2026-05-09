package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookieName = "saker_session"
	sessionTTL        = 7 * 24 * time.Hour // 7 days
)

// createToken builds a signed token: "username:role:expiresUnix:nonce.signature"
func (a *AuthManager) createToken(username, role string) string {
	expires := time.Now().Add(sessionTTL).Unix()
	nonce := make([]byte, 8)
	_, _ = rand.Read(nonce)

	// Base64-encode username so `:` in usernames cannot break the `:`-delimited payload.
	encodedUser := base64.RawURLEncoding.EncodeToString([]byte(username))
	payload := fmt.Sprintf("%s:%s:%d:%s", encodedUser, role, expires, hex.EncodeToString(nonce))
	sig := a.sign(payload)
	return payload + "." + sig
}

// validToken checks that the token signature is correct, not expired, and not revoked.
// Supports both old format "username:expires:nonce" and new "username:role:expires:nonce".
func (a *AuthManager) validToken(token string) bool {
	dot := strings.LastIndex(token, ".")
	if dot < 0 {
		return false
	}
	payload := token[:dot]
	sig := token[dot+1:]

	// Verify signature.
	if !hmac.Equal([]byte(sig), []byte(a.sign(payload))) {
		return false
	}

	// Parse expiry — supports both 3-part (legacy) and 4-part (new) format.
	parts := strings.SplitN(payload, ":", 4)
	var expiresStr string
	switch len(parts) {
	case 3:
		// Legacy: "username:expires:nonce"
		expiresStr = parts[1]
	case 4:
		// New: "username:role:expires:nonce"
		expiresStr = parts[2]
	default:
		return false
	}

	expires, err := strconv.ParseInt(expiresStr, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > expires {
		return false
	}

	// Check revocation.
	a.mu.RLock()
	_, revoked := a.revoked[token]
	a.mu.RUnlock()
	return !revoked
}

// extractTokenInfo parses username and role from a validated token payload.
// Token format: "username:role:expiresUnix:nonce.signature"
func (a *AuthManager) extractTokenInfo(token string) (username, role string) {
	dot := strings.LastIndex(token, ".")
	if dot < 0 {
		return "", "admin"
	}
	payload := token[:dot]
	parts := strings.SplitN(payload, ":", 4)
	if len(parts) < 2 {
		return "", "admin"
	}
	// Decode base64-encoded username; fall back to raw value for legacy tokens.
	rawUser := parts[0]
	decoded, err := base64.RawURLEncoding.DecodeString(rawUser)
	if err == nil {
		username = string(decoded)
	} else {
		username = rawUser // legacy token without base64 encoding
	}
	if len(parts) >= 4 {
		role = parts[1]
	} else {
		role = "admin" // legacy tokens without role field
	}
	return username, role
}

func (a *AuthManager) sign(payload string) string {
	key := a.signingKey()
	if key == nil {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *AuthManager) revokeToken(token string) {
	a.mu.Lock()
	a.revoked[token] = time.Now().Add(sessionTTL)
	a.mu.Unlock()
}

func (a *AuthManager) signingKey() []byte {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessionSigningKey
}

// signingKey persists the session signing key so existing sessions survive
// restarts. On first start a random key is generated and saved; on subsequent
// starts the saved key is loaded.
func generateSigningKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(fmt.Sprintf("crypto/rand.Read failed: %v — cannot generate secure signing key, refusing to start", err))
	}
	return key
}

// SetKeyDir persists the session signing key to a file under the given
// directory. On first start the random key is saved; on subsequent starts
// the saved key is loaded so existing sessions remain valid.
func (a *AuthManager) SetKeyDir(dir string) {
	if dir == "" {
		return
	}
	keyPath := filepath.Join(dir, ".saker_session_key")

	a.mu.Lock()
	currentKey := a.sessionSigningKey
	a.keyFile = keyPath
	a.mu.Unlock()

	// Try to load an existing key file.
	if data, err := os.ReadFile(keyPath); err == nil && len(data) == 32 {
		a.mu.Lock()
		a.sessionSigningKey = data
		a.mu.Unlock()
		a.logger.Debug("session key loaded from file", "path", keyPath)
		return
	}

	// No existing key file — persist the generated key.
	if err := os.WriteFile(keyPath, currentKey, 0600); err != nil {
		a.logger.Warn("failed to persist session key; sessions will invalidate on restart", "path", keyPath, "error", err)
	} else {
		a.logger.Debug("session key persisted to file", "path", keyPath)
	}
}

// cleanupRevokedLoop periodically removes expired revoked tokens.
func (a *AuthManager) cleanupRevokedLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			a.mu.Lock()
			now := time.Now()
			for k, exp := range a.revoked {
				if now.After(exp) {
					delete(a.revoked, k)
				}
			}
			a.mu.Unlock()
		case <-a.stopCleanup:
			return
		}
	}
}