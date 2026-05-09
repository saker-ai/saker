package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cinience/saker/pkg/message"
)

// PostCompactConfig controls what gets restored after compaction.
type PostCompactConfig struct {
	RestoreFiles      bool `json:"restore_files"`        // restore recently read files (default true when Enabled)
	MaxFilesToRestore int  `json:"max_files_to_restore"` // max files to restore (default 5)
	FileTokenBudget   int  `json:"file_token_budget"`    // token budget for file restoration (default 50000)
}

const (
	defaultMaxFilesToRestore = 5
	defaultFileTokenBudget   = 50000
)

func (c PostCompactConfig) withDefaults() PostCompactConfig {
	cfg := c
	if cfg.MaxFilesToRestore <= 0 {
		cfg.MaxFilesToRestore = defaultMaxFilesToRestore
	}
	if cfg.FileTokenBudget <= 0 {
		cfg.FileTokenBudget = defaultFileTokenBudget
	}
	return cfg
}

// extractRecentFilePaths scans messages for recent file read/write/edit tool calls
// and returns their file paths in reverse chronological order (most recent first).
func extractRecentFilePaths(msgs []message.Message, maxFiles int) []string {
	if maxFiles <= 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var paths []string
	for i := len(msgs) - 1; i >= 0 && len(paths) < maxFiles; i-- {
		for _, tc := range msgs[i].ToolCalls {
			name := strings.ToLower(tc.Name)
			if !strings.Contains(name, "read") && !strings.Contains(name, "write") && !strings.Contains(name, "edit") {
				continue
			}
			fp := extractFilePath(tc.Arguments)
			if fp == "" {
				continue
			}
			if _, ok := seen[fp]; ok {
				continue
			}
			seen[fp] = struct{}{}
			paths = append(paths, fp)
			if len(paths) >= maxFiles {
				break
			}
		}
	}
	return paths
}

// buildPostCompactMessages re-reads recently accessed files and builds
// system messages to restore context after compaction.
func buildPostCompactMessages(paths []string, tokenBudget int) []message.Message {
	if len(paths) == 0 || tokenBudget <= 0 {
		return nil
	}
	charBudget := tokenBudget * 4 // approximate 4 chars per token
	totalChars := 0
	var parts []string

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if totalChars+len(content) > charBudget {
			remaining := charBudget - totalChars
			if remaining > 200 {
				content = content[:remaining] + "\n... [truncated]"
			} else {
				break
			}
		}
		totalChars += len(content)
		parts = append(parts, fmt.Sprintf("### %s\n```\n%s\n```", filepath.Base(path), content))
	}
	if len(parts) == 0 {
		return nil
	}
	return []message.Message{{
		Role:    "system",
		Content: fmt.Sprintf("[Post-compact context restoration]\n\nRecently accessed files:\n\n%s", strings.Join(parts, "\n\n")),
	}}
}

// isPromptTooLong checks if an error indicates the prompt exceeds the model's context window.
func isPromptTooLong(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "prompt is too long") ||
		strings.Contains(msg, "too many tokens") ||
		strings.Contains(msg, "context length exceeded") ||
		strings.Contains(msg, "max_tokens") ||
		strings.Contains(msg, "token limit")
}
