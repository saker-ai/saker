package embedding

import (
	"os"
	"testing"
)

func TestDetectBackend(t *testing.T) {
	t.Parallel()
	// Save original env vars and restore after test.
	origVars := map[string]string{}
	for _, b := range backendRegistry {
		origVars[b.envKey] = os.Getenv(b.envKey)
	}
	defer func() {
		for k, v := range origVars {
			os.Setenv(k, v)
		}
	}()

	// Clear all API keys.
	for _, b := range backendRegistry {
		os.Setenv(b.envKey, "")
	}
	if got := DetectBackend(); got != "" {
		t.Errorf("DetectBackend with no keys = %q, want empty", got)
	}

	// Set only one key.
	os.Setenv("DASHSCOPE_API_KEY", "test-key")
	if got := DetectBackend(); got != "aliyun" {
		t.Errorf("DetectBackend with DASHSCOPE_API_KEY = %q, want %q", got, "aliyun")
	}

	// Set multiple keys — first match wins (aliyun is first in registry).
	os.Setenv("GEMINI_API_KEY", "test-key-2")
	if got := DetectBackend(); got != "aliyun" {
		t.Errorf("DetectBackend with multiple keys = %q, want %q (first match)", got, "aliyun")
	}

	// Clear aliyun key, gemini should win.
	os.Setenv("DASHSCOPE_API_KEY", "")
	if got := DetectBackend(); got != "gemini" {
		t.Errorf("DetectBackend with only GEMINI_API_KEY = %q, want %q", got, "gemini")
	}
}

func TestAllBackends(t *testing.T) {
	t.Parallel()
	backends := AllBackends()
	if len(backends) != len(backendRegistry) {
		t.Fatalf("AllBackends returned %d entries, want %d", len(backends), len(backendRegistry))
	}

	expectedNames := []string{"aliyun", "gemini", "openai", "voyage", "jina"}
	expectedEnvKeys := []string{"DASHSCOPE_API_KEY", "GEMINI_API_KEY", "OPENAI_API_KEY", "VOYAGE_API_KEY", "JINA_API_KEY"}

	for i, b := range backends {
		if b.Name != expectedNames[i] {
			t.Errorf("AllBackends()[%d].Name = %q, want %q", i, b.Name, expectedNames[i])
		}
		if b.EnvKey != expectedEnvKeys[i] {
			t.Errorf("AllBackends()[%d].EnvKey = %q, want %q", i, b.EnvKey, expectedEnvKeys[i])
		}
	}
}

func TestAvailableBackends(t *testing.T) {
	t.Parallel()
	origVars := map[string]string{}
	for _, b := range backendRegistry {
		origVars[b.envKey] = os.Getenv(b.envKey)
	}
	defer func() {
		for k, v := range origVars {
			os.Setenv(k, v)
		}
	}()

	// Clear all keys.
	for _, b := range backendRegistry {
		os.Setenv(b.envKey, "")
	}
	if got := AvailableBackends(); len(got) != 0 {
		t.Errorf("AvailableBackends with no keys = %v, want empty", got)
	}

	// Set two keys.
	os.Setenv("OPENAI_API_KEY", "sk-test")
	os.Setenv("JINA_API_KEY", "jina-test")
	avail := AvailableBackends()
	if len(avail) != 2 {
		t.Fatalf("AvailableBackends = %v, want 2 entries", avail)
	}
	if avail[0] != "openai" && avail[1] != "openai" {
		t.Errorf("expected openai in available backends: %v", avail)
	}
	if avail[0] != "jina" && avail[1] != "jina" {
		t.Errorf("expected jina in available backends: %v", avail)
	}
}

func TestNewEmbedderNoBackend(t *testing.T) {
	t.Parallel()
	origVars := map[string]string{}
	for _, b := range backendRegistry {
		origVars[b.envKey] = os.Getenv(b.envKey)
	}
	defer func() {
		for k, v := range origVars {
			os.Setenv(k, v)
		}
	}()

	// Clear all API keys and backend.
	for _, b := range backendRegistry {
		os.Setenv(b.envKey, "")
	}
	_, err := NewEmbedder(Config{Backend: ""})
	if err == nil {
		t.Error("NewEmbedder with no backend and no env keys: expected error, got nil")
	}
}

func TestNewEmbedderUnknownBackend(t *testing.T) {
	t.Parallel()
	_, err := NewEmbedder(Config{Backend: "unknown_provider"})
	if err == nil {
		t.Error("NewEmbedder with unknown backend: expected error, got nil")
	}
}

func TestBackendInfoStruct(t *testing.T) {
	t.Parallel()
	bi := BackendInfo{
		Name:      "gemini",
		EnvKey:    "GEMINI_API_KEY",
		Available: true,
	}
	if bi.Name != "gemini" {
		t.Errorf("BackendInfo.Name = %q, want %q", bi.Name, "gemini")
	}
	if bi.EnvKey != "GEMINI_API_KEY" {
		t.Errorf("BackendInfo.EnvKey = %q, want %q", bi.EnvKey, "GEMINI_API_KEY")
	}
	if !bi.Available {
		t.Error("BackendInfo.Available = false, want true")
	}
}

func TestConfigDefaults(t *testing.T) {
	t.Parallel()
	cfg := Config{}
	if cfg.Backend != "" {
		t.Errorf("Config.Backend default = %q, want empty", cfg.Backend)
	}
	if cfg.APIKey != "" {
		t.Errorf("Config.APIKey default = %q, want empty", cfg.APIKey)
	}
	if cfg.Model != "" {
		t.Errorf("Config.Model default = %q, want empty", cfg.Model)
	}
	if cfg.Dims != 0 {
		t.Errorf("Config.Dims default = %d, want 0", cfg.Dims)
	}
	if cfg.RPM != 0 {
		t.Errorf("Config.RPM default = %d, want 0", cfg.RPM)
	}
	if cfg.BaseURL != "" {
		t.Errorf("Config.BaseURL default = %q, want empty", cfg.BaseURL)
	}
}