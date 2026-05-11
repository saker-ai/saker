// runner_report.go: result/report data types, transcript and verifier-log writers.
package terminalbench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/message"
)

// TaskResult is one task's outcome.
type TaskResult struct {
	Name                string        `json:"name"`
	Category            string        `json:"category,omitempty"`
	Pass                bool          `json:"pass"`
	Score               float64       `json:"score"`
	Skipped             bool          `json:"skipped,omitempty"`
	SkipReason          string        `json:"skip_reason,omitempty"`
	Iterations          int           `json:"iterations,omitempty"`
	StartedAt           time.Time     `json:"started_at"`
	Duration            time.Duration `json:"duration_ns"`
	Stage               string        `json:"stage,omitempty"`
	ErrorMsg            string        `json:"error,omitempty"`
	VerifierLog         string        `json:"verifier_log,omitempty"`
	VerifierLogPath     string        `json:"verifier_log_path,omitempty"`
	VerifierRan         bool          `json:"verifier_ran,omitempty"`
	TestsPassed         int           `json:"tests_passed,omitempty"`
	TestsTotal          int           `json:"tests_total,omitempty"`
	InputTokens         int           `json:"input_tokens,omitempty"`
	OutputTokens        int           `json:"output_tokens,omitempty"`
	CacheReadTokens     int           `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int           `json:"cache_creation_tokens,omitempty"`
	IterationTokens     []TokenSample `json:"iteration_tokens,omitempty"`
	StopReason          string        `json:"stop_reason,omitempty"`
	ImageDigest         string        `json:"image_digest,omitempty"`
	TranscriptPath      string        `json:"transcript_path,omitempty"`
}

// TokenSample is one model.Generate call's contribution to cumulative usage.
// Captured per iteration so a regression analyst can spot context-window
// runaway, cache breakage, or a single unusually-large turn without rerunning.
type TokenSample struct {
	Iter          int `json:"iter"`
	Input         int `json:"input"`
	Output        int `json:"output"`
	CacheRead     int `json:"cache_read,omitempty"`
	CacheCreation int `json:"cache_creation,omitempty"`
}

// verifierLogInlineCap is the maximum bytes of verifier output we embed in
// results.jsonl. The full untruncated stream is persisted to
// <output>/transcripts/<task>.verifier.log via writeVerifierLog so we never
// lose pytest output to truncation again. 32 KiB keeps results.jsonl
// human-skimmable while still capturing the tail of most pytest sessions.
const verifierLogInlineCap = 32 * 1024

// CategoryScore aggregates results within one category.
type CategoryScore struct {
	Category string  `json:"category"`
	Total    int     `json:"total"`
	Passed   int     `json:"passed"`
	Skipped  int     `json:"skipped"`
	PassRate float64 `json:"pass_rate"`
}

// AggregateScore is the dataset-wide summary.
type AggregateScore struct {
	Total    int     `json:"total"`
	Passed   int     `json:"passed"`
	Failed   int     `json:"failed"`
	Errored  int     `json:"errored"`
	Skipped  int     `json:"skipped"`
	PassRate float64 `json:"pass_rate"`
}

// ConfigSnapshot is the subset of Config we persist alongside the report.
type ConfigSnapshot struct {
	Provider        string        `json:"provider,omitempty"`
	Model           string        `json:"model,omitempty"`
	Concurrency     int           `json:"concurrency"`
	MaxIterations   int           `json:"max_iterations"`
	MaxTokens       int           `json:"max_tokens,omitempty"`
	MaxBudgetUSD    float64       `json:"max_budget_usd,omitempty"`
	TaskTimeout     time.Duration `json:"task_timeout_ns"`
	TerminalTimeout time.Duration `json:"terminal_timeout_ns"`
	PullPolicy      string        `json:"pull_policy,omitempty"`
	NetworkMode     string        `json:"network_mode,omitempty"`
	// Build-identity fields. Help baseline diffs distinguish "agent code
	// changed" from "agent code identical, model behavior drifted".
	SakerCommit  string `json:"saker_commit,omitempty"`
	GoVersion    string `json:"go_version,omitempty"`
	WithMirror   bool   `json:"with_mirror,omitempty"`
	VerifierMirr bool   `json:"verifier_mirror,omitempty"`
	ProxyURL     string `json:"proxy_url,omitempty"`
}

// Report is the structured output written to report.json.
type Report struct {
	Dataset    string          `json:"dataset"`
	OutputDir  string          `json:"output_dir,omitempty"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	Duration   time.Duration   `json:"duration_ns"`
	Aggregate  AggregateScore  `json:"aggregate"`
	Categories []CategoryScore `json:"categories"`
	Results    []TaskResult    `json:"results"`
	Config     ConfigSnapshot  `json:"config"`
}

// writeTranscript dumps the conversation history of one task into
// <outputDir>/transcripts/<task>.jsonl. Returns the absolute path on
// success, "" when there is nothing to persist.
func writeTranscript(outputDir, taskName string, msgs []message.Message) (string, error) {
	if strings.TrimSpace(outputDir) == "" || strings.TrimSpace(taskName) == "" || len(msgs) == 0 {
		return "", nil
	}
	dir := filepath.Join(outputDir, "transcripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("transcripts dir: %w", err)
	}
	path := filepath.Join(dir, sanitizeTranscriptFilename(taskName)+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create transcript: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i, msg := range msgs {
		entry := map[string]any{
			"index":   i,
			"role":    msg.Role,
			"content": msg.Content,
		}
		if msg.ReasoningContent != "" {
			entry["reasoning_content"] = msg.ReasoningContent
		}
		if len(msg.ToolCalls) > 0 {
			entry["tool_calls"] = msg.ToolCalls
		}
		if err := enc.Encode(entry); err != nil {
			return "", fmt.Errorf("encode transcript line %d: %w", i, err)
		}
	}
	return path, nil
}

// writeVerifierLog persists the *full* untruncated verifier output (stdout
// merged with stderr) to <outputDir>/transcripts/<task>.verifier.log. Empty
// inputs return ("", nil) so callers can ignore the result without
// branching. Errors are returned but the runner deliberately treats them as
// non-fatal — losing a debug log shouldn't fail an otherwise passing task.
func writeVerifierLog(outputDir, taskName, full string) (string, error) {
	if strings.TrimSpace(outputDir) == "" || strings.TrimSpace(taskName) == "" || full == "" {
		return "", nil
	}
	dir := filepath.Join(outputDir, "transcripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("transcripts dir: %w", err)
	}
	path := filepath.Join(dir, sanitizeTranscriptFilename(taskName)+".verifier.log")
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		return "", fmt.Errorf("write verifier log: %w", err)
	}
	return path, nil
}

// tailBytes returns the last `cap` bytes of s, prefixed with a `...truncated
// (N bytes omitted)...\n` marker when truncation actually happens. Returns s
// unchanged when within the cap or when cap <= 0.
func tailBytes(s string, cap int) string {
	if cap <= 0 || len(s) <= cap {
		return s
	}
	skipped := len(s) - cap
	return fmt.Sprintf("...truncated (%d bytes omitted, see verifier_log_path for full output)...\n%s",
		skipped, s[skipped:])
}

// sanitizeTranscriptFilename replaces filesystem-hostile chars in a task name
// so it can be used as a flat filename. Keeps it readable for humans.
func sanitizeTranscriptFilename(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "task"
	}
	return string(out)
}
