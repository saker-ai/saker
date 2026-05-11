// bash_exec.go: subprocess execution helpers - session prep, workdir/timeout resolution, async task glue.
package toolbuiltin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/middleware"
	"github.com/cinience/saker/pkg/model"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
)

func (b *BashTool) resolveWorkdir(params map[string]interface{}, ps *sandboxenv.PreparedSession) (string, error) {
	if isVirtualizedSandboxSession(ps) {
		dir := ps.GuestCwd
		if raw, ok := params["workdir"]; ok && raw != nil {
			value, err := coerceString(raw)
			if err != nil {
				return "", fmt.Errorf("workdir must be string: %w", err)
			}
			value = strings.TrimSpace(value)
			if value != "" {
				dir = expandHome(value)
			}
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(ps.GuestCwd, dir)
		}
		return filepath.Clean(dir), nil
	}
	dir := b.root
	if raw, ok := params["workdir"]; ok && raw != nil {
		value, err := coerceString(raw)
		if err != nil {
			return "", fmt.Errorf("workdir must be string: %w", err)
		}
		value = strings.TrimSpace(value)
		if value != "" {
			dir = expandHome(value)
		}
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(b.root, dir)
	}
	dir = filepath.Clean(dir)
	return b.ensureDirectory(dir)
}

func (b *BashTool) prepareSession(ctx context.Context) (*sandboxenv.PreparedSession, error) {
	if b == nil || b.env == nil {
		return nil, nil
	}
	return b.env.PrepareSession(ctx, sandboxenv.SessionContext{
		SessionID:   bashSessionID(ctx),
		ProjectRoot: b.root,
	})
}

func (b *BashTool) ensureDirectory(path string) (string, error) {
	if err := b.sandbox.ValidatePath(path); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("workdir stat: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workdir %s is not a directory", path)
	}
	return path, nil
}

func (b *BashTool) resolveTimeout(params map[string]interface{}) (time.Duration, error) {
	timeout := b.timeout
	raw, ok := params["timeout"]
	if !ok || raw == nil {
		return timeout, nil
	}
	dur, err := durationFromParam(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout: %w", err)
	}
	if dur == 0 {
		return timeout, nil
	}
	if dur > maxBashTimeout {
		dur = maxBashTimeout
	}
	return dur, nil
}

func parseAsyncFlag(params map[string]interface{}) (bool, error) {
	if params == nil {
		return false, nil
	}
	raw, ok := params["async"]
	if !ok || raw == nil {
		return false, nil
	}
	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		val := strings.TrimSpace(v)
		if val == "" {
			return false, nil
		}
		b, err := strconv.ParseBool(val)
		if err != nil {
			return false, fmt.Errorf("async must be boolean: %w", err)
		}
		return b, nil
	default:
		return false, fmt.Errorf("async must be boolean got %T", raw)
	}
}

func optionalAsyncTaskID(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", nil
	}
	raw, ok := params["task_id"]
	if !ok || raw == nil {
		return "", nil
	}
	value, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("task_id must be string: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("task_id cannot be empty")
	}
	return value, nil
}

func generateAsyncTaskID() string {
	var buf [4]byte
	if _, err := io.ReadFull(rand.Reader, buf[:]); err == nil {
		return "task-" + hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("task-%d", time.Now().UnixNano())
}

func bashSessionID(ctx context.Context) string {
	const fallback = "default"
	var session string
	if ctx != nil {
		if st, ok := ctx.Value(model.MiddlewareStateKey).(*middleware.State); ok && st != nil {
			if value, ok := st.Values["session_id"]; ok && value != nil {
				if s, err := coerceString(value); err == nil {
					session = s
				}
			}
			if session == "" {
				if value, ok := st.Values["trace.session_id"]; ok && value != nil {
					if s, err := coerceString(value); err == nil {
						session = s
					}
				}
			}
		}
		if session == "" {
			if value, ok := ctx.Value(middleware.TraceSessionIDContextKey).(string); ok {
				session = value
			} else if value, ok := ctx.Value(middleware.SessionIDContextKey).(string); ok {
				session = value
			}
		}
	}
	session = strings.TrimSpace(session)
	if session == "" {
		return fallback
	}
	return session
}
