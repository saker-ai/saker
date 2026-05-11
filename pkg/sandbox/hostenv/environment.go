package hostenv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
)

// Environment is the host-native execution environment.
type Environment struct {
	projectRoot string
}

func New(projectRoot string) *Environment {
	return &Environment{projectRoot: projectRoot}
}

func (e *Environment) PrepareSession(_ context.Context, session sandboxenv.SessionContext) (*sandboxenv.PreparedSession, error) {
	cwd := e.projectRoot
	if cwd == "" {
		cwd = session.ProjectRoot
	}
	return &sandboxenv.PreparedSession{
		SessionID:   session.SessionID,
		GuestCwd:    cwd,
		SandboxType: "host",
	}, nil
}

func (e *Environment) RunCommand(context.Context, *sandboxenv.PreparedSession, sandboxenv.CommandRequest) (*sandboxenv.CommandResult, error) {
	return nil, errors.New("sandbox hostenv: command execution is not implemented")
}

func (e *Environment) ReadFile(_ context.Context, ps *sandboxenv.PreparedSession, path string) ([]byte, error) {
	resolved, err := cleanHostPath(e.sessionRoot(ps), path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(resolved)
}

func (e *Environment) WriteFile(_ context.Context, ps *sandboxenv.PreparedSession, path string, data []byte) error {
	resolved, err := cleanHostPath(e.sessionRoot(ps), path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return fmt.Errorf("hostenv: ensure directory: %w", err)
	}
	if err := os.WriteFile(resolved, data, 0o666); err != nil { //nolint:gosec // respect umask for created files
		return fmt.Errorf("hostenv: write file: %w", err)
	}
	return nil
}

func (e *Environment) EditFile(ctx context.Context, ps *sandboxenv.PreparedSession, req sandboxenv.EditRequest) error {
	resolved, err := cleanHostPath(e.sessionRoot(ps), req.Path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return err
	}
	content := string(data)
	if req.ReplaceAll {
		content = strings.ReplaceAll(content, req.OldText, req.NewText)
	} else {
		content = strings.Replace(content, req.OldText, req.NewText, 1)
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return fmt.Errorf("hostenv: ensure directory: %w", err)
	}
	if err := os.WriteFile(resolved, []byte(content), 0o666); err != nil { //nolint:gosec // respect umask for created files
		return fmt.Errorf("hostenv: write file: %w", err)
	}
	return nil
}

func (e *Environment) Glob(_ context.Context, ps *sandboxenv.PreparedSession, pattern string) ([]string, error) {
	resolved, err := cleanHostPath(e.sessionRoot(ps), pattern)
	if err != nil {
		return nil, err
	}
	return filepath.Glob(resolved)
}

func (e *Environment) Grep(_ context.Context, _ *sandboxenv.PreparedSession, _ sandboxenv.GrepRequest) ([]sandboxenv.GrepMatch, error) {
	return nil, errors.New("sandbox hostenv: grep is not implemented")
}

func (e *Environment) CloseSession(_ context.Context, _ *sandboxenv.PreparedSession) error {
	return nil
}

// sessionRoot prefers the prepared session's GuestCwd (per-session view) over
// the environment-wide projectRoot. Falling back to projectRoot keeps callers
// that bypass PrepareSession working, but the calling code is encouraged to
// always pass a PreparedSession so each session is sandboxed.
func (e *Environment) sessionRoot(ps *sandboxenv.PreparedSession) string {
	if ps != nil && ps.GuestCwd != "" {
		return ps.GuestCwd
	}
	return e.projectRoot
}

// cleanHostPath resolves path relative to root and rejects any result that
// escapes root via "..", absolute paths, or symlinks. An empty root disables
// the escape check (callers without a project context still get path cleanup
// but no sandbox guarantee — log loudly if this branch fires in production).
func cleanHostPath(root, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("hostenv: empty path")
	}

	var resolved string
	if filepath.IsAbs(path) {
		resolved = filepath.Clean(path)
	} else {
		base := root
		if base == "" {
			base = "."
		}
		resolved = filepath.Clean(filepath.Join(base, path))
	}

	if root == "" {
		return resolved, nil
	}

	cleanRoot := filepath.Clean(root)
	rel, err := filepath.Rel(cleanRoot, resolved)
	if err != nil {
		return "", fmt.Errorf("hostenv: cannot relativize %q against root %q: %w", path, cleanRoot, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("hostenv: path %q escapes project root %q", path, cleanRoot)
	}

	// Defensive: if the resolved path exists, ensure its symlink-resolved form
	// also lives within root. New files (ENOENT) are accepted for Write.
	if eval, err := filepath.EvalSymlinks(resolved); err == nil {
		evalRel, err := filepath.Rel(cleanRoot, eval)
		if err != nil || evalRel == ".." || strings.HasPrefix(evalRel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("hostenv: path %q resolves outside project root via symlink", path)
		}
	}

	return resolved, nil
}
