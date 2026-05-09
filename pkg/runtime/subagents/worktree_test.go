package subagents

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", dir},
		{"-C", dir, "config", "user.email", "test@test.com"},
		{"-C", dir, "config", "user.name", "Test"},
		{"-C", dir, "commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	return dir
}

func TestWorktreeManager_CreateAndRemove(t *testing.T) {
	repoDir := initTestRepo(t)
	baseDir := filepath.Join(t.TempDir(), "worktrees")

	mgr, err := NewWorktreeManager(repoDir, baseDir)
	if err != nil {
		t.Fatalf("NewWorktreeManager: %v", err)
	}

	// Create
	path, branch, err := mgr.Create("test-wt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if path != filepath.Join(baseDir, "test-wt") {
		t.Errorf("unexpected path: %s", path)
	}
	if branch != "worktree/test-wt" {
		t.Errorf("unexpected branch: %s", branch)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("worktree dir should exist: %v", err)
	}

	// Duplicate create should fail
	if _, _, err := mgr.Create("test-wt"); err == nil {
		t.Error("expected error for duplicate worktree")
	}

	// HasChanges on clean worktree
	hasChanges, err := mgr.HasChanges("test-wt")
	if err != nil {
		t.Fatalf("HasChanges: %v", err)
	}
	if hasChanges {
		t.Error("expected no changes in fresh worktree")
	}

	// Create a file to make changes
	if err := os.WriteFile(filepath.Join(path, "test.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	hasChanges, err = mgr.HasChanges("test-wt")
	if err != nil {
		t.Fatalf("HasChanges after file: %v", err)
	}
	if !hasChanges {
		t.Error("expected changes after creating file")
	}

	// List
	names, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 1 || names[0] != "test-wt" {
		t.Errorf("unexpected list: %v", names)
	}

	// Remove with force (has uncommitted changes)
	if err := mgr.Remove("test-wt", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("worktree dir should be removed")
	}

	// Remove nonexistent
	if err := mgr.Remove("nonexistent", false); err == nil {
		t.Error("expected error for nonexistent worktree")
	}
}

func TestWorktreeManager_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewWorktreeManager(dir, filepath.Join(dir, "wt")); err == nil {
		t.Error("expected error for non-git directory")
	}
}

func TestWorktreeManager_EmptyName(t *testing.T) {
	repoDir := initTestRepo(t)
	mgr, _ := NewWorktreeManager(repoDir, filepath.Join(t.TempDir(), "wt"))
	if _, _, err := mgr.Create(""); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestWorktreeManager_HasChangesNotFound(t *testing.T) {
	repoDir := initTestRepo(t)
	mgr, _ := NewWorktreeManager(repoDir, filepath.Join(t.TempDir(), "wt"))
	if _, err := mgr.HasChanges("nonexistent"); err == nil {
		t.Error("expected error for nonexistent worktree")
	}
}
