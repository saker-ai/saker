package dockerenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	sandboxenv "github.com/saker-ai/saker/pkg/sandbox/env"
)

// environment_io.go covers everything the agent runs against an existing
// container: shell commands (sync + streaming), file IO, glob/grep, and tar
// archive uploads. Lifecycle and helpers are in sibling files.

// RunCommand executes /bin/sh -lc <cmd> inside the session's container.
func (e *Environment) RunCommand(ctx context.Context, ps *sandboxenv.PreparedSession, req sandboxenv.CommandRequest) (*sandboxenv.CommandResult, error) {
	state, err := e.session(ps)
	if err != nil {
		return nil, err
	}
	workdir := strings.TrimSpace(req.Workdir)
	if workdir == "" {
		workdir = state.workdir
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = e.cfg.DefaultTimeout
	}
	res, err := e.execInContainer(ctx, state.containerID, workdir, timeout, req.Env, "/bin/sh", "-lc", req.Command)
	if err != nil {
		return nil, err
	}
	return &sandboxenv.CommandResult{
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
		Duration: res.Duration,
	}, nil
}

// RunCommandStream is like RunCommand but pipes stdout/stderr deltas.
func (e *Environment) RunCommandStream(ctx context.Context, ps *sandboxenv.PreparedSession, req sandboxenv.CommandRequest, cb sandboxenv.CommandStreamCallbacks) (*sandboxenv.CommandResult, error) {
	state, err := e.session(ps)
	if err != nil {
		return nil, err
	}
	workdir := strings.TrimSpace(req.Workdir)
	if workdir == "" {
		workdir = state.workdir
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = e.cfg.DefaultTimeout
	}
	argv := buildExecArgv(state.containerID, workdir, req.Env, "/bin/sh", "-lc", req.Command)
	res, err := e.cmd.Stream(ctx, argv, nil, timeout, cb.OnStdout, cb.OnStderr)
	if err != nil {
		return nil, err
	}
	return &sandboxenv.CommandResult{
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
		Duration: res.Duration,
	}, nil
}

// ReadFile cats the file via `docker exec`. Stdout is the file body; we
// detect binary content by scanning for NUL like other env impls.
func (e *Environment) ReadFile(ctx context.Context, ps *sandboxenv.PreparedSession, path string) ([]byte, error) {
	state, err := e.session(ps)
	if err != nil {
		return nil, err
	}
	guestPath := normalizeGuestPath(path, state.workdir)
	res, err := e.execInContainer(ctx, state.containerID, "/", e.cfg.DefaultTimeout, nil, "cat", "--", guestPath)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("dockerenv: read file %s: exit %d: %s", guestPath, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	data := []byte(res.Stdout)
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("binary file %s is not supported", path)
	}
	return data, nil
}

// WriteFile pipes `data` into `tee <guestPath>` running inside the container.
// Creates parent dirs first.
func (e *Environment) WriteFile(ctx context.Context, ps *sandboxenv.PreparedSession, path string, data []byte) error {
	state, err := e.session(ps)
	if err != nil {
		return err
	}
	guestPath := normalizeGuestPath(path, state.workdir)
	parent := filepath.Dir(guestPath)
	if parent != "" && parent != "." {
		if res, mkErr := e.execInContainer(ctx, state.containerID, "/", e.cfg.DefaultTimeout, nil, "mkdir", "-p", parent); mkErr != nil {
			return fmt.Errorf("dockerenv: ensure parent dir: %w", mkErr)
		} else if res.ExitCode != 0 {
			return fmt.Errorf("dockerenv: ensure parent dir %s: exit %d: %s", parent, res.ExitCode, strings.TrimSpace(res.Stderr))
		}
	}
	argv := buildExecArgv(state.containerID, "/", nil, "sh", "-c", "cat > "+shellQuote(guestPath))
	// Prefix with `-i` for stdin attach.
	argv = injectInteractive(argv)
	res, err := e.cmd.Run(ctx, argv, bytes.NewReader(data), e.cfg.DefaultTimeout)
	if err != nil {
		return fmt.Errorf("dockerenv: write file: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("dockerenv: write file %s: exit %d: %s", guestPath, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// EditFile reads, replaces, writes — same pattern as other env impls.
func (e *Environment) EditFile(ctx context.Context, ps *sandboxenv.PreparedSession, req sandboxenv.EditRequest) error {
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

// Glob delegates to `find -path` inside the container; returns guest paths.
func (e *Environment) Glob(ctx context.Context, ps *sandboxenv.PreparedSession, pattern string) ([]string, error) {
	state, err := e.session(ps)
	if err != nil {
		return nil, err
	}
	guestPattern := pattern
	if !strings.HasPrefix(guestPattern, "/") {
		guestPattern = filepath.Join(state.workdir, guestPattern)
	}
	guestPattern = filepath.Clean(guestPattern)
	root := guestRootForPattern(guestPattern)
	cmd := fmt.Sprintf("find %s -path %s -print 2>/dev/null", shellQuote(root), shellQuote(guestPattern))
	res, err := e.execInContainer(ctx, state.containerID, "/", e.cfg.DefaultTimeout, nil, "/bin/sh", "-c", cmd)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 && strings.TrimSpace(res.Stderr) != "" {
		return nil, fmt.Errorf("dockerenv: glob: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	var matches []string
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		matches = append(matches, line)
	}
	return matches, nil
}

// Grep uses `grep -nrE` inside the container and parses path:line:preview.
func (e *Environment) Grep(ctx context.Context, ps *sandboxenv.PreparedSession, req sandboxenv.GrepRequest) ([]sandboxenv.GrepMatch, error) {
	state, err := e.session(ps)
	if err != nil {
		return nil, err
	}
	root := strings.TrimSpace(req.Path)
	if root == "" {
		root = state.workdir
	}
	flags := "-nrI"
	if !req.CaseSensitive {
		flags += "i"
	}
	if !req.Literal {
		flags += "E"
	} else {
		flags += "F"
	}
	cmd := fmt.Sprintf("grep %s -- %s %s 2>/dev/null || true",
		flags,
		shellQuote(req.Pattern),
		shellQuote(root))
	res, err := e.execInContainer(ctx, state.containerID, "/", e.cfg.DefaultTimeout, nil, "/bin/sh", "-c", cmd)
	if err != nil {
		return nil, err
	}
	var matches []sandboxenv.GrepMatch
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		// path:line:preview
		first := strings.IndexByte(line, ':')
		if first < 0 {
			continue
		}
		rest := line[first+1:]
		second := strings.IndexByte(rest, ':')
		if second < 0 {
			continue
		}
		path := line[:first]
		lineNum, convErr := strconv.Atoi(rest[:second])
		if convErr != nil {
			continue
		}
		preview := rest[second+1:]
		matches = append(matches, sandboxenv.GrepMatch{
			Path:    path,
			Line:    lineNum,
			Column:  1,
			Preview: preview,
		})
	}
	return matches, nil
}

// CopyArchiveTo streams a tar archive into `<destDir>` of the container.
// Used by the TB2 runner to upload environment.tar / tests.tar without having
// to land them on the host first. destDir must already exist.
func (e *Environment) CopyArchiveTo(ctx context.Context, ps *sandboxenv.PreparedSession, destDir string, archive io.Reader) error {
	state, err := e.session(ps)
	if err != nil {
		return err
	}
	if strings.TrimSpace(destDir) == "" {
		return errors.New("dockerenv: destDir is required")
	}
	// Ensure destDir exists; tar -xf - relies on it.
	if res, mkErr := e.execInContainer(ctx, state.containerID, "/", e.cfg.DefaultTimeout, nil, "mkdir", "-p", destDir); mkErr != nil {
		return fmt.Errorf("dockerenv: ensure dest dir: %w", mkErr)
	} else if res.ExitCode != 0 {
		return fmt.Errorf("dockerenv: ensure dest dir %s: exit %d: %s", destDir, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	argv := buildExecArgv(state.containerID, "/", nil, "tar", "-xf", "-", "-C", destDir)
	argv = injectInteractive(argv)
	res, err := e.cmd.Run(ctx, argv, archive, e.cfg.DefaultTimeout)
	if err != nil {
		return fmt.Errorf("dockerenv: extract archive: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("dockerenv: extract archive: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}
