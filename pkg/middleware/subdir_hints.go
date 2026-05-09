package middleware

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	subdirHintMaxSize  = 8192 // max bytes per hint file
	subdirHintMaxDirs  = 5    // max parent dirs to walk
	subdirHintStateKey = "subdir_hints_loaded"
)

// hintFileNames are the context files to look for in subdirectories.
var hintFileNames = []string{"AGENTS.md", "CLAUDE.md", ".cursorrules"}

// ToolCallInputExtractor reads the input map from State.ToolCall.
// Returns nil if the tool call cannot be interpreted.
type ToolCallInputExtractor func(toolCall any) map[string]any

// ToolResultAppender appends extra text to State.ToolResult's output.
type ToolResultAppender func(st *State, extra string)

// SubdirHintsConfig configures the subdirectory hints middleware.
type SubdirHintsConfig struct {
	WorkingDir     string
	ExtractInput   ToolCallInputExtractor
	AppendToResult ToolResultAppender
}

// NewSubdirHints creates middleware that appends context from AGENTS.md,
// CLAUDE.md, or .cursorrules files found in directories referenced by tool calls.
// It runs at the AfterTool stage and appends hints to the tool result output.
func NewSubdirHints(cfg SubdirHintsConfig) Middleware {
	workDir := cfg.WorkingDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	workDir, _ = filepath.Abs(workDir)

	var mu sync.Mutex
	loaded := map[string]bool{}

	return Funcs{
		Identifier: "subdir_hints",
		OnAfterTool: func(_ context.Context, st *State) error {
			if st.ToolCall == nil || cfg.ExtractInput == nil || cfg.AppendToResult == nil {
				return nil
			}

			input := cfg.ExtractInput(st.ToolCall)
			dirs := extractDirsFromInput(input, workDir)
			if len(dirs) == 0 {
				return nil
			}

			mu.Lock()
			defer mu.Unlock()

			var hints strings.Builder
			for _, dir := range dirs {
				walkDir := dir
				for i := 0; i < subdirHintMaxDirs; i++ {
					if loaded[walkDir] {
						break
					}
					loaded[walkDir] = true

					for _, name := range hintFileNames {
						fp := filepath.Join(walkDir, name)
						content, err := readHintFile(fp)
						if err != nil || content == "" {
							continue
						}
						hints.WriteString("\n\n--- [" + name + " from " + walkDir + "] ---\n")
						hints.WriteString(content)
					}

					parent := filepath.Dir(walkDir)
					if parent == walkDir || parent == workDir || !strings.HasPrefix(parent, workDir) {
						break
					}
					walkDir = parent
				}
			}

			if hints.Len() > 0 {
				cfg.AppendToResult(st, hints.String())
			}
			return nil
		},
	}
}

// extractDirsFromInput looks for file path references in a tool call's input map.
func extractDirsFromInput(input map[string]any, workDir string) []string {
	if len(input) == 0 {
		return nil
	}

	pathKeys := []string{"path", "file_path", "filePath", "file", "directory", "dir"}
	seen := map[string]bool{}
	var dirs []string

	for _, key := range pathKeys {
		if v, ok := input[key].(string); ok && v != "" {
			dir := resolveDir(v, workDir)
			if dir != "" && !seen[dir] && strings.HasPrefix(dir, workDir) {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
	}

	// Also extract paths from "command" field (bash tool).
	if cmd, ok := input["command"].(string); ok {
		for _, word := range strings.Fields(cmd) {
			if strings.Contains(word, "/") && !strings.HasPrefix(word, "-") {
				dir := resolveDir(word, workDir)
				if dir != "" && !seen[dir] && strings.HasPrefix(dir, workDir) {
					seen[dir] = true
					dirs = append(dirs, dir)
				}
			}
		}
	}

	return dirs
}

// resolveDir converts a file path to its containing directory, resolving relative paths.
func resolveDir(path, workDir string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workDir, path)
	}
	path = filepath.Clean(path)

	info, err := os.Stat(path)
	if err != nil {
		// Path doesn't exist; assume file if it has an extension.
		if filepath.Ext(path) != "" {
			return filepath.Dir(path)
		}
		return path
	}
	if !info.IsDir() {
		return filepath.Dir(path)
	}
	return path
}

// readHintFile reads a hint file, capping at subdirHintMaxSize bytes.
func readHintFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buf := make([]byte, subdirHintMaxSize)
	n, err := f.Read(buf)
	if n == 0 {
		return "", err
	}
	return string(buf[:n]), nil
}
