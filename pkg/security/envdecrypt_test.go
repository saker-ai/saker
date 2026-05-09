package security

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	// Use a known master key via env var.
	key := strings.Repeat("ab", 16) // 32 hex chars → 16 bytes... need 64 hex chars
	key = strings.Repeat("ab", 32)  // 64 hex chars → 32 bytes
	t.Setenv(masterKeyEnv, key)
	resetDerivedKey()

	plain := "sk-ant-test-key-1234567890"
	enc, err := Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !strings.HasPrefix(enc, encPrefix) {
		t.Fatalf("expected ENC: prefix, got %q", enc[:10])
	}

	dec, err := Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if dec != plain {
		t.Fatalf("round-trip failed: got %q, want %q", dec, plain)
	}
}

func TestResolveEnvPlaintext(t *testing.T) {
	plain := "sk-ant-plain-key"
	got := ResolveEnv(plain)
	if got != plain {
		t.Fatalf("ResolveEnv should pass through plain text, got %q", got)
	}
}

func TestResolveEnvEncrypted(t *testing.T) {
	key := strings.Repeat("cd", 32)
	t.Setenv(masterKeyEnv, key)
	resetDerivedKey()

	plain := "sk-ant-secret-key"
	enc, err := Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	got := ResolveEnv(enc)
	if got != plain {
		t.Fatalf("ResolveEnv(encrypted) = %q, want %q", got, plain)
	}
}

func TestResolveEnvBadPayload(t *testing.T) {
	bad := "ENC:not-valid-base64!!!"
	got := ResolveEnv(bad)
	if got != bad {
		t.Fatalf("ResolveEnv should return original on error, got %q", got)
	}
}

func TestResolveEnvWrongKey(t *testing.T) {
	// Encrypt with key1.
	key1 := strings.Repeat("aa", 32)
	t.Setenv(masterKeyEnv, key1)
	resetDerivedKey()

	enc, err := Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Decrypt with key2 — should fail and return original.
	key2 := strings.Repeat("bb", 32)
	t.Setenv(masterKeyEnv, key2)
	resetDerivedKey()

	got := ResolveEnv(enc)
	if got != enc {
		t.Fatalf("ResolveEnv with wrong key should return original, got %q", got)
	}
}

func TestDecryptInvalidFormats(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"no prefix", "plain-text"},
		{"empty payload", "ENC:"},
		{"too short", "ENC:" + hex.EncodeToString([]byte("short"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decrypt(tt.input)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseMasterKeyHex(t *testing.T) {
	hexKey := strings.Repeat("ef", 32) // 64 hex chars
	key, err := parseMasterKey(hexKey)
	if err != nil {
		t.Fatalf("parseMasterKey hex: %v", err)
	}
	if len(key) != aesKeyLen {
		t.Fatalf("expected %d bytes, got %d", aesKeyLen, len(key))
	}
}

func TestParseMasterKeyBase64(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	b64 := strings.TrimRight(string(mustBase64(raw)), "=")
	key, err := parseMasterKey(b64)
	if err != nil {
		t.Fatalf("parseMasterKey base64: %v", err)
	}
	if len(key) != aesKeyLen {
		t.Fatalf("expected %d bytes, got %d", aesKeyLen, len(key))
	}
}

func TestParseMasterKeyInvalid(t *testing.T) {
	_, err := parseMasterKey("too-short")
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestDerivedKeyConsistency(t *testing.T) {
	t.Setenv(masterKeyEnv, "")
	resetDerivedKey()

	k1, err1 := derivedKey()
	resetDerivedKey()
	k2, err2 := derivedKey()

	if err1 != nil || err2 != nil {
		t.Skipf("machine-id not available: err1=%v err2=%v", err1, err2)
	}
	if len(k1) != aesKeyLen {
		t.Fatalf("derived key length %d, want %d", len(k1), aesKeyLen)
	}
	if string(k1) != string(k2) {
		t.Fatal("derived key not consistent across calls")
	}
}

func TestMachineIDNotEmpty(t *testing.T) {
	id := machineID()
	if id == "" {
		t.Skip("no machine-id available in this environment")
	}
	t.Logf("machine-id: %s", id)
}

// resetDerivedKey clears the sync.Once cache for testing.
func resetDerivedKey() {
	derivedKeyOnce = sync.Once{}
	derivedKeyCache = nil
	derivedKeyErr = nil
}

func mustBase64(b []byte) []byte {
	dst := make([]byte, base64.StdEncoding.EncodedLen(len(b)))
	base64.StdEncoding.Encode(dst, b)
	return dst
}
