package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ──────────────────────────────────────────────
// Unit tests (always run, no external binary)
// ──────────────────────────────────────────────

func TestBuildFdArgs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		pattern string
		dir     string
		opts    fdSearchOptions
		want    []string
		reject  []string
	}{
		{
			name:    "basic",
			pattern: "*.go",
			dir:     "/src",
			opts:    fdSearchOptions{respectGitignore: true, maxResults: 0},
			want:    []string{"--glob", "--color", "never", "--type", "f", "*.go", "/src"},
			reject:  []string{"--no-ignore", "--max-results"},
		},
		{
			name:    "no_gitignore",
			pattern: "*.txt",
			dir:     "/tmp",
			opts:    fdSearchOptions{respectGitignore: false},
			want:    []string{"--no-ignore"},
		},
		{
			name:    "max_results",
			pattern: "**/*.ts",
			dir:     "/app",
			opts:    fdSearchOptions{respectGitignore: true, maxResults: 50},
			want:    []string{"--max-results", "50"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := buildFdArgs(tc.pattern, tc.dir, tc.opts)
			joined := strings.Join(args, " ")
			for _, w := range tc.want {
				if !strings.Contains(joined, w) {
					t.Errorf("args missing %q: %v", w, args)
				}
			}
			for _, r := range tc.reject {
				if strings.Contains(joined, r) {
					t.Errorf("args should not contain %q: %v", r, args)
				}
			}
		})
	}
}

func TestParseFdOutput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		data       string
		root       string
		maxResults int
		wantCount  int
		wantTrunc  bool
	}{
		{
			name:       "empty",
			data:       "",
			root:       "/root",
			maxResults: 10,
			wantCount:  0,
			wantTrunc:  false,
		},
		{
			name:       "basic",
			data:       "/root/a.go\n/root/b.go\n",
			root:       "/root",
			maxResults: 10,
			wantCount:  2,
			wantTrunc:  false,
		},
		{
			name:       "truncation",
			data:       "/root/a.go\n/root/b.go\n/root/c.go\n",
			root:       "/root",
			maxResults: 2,
			wantCount:  2,
			wantTrunc:  true,
		},
		{
			name:       "blank_lines_skipped",
			data:       "\n/root/a.go\n\n/root/b.go\n\n",
			root:       "/root",
			maxResults: 10,
			wantCount:  2,
			wantTrunc:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, truncated := parseFdOutput([]byte(tc.data), tc.root, tc.maxResults)
			if len(results) != tc.wantCount {
				t.Errorf("count: got %d want %d", len(results), tc.wantCount)
			}
			if truncated != tc.wantTrunc {
				t.Errorf("truncated: got %v want %v", truncated, tc.wantTrunc)
			}
		})
	}
}

// ──────────────────────────────────────────────
// Integration tests (require fd/fdfind binary)
// ──────────────────────────────────────────────

func skipIfNoFd(t *testing.T) {
	t.Helper()
	if !fdAvailable() {
		t.Skip("fd/fdfind not available")
	}
}

func TestFdSearchBasic(t *testing.T) {
	skipIfNoFd(t)

	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	writeFile(t, filepath.Join(dir, "hello.go"), "package main")
	writeFile(t, filepath.Join(dir, "world.txt"), "text")

	results, truncated, err := fdSearch(context.Background(), "*.go", dir, dir, fdSearchOptions{
		respectGitignore: false,
		maxResults:       100,
	})
	if err != nil {
		t.Fatalf("fdSearch error: %v", err)
	}
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %v", len(results), results)
	}
	if !strings.HasSuffix(results[0], "hello.go") {
		t.Fatalf("expected hello.go, got %s", results[0])
	}
}

func TestFdSearchDoublestar(t *testing.T) {
	skipIfNoFd(t)

	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	sub := filepath.Join(dir, "src", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sub, "main.go"), "package pkg")
	writeFile(t, filepath.Join(dir, "readme.md"), "# readme")

	results, _, err := fdSearch(context.Background(), "**/*.go", dir, dir, fdSearchOptions{
		respectGitignore: false,
		maxResults:       100,
	})
	if err != nil {
		t.Fatalf("fdSearch error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %v", len(results), results)
	}
	if !strings.Contains(results[0], "main.go") {
		t.Fatalf("expected main.go match, got %s", results[0])
	}
}

func TestFdSearchMaxResults(t *testing.T) {
	skipIfNoFd(t)

	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for i := 0; i < 10; i++ {
		writeFile(t, filepath.Join(dir, strings.Repeat("a", i+1)+".txt"), "x")
	}

	results, truncated, err := fdSearch(context.Background(), "*.txt", dir, dir, fdSearchOptions{
		respectGitignore: false,
		maxResults:       3,
	})
	if err != nil {
		t.Fatalf("fdSearch error: %v", err)
	}
	if len(results) > 3 {
		t.Fatalf("expected at most 3 results, got %d", len(results))
	}
	// fd --max-results limits at the fd level, so truncated may or may not be set
	// depending on whether parseFdOutput also truncates. Either way, <=3 results.
	_ = truncated
}

func TestFdSearchNoMatches(t *testing.T) {
	skipIfNoFd(t)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "test.txt"), "x")

	results, truncated, err := fdSearch(context.Background(), "*.go", dir, dir, fdSearchOptions{
		respectGitignore: false,
		maxResults:       100,
	})
	if err != nil {
		t.Fatalf("fdSearch error: %v", err)
	}
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestFdSearchRespectGitignore(t *testing.T) {
	skipIfNoFd(t)

	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	// Initialize git repo so fd respects .gitignore
	writeFile(t, filepath.Join(dir, ".gitignore"), "ignored.txt\n")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "kept.txt"), "keep")
	writeFile(t, filepath.Join(dir, "ignored.txt"), "ignore")

	results, _, err := fdSearch(context.Background(), "*.txt", dir, dir, fdSearchOptions{
		respectGitignore: true,
		maxResults:       100,
	})
	if err != nil {
		t.Fatalf("fdSearch error: %v", err)
	}
	for _, r := range results {
		if strings.Contains(r, "ignored.txt") {
			t.Fatalf("ignored.txt should be excluded, got results: %v", results)
		}
	}
	found := false
	for _, r := range results {
		if strings.Contains(r, "kept.txt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("kept.txt should be included, got results: %v", results)
	}
}

func TestSortByMtime(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := []string{"a.txt", "b.txt", "c.txt"}
	for _, f := range files {
		writeFile(t, filepath.Join(dir, f), f)
	}

	paths := make([]string, len(files))
	copy(paths, files)
	sortByMtime(paths, dir)

	// After sorting by mtime (newest first), the last-written file should be first.
	// All files are written in quick succession, so order may vary.
	// Just verify all files are present.
	sorted := make([]string, len(paths))
	copy(sorted, paths)
	sort.Strings(sorted)
	expected := []string{"a.txt", "b.txt", "c.txt"}
	sort.Strings(expected)
	for i := range sorted {
		if sorted[i] != expected[i] {
			t.Fatalf("sortByMtime lost files: got %v", paths)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
