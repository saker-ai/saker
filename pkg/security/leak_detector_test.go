package security

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLeakDetector_CleanContent(t *testing.T) {
	d := NewLeakDetector()
	result := d.Scan("Hello world! This is just regular text with no secrets.")
	if !result.IsClean() {
		t.Fatal("expected clean content to have no findings")
	}
	if result.ShouldBlock {
		t.Fatal("clean content should not be blocked")
	}
}

func TestLeakDetector_OpenAIKey(t *testing.T) {
	d := NewLeakDetector()
	result := d.Scan("API key: sk-proj-abc123def456ghi789jkl012mno345pqrT3BlbkFJtest123")
	if result.IsClean() {
		t.Fatal("expected OpenAI key to be detected")
	}
	if !result.ShouldBlock {
		t.Fatal("OpenAI key should trigger block")
	}
	found := false
	for _, f := range result.Findings {
		if f.PatternName == "openai_api_key" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected openai_api_key pattern match")
	}
}

func TestLeakDetector_AWSKey(t *testing.T) {
	d := NewLeakDetector()
	result := d.Scan("AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE")
	if result.IsClean() {
		t.Fatal("expected AWS key to be detected")
	}
	if !result.ShouldBlock {
		t.Fatal("AWS key should trigger block")
	}
}

func TestLeakDetector_GitHubToken(t *testing.T) {
	d := NewLeakDetector()
	content := fmt.Sprintf("token: ghp_%s", strings.Repeat("x", 36))
	result := d.Scan(content)
	if result.IsClean() {
		t.Fatal("expected GitHub token to be detected")
	}
}

func TestLeakDetector_AnthropicKey(t *testing.T) {
	d := NewLeakDetector()
	key := fmt.Sprintf("sk-ant-api%s", strings.Repeat("a", 90))
	result := d.Scan("key: " + key)
	if result.IsClean() {
		t.Fatal("expected Anthropic key to be detected")
	}
	if !result.ShouldBlock {
		t.Fatal("Anthropic key should trigger block")
	}
}

func TestLeakDetector_StripeKey(t *testing.T) {
	d := NewLeakDetector()
	content := fmt.Sprintf("sk_%s_aAbBcCdDfFgGhHjJkKmMnNpPqQ", "live")
	result := d.Scan(content)
	if result.IsClean() {
		t.Fatal("expected Stripe key to be detected")
	}
}

func TestLeakDetector_PEMPrivateKey(t *testing.T) {
	d := NewLeakDetector()
	result := d.Scan("-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA...")
	if result.IsClean() {
		t.Fatal("expected PEM private key to be detected")
	}
}

func TestLeakDetector_SSHPrivateKey(t *testing.T) {
	d := NewLeakDetector()
	result := d.Scan("-----BEGIN OPENSSH PRIVATE KEY-----\nbase64data==")
	if result.IsClean() {
		t.Fatal("expected SSH private key to be detected")
	}
}

func TestLeakDetector_SlackToken(t *testing.T) {
	d := NewLeakDetector()
	result := d.Scan("xoxb-1234567890-abcdefghij")
	if result.IsClean() {
		t.Fatal("expected Slack token to be detected")
	}
}

func TestLeakDetector_GroqKey(t *testing.T) {
	d := NewLeakDetector()
	content := fmt.Sprintf("GROQ_API_KEY=gsk_%s", strings.Repeat("a", 56))
	result := d.Scan(content)
	if result.IsClean() {
		t.Fatal("expected Groq key to be detected")
	}
}

func TestLeakDetector_OpenRouterKey(t *testing.T) {
	d := NewLeakDetector()
	result := d.Scan("LLM_API_KEY=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")
	if result.IsClean() {
		t.Fatal("expected OpenRouter key to be detected")
	}
	if !result.ShouldBlock {
		t.Fatal("OpenRouter key should trigger block")
	}
}

func TestLeakDetector_TelegramBotToken(t *testing.T) {
	d := NewLeakDetector()
	result := d.Scan("TELEGRAM_BOT_TOKEN=12345678901:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw")
	if result.IsClean() {
		t.Fatal("expected Telegram bot token to be detected")
	}
}

func TestLeakDetector_BearerRedact(t *testing.T) {
	d := NewLeakDetector()
	result := d.Scan("Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9_longtokenvalue")
	if result.IsClean() {
		t.Fatal("expected Bearer token to be detected")
	}
	if result.ShouldBlock {
		t.Fatal("Bearer token should redact, not block")
	}
	if result.RedactedContent == "" {
		t.Fatal("expected redacted content")
	}
	if strings.Contains(result.RedactedContent, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9") {
		t.Fatal("redacted content should not contain the token")
	}
	if !strings.Contains(result.RedactedContent, "[REDACTED]") {
		t.Fatal("redacted content should contain [REDACTED]")
	}
}

func TestLeakDetector_MultipleMatches(t *testing.T) {
	d := NewLeakDetector()
	content := fmt.Sprintf("Keys: AKIAIOSFODNN7EXAMPLE and ghp_%s", strings.Repeat("x", 36))
	result := d.Scan(content)
	if len(result.Findings) < 2 {
		t.Fatalf("expected at least 2 findings, got %d", len(result.Findings))
	}
}

func TestLeakDetector_ScanAndClean_Blocks(t *testing.T) {
	d := NewLeakDetector()
	_, _, err := d.ScanAndClean("sk-proj-test1234567890abcdefghij")
	if err == nil {
		t.Fatal("expected error for blocked content")
	}
}

func TestLeakDetector_ScanAndClean_PassesClean(t *testing.T) {
	d := NewLeakDetector()
	cleaned, findings, err := d.ScanAndClean("Just regular text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleaned != "Just regular text" {
		t.Fatalf("expected unchanged content, got %q", cleaned)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %d", len(findings))
	}
}

func TestLeakDetector_CleanTextNotFlagged(t *testing.T) {
	d := NewLeakDetector()
	cleanTexts := []string{
		"The API returns a JSON response",
		"Use ssh to connect to the server",
		"sk-this-is-too-short",
		"The key concept is immutability",
	}
	for _, text := range cleanTexts {
		result := d.Scan(text)
		if result.ShouldBlock {
			t.Fatalf("clean text falsely blocked: %s", text)
		}
	}
}

func TestMaskSecret(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"short", "*****"},
		{"", ""},
		{"12345678", "********"},
		{"123456789", "1234*6789"},
		{"sk-test1234567890abcdef", "sk-t********cdef"},
	}
	for _, tt := range tests {
		got := maskSecret(tt.input)
		if got != tt.want {
			t.Errorf("maskSecret(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLeakDetector_Performance_100KB(t *testing.T) {
	d := NewLeakDetector()
	payload := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 2500)
	if len(payload) < 100_000 {
		t.Fatal("payload too small")
	}
	start := time.Now()
	result := d.Scan(payload)
	elapsed := time.Since(start)
	if elapsed > 200*time.Millisecond {
		t.Fatalf("scan took %v on 100KB clean text (threshold: 200ms)", elapsed)
	}
	if !result.IsClean() {
		t.Fatal("clean text should not have findings")
	}
}

func TestLeakDetector_SecretAtDifferentPositions(t *testing.T) {
	d := NewLeakDetector()
	key := "AKIAIOSFODNN7EXAMPLE"

	cases := []string{key, "prefix " + key + " suffix", "end: " + key}
	for _, content := range cases {
		result := d.Scan(content)
		if result.IsClean() {
			t.Fatalf("key not detected in %q", content)
		}
	}
}

func TestLeakDetector_PatternCount(t *testing.T) {
	d := NewLeakDetector()
	if d.PatternCount() < 18 {
		t.Fatalf("expected at least 18 patterns, got %d", d.PatternCount())
	}
}
