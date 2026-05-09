package persona

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvedSoul_inline(t *testing.T) {
	t.Parallel()
	p := &Profile{Soul: "I am a creative helper."}
	got := p.ResolvedSoul("")
	if got != "I am a creative helper." {
		t.Errorf("ResolvedSoul = %q", got)
	}
}

func TestResolvedSoul_file(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "Be warm and poetic."
	os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte(content), 0644)

	p := &Profile{SoulFile: "SOUL.md"}
	got := p.ResolvedSoul(dir)
	if got != content {
		t.Errorf("ResolvedSoul = %q, want %q", got, content)
	}
}

func TestResolvedSoul_inlinePriority(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("file soul"), 0644)

	p := &Profile{Soul: "inline soul", SoulFile: "SOUL.md"}
	got := p.ResolvedSoul(dir)
	if got != "inline soul" {
		t.Errorf("inline should take priority: got %q", got)
	}
}

func TestResolvedSoul_missingFile(t *testing.T) {
	t.Parallel()
	p := &Profile{SoulFile: "/nonexistent/SOUL.md"}
	got := p.ResolvedSoul("")
	if got != "" {
		t.Errorf("expected empty for missing file, got %q", got)
	}
}

func TestResolvedInstructions_inline(t *testing.T) {
	t.Parallel()
	p := &Profile{Instructions: "Follow these rules."}
	got := p.ResolvedInstructions("")
	if got != "Follow these rules." {
		t.Errorf("ResolvedInstructions = %q", got)
	}
}

func TestResolvedInstructions_file(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agent rules"), 0644)

	p := &Profile{InstructFile: "AGENTS.md"}
	got := p.ResolvedInstructions(dir)
	if got != "agent rules" {
		t.Errorf("ResolvedInstructions = %q", got)
	}
}

func TestMergeProfile(t *testing.T) {
	t.Parallel()
	dst := &Profile{
		Name:     "Base",
		Model:    "claude-sonnet",
		Language: "English",
	}
	src := &Profile{
		Name:         "Override",
		Emoji:        "🌸",
		EnabledTools: []string{"bash"},
	}
	mergeProfile(dst, src)

	if dst.Name != "Override" {
		t.Errorf("Name = %q", dst.Name)
	}
	if dst.Emoji != "🌸" {
		t.Errorf("Emoji = %q", dst.Emoji)
	}
	if dst.Model != "claude-sonnet" {
		t.Errorf("Model should be preserved: %q", dst.Model)
	}
	if dst.Language != "English" {
		t.Errorf("Language should be preserved: %q", dst.Language)
	}
	if len(dst.EnabledTools) != 1 || dst.EnabledTools[0] != "bash" {
		t.Errorf("EnabledTools = %v", dst.EnabledTools)
	}
}
