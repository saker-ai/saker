package landlockenv

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	"github.com/cinience/saker/pkg/sandbox/landlockhelper"
)

// Environment is the Landlock-backed execution environment.
type Environment struct {
	projectRoot string
	opts        *sandboxenv.LandlockOptions
}

// New creates a Landlock execution environment.
func New(projectRoot string, opts *sandboxenv.LandlockOptions) *Environment {
	return &Environment{projectRoot: projectRoot, opts: opts}
}

func (e *Environment) PrepareSession(ctx context.Context, session sandboxenv.SessionContext) (*sandboxenv.PreparedSession, error) {
	return prepareSession(ctx, e.projectRoot, e.opts, session)
}

func (e *Environment) RunCommand(ctx context.Context, ps *sandboxenv.PreparedSession, req sandboxenv.CommandRequest) (*sandboxenv.CommandResult, error) {
	roPaths, rwPaths := pathsFromSession(ps)
	workdir := req.Workdir
	if strings.TrimSpace(workdir) == "" {
		workdir = ps.GuestCwd
	}

	resp, err := landlockhelper.Invoke(ctx, landlockhelper.Request{
		Version:   "v1",
		SessionID: ps.SessionID,
		Command:   req.Command,
		GuestCwd:  workdir,
		TimeoutMs: req.Timeout.Milliseconds(),
		Env:       req.Env,
		ROPaths:   roPaths,
		RWPaths:   rwPaths,
	}, e.helperFlag())
	if err != nil {
		return nil, err
	}
	result := &sandboxenv.CommandResult{
		Stdout:   resp.Stdout,
		Stderr:   resp.Stderr,
		ExitCode: resp.ExitCode,
		Duration: time.Duration(resp.DurationMs) * time.Millisecond,
	}
	if !resp.Success && resp.Error != "" {
		return result, fmt.Errorf("landlockenv: command failed: %s", resp.Error)
	}
	return result, nil
}

func (e *Environment) RunCommandStream(ctx context.Context, ps *sandboxenv.PreparedSession, req sandboxenv.CommandRequest, cb sandboxenv.CommandStreamCallbacks) (*sandboxenv.CommandResult, error) {
	// Landlock helper uses buffered I/O; stream callbacks get final output.
	result, err := e.RunCommand(ctx, ps, req)
	if err != nil {
		return result, err
	}
	if cb.OnStdout != nil && result.Stdout != "" {
		cb.OnStdout(result.Stdout)
	}
	if cb.OnStderr != nil && result.Stderr != "" {
		cb.OnStderr(result.Stderr)
	}
	return result, nil
}

func (e *Environment) ReadFile(_ context.Context, ps *sandboxenv.PreparedSession, path string) ([]byte, error) {
	hostPath := resolvePath(ps, path)
	if err := checkReadAccess(ps, hostPath); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(hostPath)
	if err != nil {
		return nil, fmt.Errorf("landlockenv: read file: %w", err)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("binary file %s is not supported", path)
	}
	return data, nil
}

func (e *Environment) WriteFile(_ context.Context, ps *sandboxenv.PreparedSession, path string, data []byte) error {
	hostPath := resolvePath(ps, path)
	if err := checkWriteAccess(ps, hostPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return fmt.Errorf("landlockenv: ensure directory: %w", err)
	}
	if err := os.WriteFile(hostPath, data, 0o666); err != nil { //nolint:gosec // respect umask
		return fmt.Errorf("landlockenv: write file: %w", err)
	}
	return nil
}

func (e *Environment) EditFile(ctx context.Context, ps *sandboxenv.PreparedSession, req sandboxenv.EditRequest) error {
	hostPath := resolvePath(ps, req.Path)
	if err := checkWriteAccess(ps, hostPath); err != nil {
		return err
	}
	data, err := e.ReadFile(ctx, ps, req.Path)
	if err != nil {
		return err
	}
	content := string(data)
	if req.ReplaceAll {
		content = strings.ReplaceAll(content, req.OldText, req.NewText)
	} else {
		content = strings.Replace(content, req.OldText, req.NewText, 1)
	}
	return e.WriteFile(ctx, ps, req.Path, []byte(content))
}

func (e *Environment) Glob(ctx context.Context, ps *sandboxenv.PreparedSession, pattern string) ([]string, error) {
	root := e.projectRoot
	if err := checkReadAccess(ps, root); err != nil {
		return nil, err
	}
	pattern = filepath.Clean(pattern)
	var matches []string
	walkErr := filepath.Walk(root, func(hostPath string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		ok, err := filepath.Match(pattern, hostPath)
		if err == nil && ok {
			matches = append(matches, hostPath)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return matches, nil
}

func (e *Environment) Grep(ctx context.Context, ps *sandboxenv.PreparedSession, req sandboxenv.GrepRequest) ([]sandboxenv.GrepMatch, error) {
	re, err := regexp.Compile(req.Pattern)
	if err != nil {
		return nil, fmt.Errorf("landlockenv: compile grep pattern: %w", err)
	}
	root := req.Path
	if root == "" {
		root = e.projectRoot
	}
	if err := checkReadAccess(ps, root); err != nil {
		return nil, err
	}
	var matches []sandboxenv.GrepMatch
	walkErr := filepath.Walk(root, func(hostPath string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(hostPath)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			matches = append(matches, sandboxenv.GrepMatch{
				Path:    hostPath,
				Line:    i + 1,
				Column:  1,
				Preview: strings.TrimRight(line, "\r"),
			})
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return matches, nil
}

func (e *Environment) CloseSession(context.Context, *sandboxenv.PreparedSession) error {
	return nil
}

func (e *Environment) helperFlag() string {
	if e == nil || e.opts == nil || strings.TrimSpace(e.opts.HelperModeFlag) == "" {
		return "--saker-landlock-helper"
	}
	return e.opts.HelperModeFlag
}

func pathsFromSession(ps *sandboxenv.PreparedSession) (roPaths, rwPaths []string) {
	if ps == nil || ps.Meta == nil {
		return nil, nil
	}
	if v, ok := ps.Meta["ro_paths"].([]string); ok {
		roPaths = v
	}
	if v, ok := ps.Meta["rw_paths"].([]string); ok {
		rwPaths = v
	}
	return
}

func resolvePath(ps *sandboxenv.PreparedSession, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if ps != nil && ps.GuestCwd != "" {
		return filepath.Join(ps.GuestCwd, path)
	}
	return path
}
