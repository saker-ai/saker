package api

import (
	"os"
	"path/filepath"
	"testing"

	runtimeskills "github.com/saker-ai/saker/pkg/runtime/skills"
)

func TestRuntimeReloadSkillsLoadsImportedDefinitions(t *testing.T) {
	root := t.TempDir()
	configRoot := filepath.Join(root, ".saker")
	skillDir := filepath.Join(configRoot, "skills", "demo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "---\nname: demo-skill\ndescription: imported demo\nkeywords:\n  - demo\n---\nbody"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	rt := &Runtime{
		opts:  Options{ProjectRoot: root, ConfigRoot: configRoot},
		skReg: runtimeskills.NewRegistry(),
	}

	if errs := rt.ReloadSkills(); len(errs) > 0 {
		t.Fatalf("reload skills errors: %v", errs)
	}

	available := rt.AvailableSkills()
	if len(available) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(available))
	}
	if available[0].Name != "demo-skill" {
		t.Fatalf("loaded skill %q, want demo-skill", available[0].Name)
	}
}
