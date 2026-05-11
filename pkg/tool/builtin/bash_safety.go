// bash_safety.go: parameter parsing, path coercion, root resolution, and secret redaction.
package toolbuiltin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func extractCommand(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}
	raw, ok := params["command"]
	if !ok {
		// 提供更详细的错误信息帮助调试
		keys := make([]string, 0, len(params))
		for k := range params {
			keys = append(keys, k)
		}
		if len(keys) == 0 {
			return "", errors.New("command is required (params is empty)")
		}
		return "", fmt.Errorf("command is required (got params with keys: %v)", keys)
	}
	cmd, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("command must be string: %w", err)
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", errors.New("command cannot be empty")
	}
	return cmd, nil
}

func coerceString(value interface{}) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case fmt.Stringer:
		return v.String(), nil
	case []byte:
		return string(v), nil
	default:
		return "", fmt.Errorf("expected string got %T", value)
	}
}

// expandHome expands a leading ~ or ~/ in a path to the user's home directory.
// This mirrors shell tilde expansion so that paths like ~/Downloads work in
// tool parameters (Go's filepath package does not do this automatically).
func expandHome(path string) string {
	if path == "~" {
		if h, err := os.UserHomeDir(); err == nil {
			return h
		}
		return path
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, path[2:])
		}
	}
	return path
}

func durationFromParam(value interface{}) (time.Duration, error) {
	switch v := value.(type) {
	case time.Duration:
		if v < 0 {
			return 0, errors.New("duration cannot be negative")
		}
		return v, nil
	case float64:
		return secondsToDuration(v)
	case float32:
		return secondsToDuration(float64(v))
	case int:
		return secondsToDuration(float64(v))
	case int64:
		return secondsToDuration(float64(v))
	case uint:
		return secondsToDuration(float64(v))
	case uint64:
		return secondsToDuration(float64(v))
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, err
		}
		return secondsToDuration(f)
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, nil
		}
		if strings.ContainsAny(trimmed, "hms") {
			return time.ParseDuration(trimmed)
		}
		f, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, err
		}
		return secondsToDuration(f)
	default:
		return 0, fmt.Errorf("unsupported duration type %T", value)
	}
}

func secondsToDuration(seconds float64) (time.Duration, error) {
	if seconds < 0 {
		return 0, errors.New("duration cannot be negative")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func resolveRoot(dir string) string {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		if cwd, err := os.Getwd(); err == nil {
			trimmed = cwd
		} else {
			trimmed = "."
		}
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		return abs
	}
	return filepath.Clean(trimmed)
}

func sanitizePathComponent(value string) string {
	const fallback = "default"
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	sanitized := strings.Trim(b.String(), "-")
	if sanitized == "" {
		return fallback
	}
	return sanitized
}

// Secret redaction patterns compiled once at init time.
var (
	// Matches standalone secret tokens: sk-xxx, key-xxx (20+ alphanumeric chars).
	reSecretToken = regexp.MustCompile(`\b(sk-[a-zA-Z0-9]{20,})\b`)
	// Matches env var assignments like API_KEY=value, SECRET_KEY: value, AUTH_TOKEN=value.
	reEnvSecret = regexp.MustCompile(`(?i)(API_KEY|SECRET_KEY|AUTH_TOKEN|ACCESS_TOKEN|PRIVATE_KEY|SECRET)([=:]\s*)([^\s"']{8,})`)
)

// redactSecrets masks common secret patterns in bash output to prevent
// accidental leakage of API keys and tokens into session history.
func redactSecrets(output string) string {
	if output == "" {
		return output
	}
	result := reSecretToken.ReplaceAllString(output, "sk-***")
	result = reEnvSecret.ReplaceAllString(result, "${1}${2}***")
	return result
}
