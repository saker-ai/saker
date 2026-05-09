package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveSettingsLocal_CreatesFile(t *testing.T) {
	dir := t.TempDir()

	s := &Settings{
		Model: "claude-sonnet-4-5",
		Aigo: &AigoConfig{
			Providers: map[string]AigoProvider{
				"ali": {Type: "aliyun", APIKey: "${DASHSCOPE_API_KEY}"},
			},
			Routing: map[string][]string{
				"image": {"ali/qwen-max-vl"},
			},
			Timeout: "60s",
		},
	}

	if err := SaveSettingsLocal(dir, s); err != nil {
		t.Fatalf("SaveSettingsLocal: %v", err)
	}

	path := filepath.Join(dir, ".saker", "settings.local.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected 0600 permissions, got %o", perm)
	}
}

func TestSaveSettingsLocal_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := &Settings{
		Aigo: &AigoConfig{
			Providers: map[string]AigoProvider{
				"ali":    {Type: "aliyun", APIKey: "test-key-1"},
				"openai": {Type: "openai", APIKey: "test-key-2"},
			},
			Routing: map[string][]string{
				"image": {"ali/qwen-max-vl", "openai/dall-e-3"},
				"video": {"ali/wanx-video"},
			},
			Timeout: "120s",
		},
	}

	if err := SaveSettingsLocal(dir, original); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadSettingsLocal(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil settings")
	}
	if loaded.Aigo == nil {
		t.Fatal("expected non-nil aigo config")
	}
	if len(loaded.Aigo.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(loaded.Aigo.Providers))
	}
	if loaded.Aigo.Providers["ali"].Type != "aliyun" {
		t.Fatalf("expected aliyun, got %s", loaded.Aigo.Providers["ali"].Type)
	}
	if len(loaded.Aigo.Routing["image"]) != 2 {
		t.Fatalf("expected 2 image routes, got %d", len(loaded.Aigo.Routing["image"]))
	}
	if loaded.Aigo.Timeout != "120s" {
		t.Fatalf("expected 120s, got %s", loaded.Aigo.Timeout)
	}
}

func TestLoadSettingsLocal_NotExists(t *testing.T) {
	dir := t.TempDir()

	loaded, err := LoadSettingsLocal(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded != nil {
		t.Fatal("expected nil for non-existent file")
	}
}

func TestSaveSettingsLocal_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()

	first := &Settings{Model: "model-1"}
	if err := SaveSettingsLocal(dir, first); err != nil {
		t.Fatalf("first save: %v", err)
	}

	second := &Settings{Model: "model-2"}
	if err := SaveSettingsLocal(dir, second); err != nil {
		t.Fatalf("second save: %v", err)
	}

	loaded, err := LoadSettingsLocal(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Model != "model-2" {
		t.Fatalf("expected model-2, got %s", loaded.Model)
	}
}
