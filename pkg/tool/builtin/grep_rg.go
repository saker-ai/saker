package toolbuiltin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// rgOnce caches the result of exec.LookPath("rg").
var (
	rgOnce   sync.Once
	rgPath   string
	rgExists bool
)

// rgAvailable returns true if ripgrep is found in PATH.
func rgAvailable() bool {
	rgOnce.Do(func() {
		p, err := exec.LookPath("rg")
		if err == nil {
			rgPath = p
			rgExists = true
		}
	})
	return rgExists
}

// rg JSON output types.
type rgMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type rgMatchData struct {
	Path       rgText       `json:"path"`
	Lines      rgText       `json:"lines"`
	LineNum    int          `json:"line_number"`
	Submatches []rgSubmatch `json:"submatches"`
}

type rgContextData struct {
	Path    rgText `json:"path"`
	Lines   rgText `json:"lines"`
	LineNum int    `json:"line_number"`
}

type rgText struct {
	Text string `json:"text"`
}

type rgSubmatch struct {
	Match rgText `json:"match"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// rgSearchOptions holds parameters for an rg invocation.
type rgSearchOptions struct {
	caseInsensitive  bool
	multiline        bool
	glob             string
	fileType         string
	before           int
	after            int
	maxResults       int
	respectGitignore bool
}

// rgSearch runs ripgrep with --json and returns parsed matches.
// It returns (matches, truncated, error). On rg execution failure, it returns
// a non-nil error so the caller can fall back to the pure-Go implementation.
func rgSearch(ctx context.Context, pattern, searchPath, root string, opts rgSearchOptions) ([]GrepMatch, bool, error) {
	if !rgAvailable() {
		return nil, false, fmt.Errorf("rg: not available")
	}
	args := buildRgArgs(pattern, searchPath, opts)

	cmd := exec.CommandContext(ctx, rgPath, args...)
	cmd.Dir = root

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// Exit code 1 means no matches — not an error.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, false, nil
		}
		// Exit code 2 means rg encountered an error (e.g., bad regex).
		// Any other failure: return error so caller can fallback.
		return nil, false, fmt.Errorf("rg: %w: %s", err, stderr.String())
	}

	matches, truncated := parseRgJSON(stdout.Bytes(), root, opts.maxResults)
	return matches, truncated, nil
}

// buildRgArgs constructs the rg command-line arguments.
func buildRgArgs(pattern, searchPath string, opts rgSearchOptions) []string {
	args := []string{
		"--json",
		"--no-config",      // ignore user's .ripgreprc
		"--no-require-git", // respect .gitignore even without .git dir
	}

	if !opts.respectGitignore {
		args = append(args, "--no-ignore")
	}

	if opts.caseInsensitive {
		args = append(args, "--ignore-case")
	}
	if opts.multiline {
		args = append(args, "--multiline", "--multiline-dotall")
	}
	if opts.glob != "" {
		args = append(args, "--glob", opts.glob)
	}
	if opts.fileType != "" {
		if rgSupportsType(opts.fileType) {
			args = append(args, "--type", opts.fileType)
		} else {
			// Fallback: convert type to glob pattern.
			args = append(args, "--glob", "*."+opts.fileType)
		}
	}
	if opts.before > 0 {
		args = append(args, "--before-context", strconv.Itoa(opts.before))
	}
	if opts.after > 0 {
		args = append(args, "--after-context", strconv.Itoa(opts.after))
	}

	args = append(args, "--", pattern, searchPath)
	return args
}

// rgSupportsType returns true if rg has a built-in type definition.
var rgBuiltinTypes = map[string]bool{
	"go": true, "py": true, "python": true, "js": true, "ts": true,
	"tsx": true, "jsx": true, "rust": true, "java": true, "c": true,
	"cpp": true, "h": true, "hpp": true, "rb": true, "ruby": true,
	"php": true, "cs": true, "swift": true, "sh": true, "kotlin": true,
	"kt": true, "css": true, "html": true, "json": true, "yaml": true,
	"xml": true, "sql": true, "md": true, "markdown": true, "txt": true,
}

func rgSupportsType(t string) bool {
	return rgBuiltinTypes[t]
}

// parseRgJSON parses rg --json output into GrepMatch slices.
// Context lines are associated with their nearest match.
func parseRgJSON(data []byte, root string, maxResults int) ([]GrepMatch, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Increase buffer for potentially long lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var matches []GrepMatch
	// pendingContext holds context lines before the next match.
	var pendingBefore []string
	truncated := false

	for scanner.Scan() {
		var msg rgMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "match":
			var md rgMatchData
			if err := json.Unmarshal(msg.Data, &md); err != nil {
				continue
			}
			relPath := relativizePath(md.Path.Text, root)
			matchLine := strings.TrimRight(md.Lines.Text, "\r\n")

			m := GrepMatch{
				File:  relPath,
				Line:  md.LineNum,
				Match: matchLine,
			}
			if len(pendingBefore) > 0 {
				m.Before = pendingBefore
				pendingBefore = nil
			}
			matches = append(matches, m)

			if maxResults > 0 && len(matches) >= maxResults {
				truncated = true
				// Attach remaining context to last match, then stop.
				attachTrailingContext(scanner, &matches[len(matches)-1], root)
				return matches, truncated
			}

		case "context":
			var cd rgContextData
			if err := json.Unmarshal(msg.Data, &cd); err != nil {
				continue
			}
			contextLine := strings.TrimRight(cd.Lines.Text, "\r\n")

			// Determine if this is before-context or after-context.
			// If we have matches and this context line comes right after the last match,
			// it's after-context. Otherwise, it's before-context for the next match.
			if len(matches) > 0 {
				last := &matches[len(matches)-1]
				expectedAfterLine := last.Line + 1 + len(last.After)
				if cd.LineNum == expectedAfterLine && relativizePath(cd.Path.Text, root) == last.File {
					last.After = append(last.After, contextLine)
					continue
				}
			}
			pendingBefore = append(pendingBefore, contextLine)

		case "begin":
			// New file — reset pending context.
			pendingBefore = nil
		}
	}

	return matches, truncated
}

// attachTrailingContext reads remaining scanner lines to attach after-context
// to the last match after truncation.
func attachTrailingContext(scanner *bufio.Scanner, last *GrepMatch, root string) {
	for scanner.Scan() {
		var msg rgMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Type != "context" {
			return
		}
		var cd rgContextData
		if err := json.Unmarshal(msg.Data, &cd); err != nil {
			return
		}
		expectedLine := last.Line + 1 + len(last.After)
		if cd.LineNum == expectedLine && relativizePath(cd.Path.Text, root) == last.File {
			last.After = append(last.After, strings.TrimRight(cd.Lines.Text, "\r\n"))
		} else {
			return
		}
	}
}

// relativizePath converts an absolute path to a path relative to root.
func relativizePath(path, root string) string {
	if root == "" {
		return path
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	if strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}
