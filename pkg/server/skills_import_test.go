package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeSkillImportParamsPath(t *testing.T) {
	sourceType, paths, err := normalizeSkillImportParams(skillImportParams{
		SourceType:  skillImportSourcePath,
		SourcePath:  "/tmp/demo-skill",
		TargetScope: skillImportScopeLocal,
	})
	if err != nil {
		t.Fatalf("normalize import params: %v", err)
	}
	if sourceType != skillImportSourcePath {
		t.Fatalf("sourceType=%q want %q", sourceType, skillImportSourcePath)
	}
	if len(paths) != 1 || paths[0] != "/tmp/demo-skill" {
		t.Fatalf("paths=%v", paths)
	}
}

func TestNormalizeSkillImportParamsRejectsEscapingSourcePaths(t *testing.T) {
	_, _, err := normalizeSkillImportParams(skillImportParams{
		SourceType:  skillImportSourceGit,
		RepoURL:     "https://example.com/repo.git",
		SourcePaths: []string{"../outside"},
		TargetScope: skillImportScopeLocal,
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateImportedSkillReadsFrontmatterName(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "sample")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "---\nname: imported-skill\ndescription: demo\n---\nbody"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	name, err := validateImportedSkill(skillDir)
	if err != nil {
		t.Fatalf("validate imported skill: %v", err)
	}
	if name != "imported-skill" {
		t.Fatalf("name=%q want imported-skill", name)
	}
}

func TestPrepareTargetDirConflictStrategies(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}

	action, err := prepareTargetDir(targetDir, skillImportConflictSkip)
	if err != nil {
		t.Fatalf("skip conflict: %v", err)
	}
	if action != "skipped" {
		t.Fatalf("action=%q want skipped", action)
	}

	action, err = prepareTargetDir(targetDir, skillImportConflictOverwrite)
	if err != nil {
		t.Fatalf("overwrite conflict: %v", err)
	}
	if action != "overwritten" {
		t.Fatalf("action=%q want overwritten", action)
	}

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("recreate target dir: %v", err)
	}
	if _, err := prepareTargetDir(targetDir, skillImportConflictError); err == nil {
		t.Fatal("expected error conflict strategy to fail")
	}
}

func TestBuildSkillImportPreviewItemsReportsConflicts(t *testing.T) {
	root := t.TempDir()
	importDir := filepath.Join(root, "incoming", "demo-skill")
	if err := os.MkdirAll(importDir, 0o755); err != nil {
		t.Fatalf("mkdir import dir: %v", err)
	}
	targetRoot := filepath.Join(root, ".saker", "skills")
	if err := os.MkdirAll(filepath.Join(targetRoot, "demo-skill"), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	content := "---\nname: demo-skill\ndescription: demo\n---\nbody"
	if err := os.WriteFile(filepath.Join(importDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	items, err := buildSkillImportPreviewItems(skillImportSourcePath, []string{importDir}, "", targetRoot)
	if err != nil {
		t.Fatalf("build preview items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("unexpected preview items: %#v", items)
	}
	if items[0].Status != "conflict" {
		t.Fatalf("status=%q want conflict", items[0].Status)
	}
}
