package toolbuiltin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	"github.com/cinience/saker/pkg/security"
)

const defaultMaxFileBytes = 1 << 20 // 1 MiB

// fileSandbox enforces sandboxed filesystem operations shared by file tools.
type fileSandbox struct {
	sandbox  *security.Sandbox
	root     string
	maxBytes int64
	env      sandboxenv.ExecutionEnvironment
}

func newFileSandbox(root string) *fileSandbox {
	resolved := resolveRoot(root)
	return newFileSandboxWithSandbox(resolved, security.NewSandbox(resolved))
}

func newFileSandboxWithSandbox(root string, sandbox *security.Sandbox) *fileSandbox {
	return &fileSandbox{
		sandbox:  sandbox,
		root:     resolveRoot(root),
		maxBytes: defaultMaxFileBytes,
	}
}

func (f *fileSandbox) SetEnvironment(env sandboxenv.ExecutionEnvironment) {
	if f != nil {
		f.env = env
	}
}

func (f *fileSandbox) prepareSession(ctx context.Context) (*sandboxenv.PreparedSession, error) {
	if f == nil || f.env == nil {
		return nil, nil
	}
	return f.env.PrepareSession(ctx, sandboxenv.SessionContext{
		SessionID:   bashSessionID(ctx),
		ProjectRoot: f.root,
	})
}

func (f *fileSandbox) resolveGuestPath(raw interface{}, ps *sandboxenv.PreparedSession) (string, error) {
	if !isVirtualizedSandboxSession(ps) {
		return f.resolvePath(raw)
	}
	if raw == nil {
		return "", errors.New("path is required")
	}
	pathStr, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("path must be string: %w", err)
	}
	trimmed := strings.TrimSpace(pathStr)
	if trimmed == "" {
		return "", errors.New("path cannot be empty")
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed), nil
	}
	base := ps.GuestCwd
	if strings.TrimSpace(base) == "" {
		base = "/workspace"
	}
	return filepath.Clean(filepath.Join(base, trimmed)), nil
}

func isVirtualizedSandboxSession(ps *sandboxenv.PreparedSession) bool {
	if ps == nil {
		return false
	}
	switch ps.SandboxType {
	case "gvisor", "govm", "docker":
		return true
	default:
		return false
	}
}

func (f *fileSandbox) resolvePath(raw interface{}) (string, error) {
	if f == nil || f.sandbox == nil {
		return "", errors.New("file sandbox is not initialised")
	}
	if raw == nil {
		return "", errors.New("path is required")
	}
	pathStr, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("path must be string: %w", err)
	}
	trimmed := strings.TrimSpace(pathStr)
	if trimmed == "" {
		return "", errors.New("path cannot be empty")
	}
	candidate := expandHome(trimmed)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(f.root, candidate)
	}
	candidate = filepath.Clean(candidate)
	if err := f.sandbox.ValidatePath(candidate); err != nil {
		return "", err
	}
	return candidate, nil
}

func (f *fileSandbox) readFile(path string) (string, error) {
	if f == nil || f.sandbox == nil {
		return "", errors.New("file sandbox is not initialised")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	if f.maxBytes > 0 && info.Size() > f.maxBytes {
		return "", fmt.Errorf("file exceeds %d bytes limit", f.maxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	if f.maxBytes > 0 && int64(len(data)) > f.maxBytes {
		return "", fmt.Errorf("file exceeds %d bytes limit", f.maxBytes)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("binary file %s is not supported", path)
	}
	return string(data), nil
}

func (f *fileSandbox) writeFile(path string, content string) error {
	if f == nil || f.sandbox == nil {
		return errors.New("file sandbox is not initialised")
	}
	data := []byte(content)
	if f.maxBytes > 0 && int64(len(data)) > f.maxBytes {
		return fmt.Errorf("content exceeds %d bytes limit", f.maxBytes)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure directory: %w", err)
	}

	// Atomic write: create temp file, write, close, rename.
	tmp, err := os.CreateTemp(dir, ".saker-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // cleanup on any failure path

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// Preserve original file mode if it exists; for new files apply umask.
	if info, err := os.Stat(path); err == nil {
		os.Chmod(tmpPath, info.Mode())
	} else {
		os.Chmod(tmpPath, newFileMode()) //nolint:gosec // mode computed with umask applied
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
