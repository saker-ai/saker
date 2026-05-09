package middleware

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/cinience/saker/pkg/logging"
	"github.com/cinience/saker/pkg/memory"
)

// MemoryNudgeConfig controls when and how the memory nudge fires.
type MemoryNudgeConfig struct {
	Store       *memory.Store
	EveryNTurns int // fire every N agent turns (default 5)
}

// NewMemoryNudge creates a middleware that periodically extracts and persists
// knowledge from agent interactions into the memory store.
func NewMemoryNudge(cfg MemoryNudgeConfig) Middleware {
	if cfg.Store == nil {
		return nil
	}
	every := cfg.EveryNTurns
	if every <= 0 {
		every = 5
	}
	var turnCount atomic.Int64

	return Funcs{
		Identifier: "memory_nudge",
		OnAfterAgent: func(ctx context.Context, st *State) error {
			n := turnCount.Add(1)

			// Extract agent output text from state.
			output := extractAgentOutput(st)
			if output == "" {
				return nil
			}

			// Check triggers: periodic or keyword-based.
			triggered := n%int64(every) == 0
			if !triggered {
				triggered = containsMemoryKeyword(output)
			}
			if !triggered {
				return nil
			}

			// Extract and save memory entries.
			entries := extractMemoryEntries(output)
			for _, entry := range entries {
				if err := cfg.Store.Save(entry); err != nil {
					logging.From(ctx).Error("memory_nudge: save failed", "name", entry.Name, "error", err)
				}
			}
			return nil
		},
	}
}

// memoryKeywords are patterns that indicate knowledge worth persisting.
var memoryKeywords = []string{
	// English
	"learned that", "discovered that", "found that", "fixed bug",
	"key insight", "important:", "note:", "remember:",
	"the fix was", "root cause", "workaround:",
	// Chinese
	"发现了", "学到了", "记住", "重要:", "注意:",
	"根因是", "修复方法", "解决方案",
}

func containsMemoryKeyword(text string) bool {
	lower := strings.ToLower(text)
	for _, kw := range memoryKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// extractAgentOutput pulls text content from the agent's State.
func extractAgentOutput(st *State) string {
	if st == nil {
		return ""
	}
	// The agent output is typically stored in ModelOutput or Agent field.
	if st.ModelOutput != nil {
		if s, ok := st.ModelOutput.(string); ok {
			return s
		}
		if m, ok := st.ModelOutput.(map[string]any); ok {
			if content, ok := m["content"].(string); ok {
				return content
			}
		}
	}
	if st.Agent != nil {
		if s, ok := st.Agent.(string); ok {
			return s
		}
	}
	return ""
}

// extractMemoryEntries scans text for structured knowledge patterns and
// returns memory entries to persist.
func extractMemoryEntries(text string) []memory.Entry {
	var entries []memory.Entry
	lines := strings.Split(text, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)

		// Pattern: "Key insight: ..." or "Important: ..."
		for _, prefix := range []string{"key insight:", "important:", "note:", "remember:", "重要:", "注意:", "记住"} {
			if strings.HasPrefix(lower, prefix) {
				content := strings.TrimSpace(line[len(prefix):])
				if content == "" {
					continue
				}
				entries = append(entries, memory.Entry{
					Name:        slugify(content, 40),
					Description: truncate(content, 100),
					Type:        classifyMemory(content),
					Content:     content,
				})
			}
		}

		// Pattern: "The fix was ...", "Root cause: ..."
		for _, prefix := range []string{"the fix was", "root cause", "解决方案", "根因是", "修复方法"} {
			idx := strings.Index(lower, prefix)
			if idx >= 0 {
				content := strings.TrimSpace(line[idx:])
				entries = append(entries, memory.Entry{
					Name:        fmt.Sprintf("fix_%s", slugify(content, 30)),
					Description: truncate(content, 100),
					Type:        memory.MemoryTypeProject,
					Content:     content,
				})
				break
			}
		}
	}

	// Deduplicate by name.
	seen := map[string]bool{}
	unique := entries[:0]
	for _, e := range entries {
		if !seen[e.Name] {
			seen[e.Name] = true
			unique = append(unique, e)
		}
	}
	return unique
}

// classifyMemory guesses the memory type from content.
func classifyMemory(content string) memory.MemoryType {
	lower := strings.ToLower(content)
	switch {
	case strings.Contains(lower, "bug") || strings.Contains(lower, "fix") ||
		strings.Contains(lower, "deploy") || strings.Contains(lower, "release"):
		return memory.MemoryTypeProject
	case strings.Contains(lower, "prefer") || strings.Contains(lower, "don't") ||
		strings.Contains(lower, "always") || strings.Contains(lower, "never"):
		return memory.MemoryTypeFeedback
	case strings.Contains(lower, "doc") || strings.Contains(lower, "link") ||
		strings.Contains(lower, "url") || strings.Contains(lower, "api"):
		return memory.MemoryTypeReference
	default:
		return memory.MemoryTypeProject
	}
}

func slugify(s string, maxLen int) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var sb strings.Builder
	for _, r := range s {
		if sb.Len() >= maxLen {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			if sb.Len() > 0 {
				sb.WriteByte('_')
			}
		case r >= 0x4e00 && r <= 0x9fff: // CJK
			sb.WriteRune(r)
		}
	}
	result := strings.TrimRight(sb.String(), "_")
	if result == "" {
		result = "memory"
	}
	return result
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
