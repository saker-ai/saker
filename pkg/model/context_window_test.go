package model

import "testing"

func TestLookupContextWindowExact(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{"qwen-max", 32_768},
		{"gpt-4o", 128_000},
		{"deepseek-chat", 163_840},
		{"claude-sonnet-4", 200_000},
	}
	for _, tc := range tests {
		if got := LookupContextWindow(tc.model); got != tc.want {
			t.Errorf("LookupContextWindow(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}

func TestLookupContextWindowPrefix(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{"claude-sonnet-4-20250514", 200_000},
		{"gpt-4o-2024-08-06", 128_000},
		{"qwen-max-latest", 32_768},
		{"deepseek-r1-0528", 163_840},
		{"gemini-2-flash", 1_048_576},
	}
	for _, tc := range tests {
		if got := LookupContextWindow(tc.model); got != tc.want {
			t.Errorf("LookupContextWindow(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}

func TestLookupContextWindowLongestPrefix(t *testing.T) {
	// "gpt-4o" should match before "gpt-4"
	if got := LookupContextWindow("gpt-4o-mini"); got != 128_000 {
		t.Errorf("gpt-4o-mini: got %d, want 128000", got)
	}
	// "gpt-4-turbo" should match before "gpt-4"
	if got := LookupContextWindow("gpt-4-turbo-2024"); got != 128_000 {
		t.Errorf("gpt-4-turbo-2024: got %d, want 128000", got)
	}
}

func TestLookupContextWindowCaseInsensitive(t *testing.T) {
	if got := LookupContextWindow("GPT-4o"); got != 128_000 {
		t.Errorf("GPT-4o: got %d, want 128000", got)
	}
}

func TestLookupContextWindowUnknown(t *testing.T) {
	if got := LookupContextWindow("unknown-model"); got != 0 {
		t.Errorf("unknown: got %d, want 0", got)
	}
	if got := LookupContextWindow(""); got != 0 {
		t.Errorf("empty: got %d, want 0", got)
	}
}

func TestRegisterContextWindow(t *testing.T) {
	RegisterContextWindow("my-custom-model", 50_000)
	if got := LookupContextWindow("my-custom-model-v2"); got != 50_000 {
		t.Errorf("custom model: got %d, want 50000", got)
	}
	// Override built-in
	RegisterContextWindow("gpt-4o", 256_000)
	if got := LookupContextWindow("gpt-4o"); got != 256_000 {
		t.Errorf("overridden gpt-4o: got %d, want 256000", got)
	}
	// Cleanup: remove custom entries for other tests
	customMu.Lock()
	delete(customWindows, "my-custom-model")
	delete(customWindows, "gpt-4o")
	customMu.Unlock()
}

func TestRegisterContextWindowInvalid(t *testing.T) {
	RegisterContextWindow("", 100)
	RegisterContextWindow("valid", 0)
	RegisterContextWindow("valid", -1)
	// None should have been added
	customMu.RLock()
	for _, key := range []string{"", "valid"} {
		if _, ok := customWindows[key]; ok {
			t.Errorf("invalid entry %q should not be registered", key)
		}
	}
	customMu.RUnlock()
}
