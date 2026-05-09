package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/message"
)

func TestPostCompactConfig_withDefaults(t *testing.T) {
	cfg := PostCompactConfig{}.withDefaults()
	if cfg.MaxFilesToRestore != defaultMaxFilesToRestore {
		t.Errorf("MaxFilesToRestore = %d, want %d", cfg.MaxFilesToRestore, defaultMaxFilesToRestore)
	}
	if cfg.FileTokenBudget != defaultFileTokenBudget {
		t.Errorf("FileTokenBudget = %d, want %d", cfg.FileTokenBudget, defaultFileTokenBudget)
	}
}

func TestExtractRecentFilePaths(t *testing.T) {
	msgs := []message.Message{
		{Role: "user", Content: "read file"},
		{Role: "tool", ToolCalls: []message.ToolCall{{Name: "file_read", Arguments: map[string]any{"file_path": "/tmp/a.go"}}}},
		{Role: "tool", ToolCalls: []message.ToolCall{{Name: "bash", Arguments: map[string]any{"command": "ls"}}}},
		{Role: "tool", ToolCalls: []message.ToolCall{{Name: "file_write", Arguments: map[string]any{"file_path": "/tmp/b.go"}}}},
		{Role: "tool", ToolCalls: []message.ToolCall{{Name: "file_read", Arguments: map[string]any{"file_path": "/tmp/a.go"}}}}, // duplicate
	}
	paths := extractRecentFilePaths(msgs, 5)
	if len(paths) != 2 {
		t.Fatalf("got %d paths, want 2", len(paths))
	}
	// Reverse chronological: a.go found first (most recent), then b.go
	if paths[0] != "/tmp/a.go" {
		t.Errorf("paths[0] = %s, want /tmp/a.go", paths[0])
	}
	if paths[1] != "/tmp/b.go" {
		t.Errorf("paths[1] = %s, want /tmp/b.go", paths[1])
	}
}

func TestExtractRecentFilePaths_maxFiles(t *testing.T) {
	msgs := []message.Message{
		{Role: "tool", ToolCalls: []message.ToolCall{{Name: "file_read", Arguments: map[string]any{"file_path": "/a"}}}},
		{Role: "tool", ToolCalls: []message.ToolCall{{Name: "file_read", Arguments: map[string]any{"file_path": "/b"}}}},
		{Role: "tool", ToolCalls: []message.ToolCall{{Name: "file_read", Arguments: map[string]any{"file_path": "/c"}}}},
	}
	paths := extractRecentFilePaths(msgs, 2)
	if len(paths) != 2 {
		t.Fatalf("got %d paths, want 2", len(paths))
	}
}

func TestExtractRecentFilePaths_empty(t *testing.T) {
	paths := extractRecentFilePaths(nil, 5)
	if len(paths) != 0 {
		t.Errorf("expected empty, got %d", len(paths))
	}
}

func TestBuildPostCompactMessages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte("package main\nfunc main() {}"), 0644)

	msgs := buildPostCompactMessages([]string{path}, 10000)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("role = %s, want system", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "test.go") {
		t.Error("message should contain filename")
	}
	if !strings.Contains(msgs[0].Content, "package main") {
		t.Error("message should contain file content")
	}
}

func TestBuildPostCompactMessages_empty(t *testing.T) {
	msgs := buildPostCompactMessages(nil, 10000)
	if msgs != nil {
		t.Error("expected nil for empty paths")
	}
}

func TestBuildPostCompactMessages_tokenBudget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	os.WriteFile(path, []byte(strings.Repeat("x", 10000)), 0644)

	msgs := buildPostCompactMessages([]string{path}, 100) // very small budget: ~400 chars
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if len(msgs[0].Content) > 600 { // content + header overhead
		t.Errorf("content too large: %d chars", len(msgs[0].Content))
	}
}

func TestBuildPostCompactMessages_missingFile(t *testing.T) {
	msgs := buildPostCompactMessages([]string{"/nonexistent/file.go"}, 10000)
	if msgs != nil {
		t.Error("expected nil for missing file")
	}
}

func TestIsPromptTooLong(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"prompt is too long", true},
		{"too many tokens in request", true},
		{"context length exceeded", true},
		{"connection refused", false},
		{"", false},
	}
	for _, tt := range tests {
		var err error
		if tt.msg != "" {
			err = &testError{msg: tt.msg}
		}
		if got := isPromptTooLong(err); got != tt.want {
			t.Errorf("isPromptTooLong(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
