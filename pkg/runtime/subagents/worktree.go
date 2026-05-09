package subagents

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	ErrNotGitRepo       = errors.New("subagents: not a git repository")
	ErrWorktreeExists   = errors.New("subagents: worktree already exists")
	ErrWorktreeNotFound = errors.New("subagents: worktree not found")
)

// WorktreeManager creates and cleans up git worktrees for isolated subagent execution.
type WorktreeManager struct {
	BaseDir string // directory where worktrees are created (e.g., .saker/worktrees/)
	RepoDir string // root of the git repository
}

// NewWorktreeManager creates a WorktreeManager for the given repo and base directory.
func NewWorktreeManager(repoDir, baseDir string) (*WorktreeManager, error) {
	// Verify we're in a git repo.
	cmd := exec.Command("git", "-C", repoDir, "rev-parse", "--git-dir")
	if err := cmd.Run(); err != nil {
		return nil, ErrNotGitRepo
	}
	return &WorktreeManager{BaseDir: baseDir, RepoDir: repoDir}, nil
}

// Create creates a new git worktree with the given name and returns the
// worktree path and branch name. The branch is based on HEAD.
func (w *WorktreeManager) Create(name string) (worktreePath string, branch string, err error) {
	if name == "" {
		return "", "", errors.New("worktree name is required")
	}

	worktreePath = filepath.Join(w.BaseDir, name)
	if _, err := os.Stat(worktreePath); err == nil {
		return "", "", ErrWorktreeExists
	}

	if err := os.MkdirAll(w.BaseDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create worktree base dir: %w", err)
	}

	branch = "worktree/" + name
	cmd := exec.Command("git", "-C", w.RepoDir, "worktree", "add", "-b", branch, worktreePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("git worktree add: %s: %w", strings.TrimSpace(string(output)), err)
	}

	return worktreePath, branch, nil
}

// Remove removes a worktree. If force is true, it removes even if there are
// uncommitted changes.
func (w *WorktreeManager) Remove(name string, force bool) error {
	worktreePath := filepath.Join(w.BaseDir, name)
	if _, err := os.Stat(worktreePath); errors.Is(err, os.ErrNotExist) {
		return ErrWorktreeNotFound
	}

	args := []string{"-C", w.RepoDir, "worktree", "remove", worktreePath}
	if force {
		args = append(args, "--force")
	}
	cmd := exec.Command("git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove: %s: %w", strings.TrimSpace(string(output)), err)
	}

	// Clean up the branch
	branch := "worktree/" + name
	deleteCmd := exec.Command("git", "-C", w.RepoDir, "branch", "-D", branch)
	_ = deleteCmd.Run() // best-effort branch cleanup

	return nil
}

// HasChanges checks if the worktree has any uncommitted changes.
func (w *WorktreeManager) HasChanges(name string) (bool, error) {
	worktreePath := filepath.Join(w.BaseDir, name)
	if _, err := os.Stat(worktreePath); errors.Is(err, os.ErrNotExist) {
		return false, ErrWorktreeNotFound
	}

	cmd := exec.Command("git", "-C", worktreePath, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return len(strings.TrimSpace(string(output))) > 0, nil
}

// List returns the names of all worktrees in the base directory.
func (w *WorktreeManager) List() ([]string, error) {
	entries, err := os.ReadDir(w.BaseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read worktree dir: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}
