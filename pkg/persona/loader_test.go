package persona

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromDir_Empty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	profiles, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestLoadFromDir_NonExistent(t *testing.T) {
	t.Parallel()
	profiles, err := LoadFromDir("/nonexistent/path")
	if err != nil {
		t.Fatalf("expected nil error for non-existent dir, got: %v", err)
	}
	if profiles != nil {
		t.Fatalf("expected nil profiles, got %v", profiles)
	}
}

func TestLoadFromDir_WithPersonaMD(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	personaDir := filepath.Join(dir, "aria")
	if err := os.MkdirAll(personaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `---
name: Aria
emoji: "🌟"
description: A creative assistant
model: claude-sonnet-4-5
language: Chinese
---
You are a warm and creative AI assistant.`

	if err := os.WriteFile(filepath.Join(personaDir, "PERSONA.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	profiles, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}

	p := profiles[0]
	if p.ID != "aria" {
		t.Errorf("ID = %q, want %q", p.ID, "aria")
	}
	if p.Name != "Aria" {
		t.Errorf("Name = %q, want %q", p.Name, "Aria")
	}
	if p.Emoji != "🌟" {
		t.Errorf("Emoji = %q, want %q", p.Emoji, "🌟")
	}
	if p.Description != "A creative assistant" {
		t.Errorf("Description = %q, want %q", p.Description, "A creative assistant")
	}
	if p.Model != "claude-sonnet-4-5" {
		t.Errorf("Model = %q, want %q", p.Model, "claude-sonnet-4-5")
	}
	if p.Language != "Chinese" {
		t.Errorf("Language = %q, want %q", p.Language, "Chinese")
	}
	if p.Soul != "You are a warm and creative AI assistant." {
		t.Errorf("Soul = %q, want body text", p.Soul)
	}
}

func TestLoadFromDir_FallbackSoulMD(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	personaDir := filepath.Join(dir, "bot")
	if err := os.MkdirAll(personaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// PERSONA.md with no soul in frontmatter or body
	if err := os.WriteFile(filepath.Join(personaDir, "PERSONA.md"), []byte("---\nname: Bot\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Standalone SOUL.md
	if err := os.WriteFile(filepath.Join(personaDir, "SOUL.md"), []byte("I am a helpful bot."), 0o644); err != nil {
		t.Fatal(err)
	}

	profiles, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	if profiles[0].Soul != "I am a helpful bot." {
		t.Errorf("Soul = %q, want SOUL.md content", profiles[0].Soul)
	}
}

func TestLoadFromDir_NoFrontmatter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	personaDir := filepath.Join(dir, "simple")
	if err := os.MkdirAll(personaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(personaDir, "PERSONA.md"), []byte("Just a soul text."), 0o644); err != nil {
		t.Fatal(err)
	}

	profiles, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	if profiles[0].Soul != "Just a soul text." {
		t.Errorf("Soul = %q, want plain text", profiles[0].Soul)
	}
	if profiles[0].ID != "simple" {
		t.Errorf("ID = %q, want dir name", profiles[0].ID)
	}
}

func TestLoadFromDir_YAMLLists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	personaDir := filepath.Join(dir, "toolbot")
	if err := os.MkdirAll(personaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `---
name: ToolBot
enabledTools: [bash, file_read]
disallowedTools: [file_write]
---
`
	if err := os.WriteFile(filepath.Join(personaDir, "PERSONA.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	profiles, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p := profiles[0]
	if len(p.EnabledTools) != 2 || p.EnabledTools[0] != "bash" || p.EnabledTools[1] != "file_read" {
		t.Errorf("EnabledTools = %v, want [bash file_read]", p.EnabledTools)
	}
	if len(p.DisallowedTools) != 1 || p.DisallowedTools[0] != "file_write" {
		t.Errorf("DisallowedTools = %v, want [file_write]", p.DisallowedTools)
	}
}

func TestParseYAMLLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		line      string
		wantKey   string
		wantValue string
	}{
		{"name: Aria", "name", "Aria"},
		{"emoji: \"🌟\"", "emoji", "\"🌟\""},
		{"no-colon", "", ""},
		{"key:", "key", ""},
		{" spaces : value ", "spaces", "value"},
	}
	for _, tt := range tests {
		k, v := parseYAMLLine(tt.line)
		if k != tt.wantKey || v != tt.wantValue {
			t.Errorf("parseYAMLLine(%q) = (%q, %q), want (%q, %q)", tt.line, k, v, tt.wantKey, tt.wantValue)
		}
	}
}

func TestParseYAMLList(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  int
	}{
		{"[a, b, c]", 3},
		{"[]", 0},
		{"", 0},
		{"[\"quoted\", 'single']", 2},
	}
	for _, tt := range tests {
		got := parseYAMLList(tt.input)
		if len(got) != tt.want {
			t.Errorf("parseYAMLList(%q) len = %d, want %d", tt.input, len(got), tt.want)
		}
	}
}
