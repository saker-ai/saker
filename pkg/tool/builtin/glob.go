package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/saker-ai/saker/pkg/gitignore"
	sandboxenv "github.com/saker-ai/saker/pkg/sandbox/env"
	"github.com/saker-ai/saker/pkg/security"
	"github.com/saker-ai/saker/pkg/tool"
)

const (
	globResultLimit = 100
	globToolDesc    = `
		- Fast file pattern matching tool that works with any codebase size
		- Supports glob patterns like \"**/*.js\" or \"src/**/*.ts\"
		- Returns matching file paths sorted by modification time
		- Use this tool when you need to find files by name patterns
		- When you are doing an open ended search that may require multiple rounds of globbing and grepping, use the Agent tool instead
		- You can call multiple tools in a single response. It is always better to speculatively perform multiple searches in parallel if they are potentially useful.
	`
)

var globSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"pattern": map[string]interface{}{
			"type":        "string",
			"description": "The glob pattern to match files against",
		},
		"path": map[string]interface{}{
			"type":        "string",
			"description": "The directory to search in. If not specified, the current working directory will be used. IMPORTANT: Omit this field to use the default directory. DO NOT enter \"undefined\" or \"null\" - simply omit it for the default behavior. Must be a valid directory path if provided.",
		},
	},
	Required: []string{"pattern"},
}

// GlobTool looks up files via glob patterns.
type GlobTool struct {
	sandbox          *security.Sandbox
	root             string
	maxResults       int
	respectGitignore bool
	gitignoreMatcher *gitignore.Matcher
	env              sandboxenv.ExecutionEnvironment
}

// NewGlobTool builds a GlobTool rooted at the current directory.
func NewGlobTool() *GlobTool { return NewGlobToolWithRoot("") }

// NewGlobToolWithRoot builds a GlobTool rooted at the provided directory.
func NewGlobToolWithRoot(root string) *GlobTool {
	resolved := resolveRoot(root)
	return &GlobTool{
		sandbox:          security.NewSandbox(resolved),
		root:             resolved,
		maxResults:       globResultLimit,
		respectGitignore: true, // Default to respecting .gitignore
	}
}

// NewGlobToolWithSandbox builds a GlobTool using a custom sandbox.
func NewGlobToolWithSandbox(root string, sandbox *security.Sandbox) *GlobTool {
	resolved := resolveRoot(root)
	return &GlobTool{
		sandbox:          sandbox,
		root:             resolved,
		maxResults:       globResultLimit,
		respectGitignore: true, // Default to respecting .gitignore
	}
}

// SetRespectGitignore configures whether the tool should respect .gitignore patterns.
func (g *GlobTool) SetRespectGitignore(respect bool) {
	g.respectGitignore = respect
	if respect && g.gitignoreMatcher == nil {
		g.gitignoreMatcher, _ = gitignore.NewMatcher(g.root) //nolint:errcheck // best-effort gitignore
	}
}

func (g *GlobTool) Name() string { return "glob" }

func (g *GlobTool) Description() string { return globToolDesc }

func (g *GlobTool) Schema() *tool.JSONSchema { return globSchema }

func (g *GlobTool) SetEnvironment(env sandboxenv.ExecutionEnvironment) {
	if g != nil {
		g.env = env
	}
}

func (g *GlobTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if g == nil || g.sandbox == nil {
		return nil, errors.New("glob tool is not initialised")
	}

	pattern, err := parseGlobPattern(params)
	if err != nil {
		return nil, err
	}
	if g.env != nil {
		ps, err := g.env.PrepareSession(ctx, sandboxenv.SessionContext{
			SessionID:   bashSessionID(ctx),
			ProjectRoot: g.root,
		})
		if err != nil {
			return nil, err
		}
		if isVirtualizedSandboxSession(ps) {
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(ps.GuestCwd, pattern)
			}
			pattern = filepath.Clean(pattern)
			matches, err := g.env.Glob(ctx, ps, pattern)
			if err != nil {
				return nil, err
			}
			truncated := false
			if len(matches) > g.maxResults {
				matches = matches[:g.maxResults]
				truncated = true
			}
			return &tool.ToolResult{
				Success: true,
				Output:  formatGlobOutput(matches, truncated),
				Data: map[string]interface{}{
					"pattern":   pattern,
					"path":      ps.GuestCwd,
					"matches":   matches,
					"count":     len(matches),
					"truncated": truncated,
				},
			}, nil
		}
	}
	dir, err := g.resolveDir(params)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Reject patterns that attempt to escape the sandbox via "..".
	if strings.Contains(pattern, "..") {
		resolved := filepath.Clean(filepath.Join(dir, pattern))
		if err := g.sandbox.ValidatePath(resolved); err != nil {
			return nil, err
		}
	}

	// Prefer fd binary when available for better performance.
	if fdAvailable() {
		fdOpts := fdSearchOptions{
			respectGitignore: g.respectGitignore,
			maxResults:       g.maxResults,
		}
		if fdResults, fdTruncated, fdErr := fdSearch(ctx, pattern, dir, g.root, fdOpts); fdErr == nil {
			// Sort by modification time (newest first).
			sortByMtime(fdResults, g.root)
			data := map[string]interface{}{
				"pattern":   pattern,
				"path":      displayPath(dir, g.root),
				"matches":   fdResults,
				"count":     len(fdResults),
				"truncated": fdTruncated,
				"backend":   "fd",
			}
			return &tool.ToolResult{
				Success: true,
				Output:  formatGlobOutput(fdResults, fdTruncated),
				Data:    data,
			}, nil
		}
		// fd failed — fall through to pure-Go implementation.
	}

	// Initialize gitignore matcher lazily if needed
	if g.respectGitignore && g.gitignoreMatcher == nil {
		g.gitignoreMatcher, _ = gitignore.NewMatcher(g.root) //nolint:errcheck // best-effort gitignore
	}

	// Pure Go fallback: WalkDir with doublestar glob support.
	results, truncated := g.walkGlob(ctx, dir, pattern)

	// Sort by modification time (newest first).
	sortByMtime(results, g.root)

	return &tool.ToolResult{
		Success: true,
		Output:  formatGlobOutput(results, truncated),
		Data: map[string]interface{}{
			"pattern":   pattern,
			"path":      displayPath(dir, g.root),
			"matches":   results,
			"count":     len(results),
			"truncated": truncated,
		},
	}, nil
}

func parseGlobPattern(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}
	raw, ok := params["pattern"]
	if !ok {
		return "", errors.New("pattern is required")
	}
	pattern, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("pattern must be string: %w", err)
	}
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", errors.New("pattern cannot be empty")
	}
	return pattern, nil
}

func (g *GlobTool) resolveDir(params map[string]interface{}) (string, error) {
	dir := g.root
	if params != nil {
		if raw, ok := params["path"]; ok && raw != nil {
			value, err := coerceString(raw)
			if err != nil {
				return "", fmt.Errorf("path must be string: %w", err)
			}
			value = strings.TrimSpace(value)
			if value != "" {
				dir = expandHome(value)
			}
		}
	}

	if !filepath.IsAbs(dir) {
		dir = filepath.Join(g.root, dir)
	}
	dir = filepath.Clean(dir)
	if err := g.sandbox.ValidatePath(dir); err != nil {
		return "", err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("stat dir: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	return dir, nil
}

// walkGlob uses filepath.WalkDir with matchGlobPattern (supports **) to find files.
func (g *GlobTool) walkGlob(ctx context.Context, dir, pattern string) ([]string, bool) {
	var results []string
	truncated := false

	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// Skip symlink directories.
		if d.Type()&fs.ModeSymlink != 0 && d.IsDir() {
			return filepath.SkipDir
		}

		relPath := displayPath(path, g.root)

		// Auto-load nested .gitignore files.
		if d.IsDir() && g.gitignoreMatcher != nil && path != dir {
			gitignorePath := filepath.Join(path, ".gitignore")
			if _, statErr := os.Stat(gitignorePath); statErr == nil {
				_ = g.gitignoreMatcher.LoadNestedGitignore(relPath)
			}
		}

		// Filter out gitignored paths.
		if g.respectGitignore && g.gitignoreMatcher != nil {
			if g.gitignoreMatcher.Match(relPath, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		if d.IsDir() {
			return nil
		}

		// Match pattern against relative path.
		matched, err := matchGlobPattern(pattern, relPath)
		if err != nil || !matched {
			// Also try matching against basename for simple patterns.
			if !strings.Contains(pattern, "/") && !strings.Contains(pattern, "**") {
				matched, _ = filepath.Match(pattern, filepath.Base(path))
			}
		}
		if !matched {
			return nil
		}

		if err := g.sandbox.ValidatePath(filepath.Clean(path)); err != nil {
			return nil // skip invalid paths silently
		}

		results = append(results, relPath)
		if len(results) >= g.maxResults {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})

	return results, truncated
}

// sortByMtime sorts file paths by modification time (newest first).
func sortByMtime(paths []string, root string) {
	if len(paths) <= 1 {
		return
	}
	type pathMtime struct {
		path  string
		mtime int64
	}
	items := make([]pathMtime, len(paths))
	for i, p := range paths {
		absPath := p
		if !filepath.IsAbs(p) {
			absPath = filepath.Join(root, p)
		}
		var mt int64
		if info, err := os.Stat(absPath); err == nil {
			mt = info.ModTime().UnixNano()
		}
		items[i] = pathMtime{path: p, mtime: mt}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].mtime > items[j].mtime // newest first
	})
	for i, item := range items {
		paths[i] = item.path
	}
}

func formatGlobOutput(matches []string, truncated bool) string {
	if len(matches) == 0 {
		return "no matches"
	}
	output := strings.Join(matches, "\n")
	if truncated {
		output += fmt.Sprintf("\n... truncated to %d results", len(matches))
	}
	return output
}

func displayPath(path, root string) string {
	cleanPath := filepath.Clean(path)
	cleanRoot := filepath.Clean(root)
	if rel, err := filepath.Rel(cleanRoot, cleanPath); err == nil {
		switch {
		case rel == ".":
			return "."
		case strings.HasPrefix(rel, ".."):
			// Path escaped root; fall back to absolute path.
		default:
			return rel
		}
	}
	return cleanPath
}
