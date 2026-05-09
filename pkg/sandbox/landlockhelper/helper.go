//go:build linux

package landlockhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/landlock-lsm/go-landlock/landlock"
)

// Run executes one helper request from stdin and writes a response to stdout.
func Run(ctx context.Context, stdin io.Reader, stdout io.Writer, _ io.Writer) error {
	var req Request
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
		return fmt.Errorf("decode landlock helper request: %w", err)
	}
	resp := ExecuteRequest(ctx, req)
	if err := json.NewEncoder(stdout).Encode(resp); err != nil {
		return fmt.Errorf("encode landlock helper response: %w", err)
	}
	return nil
}

// Invoke executes helper mode via self-exec when possible. Test binaries fall
// back to in-process execution because they do not expose the hidden helper
// flag through the generated test main.
func Invoke(ctx context.Context, req Request, helperFlag string) (Response, error) {
	exe, err := os.Executable()
	if err != nil {
		return ExecuteRequest(ctx, req), nil
	}
	if strings.HasSuffix(filepath.Base(exe), ".test") {
		return ExecuteRequest(ctx, req), nil
	}
	if strings.TrimSpace(helperFlag) == "" {
		helperFlag = "--saker-landlock-helper"
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("marshal landlock helper request: %w", err)
	}

	cmd := exec.CommandContext(ctx, exe, helperFlag)
	cmd.Stdin = bytes.NewReader(payload)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return Response{}, fmt.Errorf("run landlock helper: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return Response{}, fmt.Errorf("run landlock helper: %w", err)
	}

	var resp Response
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		if stderr.Len() > 0 {
			return Response{}, fmt.Errorf("decode landlock helper response: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return Response{}, fmt.Errorf("decode landlock helper response: %w", err)
	}
	return resp, nil
}

// ExecuteRequest applies Landlock restrictions and runs the command.
func ExecuteRequest(ctx context.Context, req Request) Response {
	execCtx := ctx
	cancel := func() {}
	if req.TimeoutMs > 0 {
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMs)*time.Millisecond)
	}
	defer cancel()

	// Build Landlock rules.
	var rules []landlock.Rule
	for _, p := range req.ROPaths {
		rules = append(rules, landlock.RODirs(p))
	}
	for _, p := range req.RWPaths {
		rules = append(rules, landlock.RWDirs(p))
	}
	// Default system paths (read-only).
	rules = append(rules,
		landlock.RODirs("/usr", "/bin", "/lib", "/lib64", "/etc", "/proc"),
		landlock.ROFiles("/dev/null", "/dev/urandom", "/dev/zero"),
	)

	// Apply Landlock (irreversible). BestEffort degrades gracefully on older kernels.
	_ = landlock.V5.BestEffort().RestrictPaths(rules...)

	cmd := exec.CommandContext(execCtx, "bash", "-c", req.Command)
	if req.GuestCwd != "" {
		cmd.Dir = req.GuestCwd
	}
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	err := cmd.Run()
	resp := Response{
		Success:    err == nil,
		Stdout:     stdoutBuf.String(),
		Stderr:     stderrBuf.String(),
		DurationMs: time.Since(start).Milliseconds(),
	}
	if err == nil {
		return resp
	}
	resp.Error = err.Error()
	if exitErr, ok := err.(*exec.ExitError); ok {
		resp.ExitCode = exitErr.ExitCode()
	} else if execCtx.Err() != nil {
		resp.ExitCode = -1
	}
	return resp
}
