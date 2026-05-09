package toolbuiltin

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// fdOnce caches the result of exec.LookPath for fd/fdfind.
var (
	fdOnce   sync.Once
	fdPath   string
	fdExists bool
)

// fdAvailable returns true if fd (or fdfind on Debian/Ubuntu) is found in PATH.
func fdAvailable() bool {
	fdOnce.Do(func() {
		// Try "fd" first (upstream name), then "fdfind" (Debian/Ubuntu package name).
		for _, name := range []string{"fd", "fdfind"} {
			p, err := exec.LookPath(name)
			if err == nil {
				fdPath = p
				fdExists = true
				return
			}
		}
	})
	return fdExists
}

// fdSearchOptions holds parameters for an fd invocation.
type fdSearchOptions struct {
	respectGitignore bool
	maxResults       int
}

// fdSearch runs fd with a glob pattern and returns matching file paths (relative to root).
func fdSearch(ctx context.Context, pattern, dir, root string, opts fdSearchOptions) ([]string, bool, error) {
	if !fdAvailable() {
		return nil, false, fmt.Errorf("fd: not available")
	}

	args := buildFdArgs(pattern, dir, opts)

	cmd := exec.CommandContext(ctx, fdPath, args...)
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
		return nil, false, fmt.Errorf("fd: %w: %s", err, stderr.String())
	}

	results, truncated := parseFdOutput(stdout.Bytes(), root, opts.maxResults)
	return results, truncated, nil
}

// buildFdArgs constructs the fd command-line arguments.
func buildFdArgs(pattern, dir string, opts fdSearchOptions) []string {
	args := []string{
		"--glob",
		"--color", "never",
		"--no-require-git",
		"--type", "f", // files only
	}

	if !opts.respectGitignore {
		args = append(args, "--no-ignore")
	}

	if opts.maxResults > 0 {
		args = append(args, "--max-results", strconv.Itoa(opts.maxResults))
	}

	args = append(args, pattern, dir)
	return args
}

// parseFdOutput parses fd's stdout (one path per line) into relative paths.
func parseFdOutput(data []byte, root string, maxResults int) ([]string, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var results []string
	truncated := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		rel := relativizePath(line, root)
		results = append(results, rel)
		if maxResults > 0 && len(results) >= maxResults {
			truncated = true
			break
		}
	}

	return results, truncated
}
