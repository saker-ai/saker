package middleware

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractDirsFromInput(t *testing.T) {
	t.Parallel()

	workDir := "/project"

	tests := []struct {
		name     string
		input    map[string]any
		expected int // number of dirs extracted
	}{
		{"nil input", nil, 0},
		{"empty input", map[string]any{}, 0},
		{"path key", map[string]any{"path": "/project/src/main.go"}, 1},
		{"file_path key", map[string]any{"file_path": "/project/pkg/foo.go"}, 1},
		{"command with path", map[string]any{"command": "cat /project/src/main.go"}, 1},
		{"outside workdir", map[string]any{"path": "/other/file.go"}, 0},
		{"relative path", map[string]any{"path": "src/main.go"}, 1},
		{"no paths in command", map[string]any{"command": "echo hello"}, 0},
		{"flag ignored", map[string]any{"command": "--flag --other"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dirs := extractDirsFromInput(tt.input, workDir)
			if len(dirs) != tt.expected {
				t.Errorf("extractDirsFromInput(%v) returned %d dirs, want %d: %v",
					tt.input, len(dirs), tt.expected, dirs)
			}
		})
	}
}

func TestResolveDir(t *testing.T) {
	t.Parallel()

	// Create a temp dir with a file in it.
	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "test.go")
	_ = os.WriteFile(testFile, []byte("package main"), 0644)
	subDir := filepath.Join(tmp, "subdir")
	_ = os.MkdirAll(subDir, 0755)

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{"existing file", testFile, tmp},
		{"existing dir", subDir, subDir},
		{"nonexistent file", filepath.Join(tmp, "noexist.go"), tmp},
		{"nonexistent dir", filepath.Join(tmp, "nodir"), filepath.Join(tmp, "nodir")},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolveDir(tt.path, tmp)
			if got != tt.expected {
				t.Errorf("resolveDir(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

func TestNewSubdirHints_Integration(t *testing.T) {
	t.Parallel()

	// Setup: create a temp project with a hint file.
	tmp := t.TempDir()
	subDir := filepath.Join(tmp, "pkg", "feature")
	_ = os.MkdirAll(subDir, 0755)
	hintContent := "# Feature Guidelines\nUse interfaces."
	_ = os.WriteFile(filepath.Join(subDir, "CLAUDE.md"), []byte(hintContent), 0644)

	var appended string
	mw := NewSubdirHints(SubdirHintsConfig{
		WorkingDir: tmp,
		ExtractInput: func(tc any) map[string]any {
			if m, ok := tc.(map[string]any); ok {
				return m
			}
			return nil
		},
		AppendToResult: func(st *State, extra string) {
			appended += extra
		},
	})

	st := &State{
		ToolCall:   map[string]any{"path": filepath.Join(subDir, "foo.go")},
		ToolResult: "original output",
		Values:     map[string]any{},
	}

	err := mw.AfterTool(context.Background(), st)
	if err != nil {
		t.Fatalf("AfterTool error: %v", err)
	}

	if !strings.Contains(appended, "Feature Guidelines") {
		t.Errorf("expected hint content in appended output, got: %q", appended)
	}

	// Second call with same dir should not append again.
	appended = ""
	err = mw.AfterTool(context.Background(), st)
	if err != nil {
		t.Fatalf("second AfterTool error: %v", err)
	}
	if appended != "" {
		t.Errorf("expected no hints on second call (already loaded), got: %q", appended)
	}
}

func TestReadHintFile(t *testing.T) {
	t.Parallel()

	t.Run("existing file", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		fp := filepath.Join(tmp, "CLAUDE.md")
		_ = os.WriteFile(fp, []byte("hello world"), 0644)

		content, err := readHintFile(fp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if content != "hello world" {
			t.Errorf("got %q, want %q", content, "hello world")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		_, err := readHintFile("/nonexistent/CLAUDE.md")
		if err == nil {
			t.Error("expected error for missing file")
		}
	})
}
