package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
)

const (
	// encPrefix marks an AES-256-GCM encrypted value.
	encPrefix = "ENC:"
	// masterKeyEnv is the environment variable for a user-supplied master key.
	masterKeyEnv = "SAKER_MASTER_KEY"
	// derivationSalt is mixed with machine-id to produce the default key.
	derivationSalt = "saker-default-key-salt-2026"
	// aesKeyLen is the required AES-256 key length.
	aesKeyLen = 32
	// gcmNonceLen is the standard GCM nonce size.
	gcmNonceLen = 12
)

var (
	ErrNoMasterKey    = errors.New("security: no master key available")
	ErrInvalidPayload = errors.New("security: invalid encrypted payload")
	ErrDecryptFailed  = errors.New("security: decryption failed")
)

// ResolveEnv checks whether value has the ENC: prefix; if so it decrypts it
// using the master key. Plain values are returned unchanged. On any error the
// original value is returned so callers never get a silent empty string.
func ResolveEnv(value string) string {
	if !strings.HasPrefix(value, encPrefix) {
		return value
	}
	plaintext, err := Decrypt(value)
	if err != nil {
		return value // fall through — let the provider surface an auth error
	}
	return plaintext
}

// Encrypt produces an "ENC:<base64>" token from plaintext using AES-256-GCM.
func Encrypt(plaintext string) (string, error) {
	key, err := MasterKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcmNonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil) // nonce || ciphertext || tag
	return encPrefix + base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt reverses Encrypt. The input must start with "ENC:".
func Decrypt(token string) (string, error) {
	if !strings.HasPrefix(token, encPrefix) {
		return "", ErrInvalidPayload
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(token, encPrefix))
	if err != nil {
		return "", ErrInvalidPayload
	}
	if len(raw) < gcmNonceLen+1 {
		return "", ErrInvalidPayload
	}
	key, err := MasterKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, raw[:gcmNonceLen], raw[gcmNonceLen:], nil)
	if err != nil {
		return "", ErrDecryptFailed
	}
	return string(plaintext), nil
}

// MasterKey returns the 32-byte AES key, preferring SAKER_MASTER_KEY env var,
// falling back to a machine-derived key.
func MasterKey() ([]byte, error) {
	if key := os.Getenv(masterKeyEnv); key != "" {
		return parseMasterKey(key)
	}
	return derivedKey()
}

// parseMasterKey accepts hex or base64 encoded 32-byte keys.
func parseMasterKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	// Try hex first (64 hex chars = 32 bytes).
	if len(raw) == 64 {
		if b, err := hex.DecodeString(raw); err == nil && len(b) == aesKeyLen {
			return b, nil
		}
	}
	// Try base64 (44 chars with padding = 32 bytes).
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == aesKeyLen {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(b) == aesKeyLen {
		return b, nil
	}
	return nil, errors.New("security: SAKER_MASTER_KEY must be 32 bytes encoded as hex (64 chars) or base64")
}

var (
	derivedKeyOnce  sync.Once
	derivedKeyCache []byte
	derivedKeyErr   error
)

func derivedKey() ([]byte, error) {
	derivedKeyOnce.Do(func() {
		mid := machineID()
		if mid == "" {
			derivedKeyErr = ErrNoMasterKey
			return
		}
		h := sha256.Sum256([]byte(mid + derivationSalt))
		derivedKeyCache = h[:]
	})
	return derivedKeyCache, derivedKeyErr
}

// machineID returns a stable machine identifier. Platform-specific
// implementations are in envdecrypt_<os>.go files.
// Fallback: hostname.
func machineID() string {
	if id := platformMachineID(); id != "" {
		return id
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return ""
}
