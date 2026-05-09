package toolbuiltin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func skipIfNoRg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep (rg) not found in PATH, skipping")
	}
}

// --- Unit tests (no rg binary needed) ---

func TestRgSupportsType(t *testing.T) {
	for _, typ := range []string{"go", "py", "js", "rust", "java", "cpp"} {
		if !rgSupportsType(typ) {
			t.Errorf("expected rg to support type %q", typ)
		}
	}
	if rgSupportsType("zig") {
		t.Error("expected rg NOT to support type 'zig'")
	}
}

func TestBuildRgArgs(t *testing.T) {
	args := buildRgArgs("pattern", "/tmp/dir", rgSearchOptions{
		caseInsensitive: true,
		multiline:       true,
		glob:            "*.go",
		fileType:        "go",
		before:          2,
		after:           3,
	})

	assertContains(t, args, "--json")
	assertContains(t, args, "--no-config")
	assertContains(t, args, "--ignore-case")
	assertContains(t, args, "--multiline")
	assertContains(t, args, "--multiline-dotall")
	assertContains(t, args, "--glob")
	assertContains(t, args, "*.go")
	assertContains(t, args, "--type")
	assertContains(t, args, "go")
	assertContains(t, args, "--before-context")
	assertContains(t, args, "2")
	assertContains(t, args, "--after-context")
	assertContains(t, args, "3")
	assertContains(t, args, "--")
	assertContains(t, args, "pattern")
	assertContains(t, args, "/tmp/dir")
}

func TestBuildRgArgsUnsupportedType(t *testing.T) {
	args := buildRgArgs("pat", "/tmp", rgSearchOptions{
		fileType: "zig",
	})
	// Should use --glob fallback, not --type.
	assertContains(t, args, "--glob")
	assertContains(t, args, "*.zig")
	for _, a := range args {
		if a == "--type" {
			t.Error("should not use --type for unsupported type")
		}
	}
}

func TestParseRgJSON(t *testing.T) {
	jsonLines := []byte(
		`{"type":"begin","data":{"path":{"text":"/root/foo.go"}}}
{"type":"match","data":{"path":{"text":"/root/foo.go"},"lines":{"text":"hello world\n"},"line_number":3,"absolute_offset":20,"submatches":[{"match":{"text":"hello"},"start":0,"end":5}]}}
{"type":"end","data":{"path":{"text":"/root/foo.go"},"binary_offset":null,"stats":{}}}
{"type":"summary","data":{}}
`)
	matches, truncated := parseRgJSON(jsonLines, "/root", 100)
	if truncated {
		t.Error("expected not truncated")
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	m := matches[0]
	if m.File != "foo.go" {
		t.Errorf("expected file 'foo.go', got %q", m.File)
	}
	if m.Line != 3 {
		t.Errorf("expected line 3, got %d", m.Line)
	}
	if m.Match != "hello world" {
		t.Errorf("expected match 'hello world', got %q", m.Match)
	}
}

func TestParseRgJSONWithContext(t *testing.T) {
	jsonLines := []byte(
		`{"type":"begin","data":{"path":{"text":"/root/a.txt"}}}
{"type":"context","data":{"path":{"text":"/root/a.txt"},"lines":{"text":"before line\n"},"line_number":1}}
{"type":"match","data":{"path":{"text":"/root/a.txt"},"lines":{"text":"match line\n"},"line_number":2,"absolute_offset":12,"submatches":[{"match":{"text":"match"},"start":0,"end":5}]}}
{"type":"context","data":{"path":{"text":"/root/a.txt"},"lines":{"text":"after line\n"},"line_number":3}}
{"type":"end","data":{"path":{"text":"/root/a.txt"},"binary_offset":null,"stats":{}}}
`)
	matches, _ := parseRgJSON(jsonLines, "/root", 100)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	m := matches[0]
	if len(m.Before) != 1 || m.Before[0] != "before line" {
		t.Errorf("expected Before=['before line'], got %v", m.Before)
	}
	if len(m.After) != 1 || m.After[0] != "after line" {
		t.Errorf("expected After=['after line'], got %v", m.After)
	}
}

func TestParseRgJSONTruncation(t *testing.T) {
	jsonLines := []byte(
		`{"type":"begin","data":{"path":{"text":"/root/a.txt"}}}
{"type":"match","data":{"path":{"text":"/root/a.txt"},"lines":{"text":"line1\n"},"line_number":1,"absolute_offset":0,"submatches":[]}}
{"type":"match","data":{"path":{"text":"/root/a.txt"},"lines":{"text":"line2\n"},"line_number":2,"absolute_offset":6,"submatches":[]}}
{"type":"match","data":{"path":{"text":"/root/a.txt"},"lines":{"text":"line3\n"},"line_number":3,"absolute_offset":12,"submatches":[]}}
{"type":"end","data":{"path":{"text":"/root/a.txt"},"binary_offset":null,"stats":{}}}
`)
	matches, truncated := parseRgJSON(jsonLines, "/root", 2)
	if !truncated {
		t.Error("expected truncated=true")
	}
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matches))
	}
}

func TestParseRgJSONNoMatches(t *testing.T) {
	jsonLines := []byte(`{"type":"summary","data":{}}
`)
	matches, truncated := parseRgJSON(jsonLines, "/root", 100)
	if truncated {
		t.Error("expected not truncated")
	}
	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matches))
	}
}

func TestRelativizePath(t *testing.T) {
	tests := []struct {
		path, root, want string
	}{
		{"/home/user/project/foo.go", "/home/user/project", "foo.go"},
		{"/home/user/project/sub/bar.go", "/home/user/project", "sub/bar.go"},
		{"/other/path/file.go", "/home/user/project", "/other/path/file.go"},
		{"foo.go", "", "foo.go"},
	}
	for _, tt := range tests {
		got := relativizePath(tt.path, tt.root)
		if got != tt.want {
			t.Errorf("relativizePath(%q, %q) = %q, want %q", tt.path, tt.root, got, tt.want)
		}
	}
}

// --- Integration tests (require rg binary) ---

func TestRgSearchBasic(t *testing.T) {
	skipIfNoRg(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "hello.txt"), "hello world\nfoo bar\nhello again\n")

	matches, truncated, err := rgSearch(context.Background(), "hello", dir, dir, rgSearchOptions{
		maxResults: 100,
	})
	if err != nil {
		t.Fatalf("rgSearch failed: %v", err)
	}
	if truncated {
		t.Error("unexpected truncation")
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0].Line != 1 || matches[1].Line != 3 {
		t.Errorf("unexpected line numbers: %d, %d", matches[0].Line, matches[1].Line)
	}
}

func TestRgSearchNoMatch(t *testing.T) {
	skipIfNoRg(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "test.txt"), "foo bar baz\n")

	matches, truncated, err := rgSearch(context.Background(), "notfound", dir, dir, rgSearchOptions{
		maxResults: 100,
	})
	if err != nil {
		t.Fatalf("rgSearch failed: %v", err)
	}
	if truncated {
		t.Error("unexpected truncation")
	}
	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matches))
	}
}

func TestRgSearchCaseInsensitive(t *testing.T) {
	skipIfNoRg(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "test.txt"), "Hello World\nhello world\n")

	matches, _, err := rgSearch(context.Background(), "hello", dir, dir, rgSearchOptions{
		caseInsensitive: true,
		maxResults:      100,
	})
	if err != nil {
		t.Fatalf("rgSearch failed: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches with case-insensitive, got %d", len(matches))
	}
}

func TestRgSearchWithContext(t *testing.T) {
	skipIfNoRg(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "test.txt"), "line1\nline2\ntarget\nline4\nline5\n")

	matches, _, err := rgSearch(context.Background(), "target", dir, dir, rgSearchOptions{
		before:     1,
		after:      1,
		maxResults: 100,
	})
	if err != nil {
		t.Fatalf("rgSearch failed: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	m := matches[0]
	if len(m.Before) != 1 || m.Before[0] != "line2" {
		t.Errorf("expected Before=['line2'], got %v", m.Before)
	}
	if len(m.After) != 1 || m.After[0] != "line4" {
		t.Errorf("expected After=['line4'], got %v", m.After)
	}
}

func TestRgSearchGlobFilter(t *testing.T) {
	skipIfNoRg(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "main.go"), "hello from go\n")
	writeTestFile(t, filepath.Join(dir, "main.py"), "hello from python\n")

	matches, _, err := rgSearch(context.Background(), "hello", dir, dir, rgSearchOptions{
		glob:       "*.go",
		maxResults: 100,
	})
	if err != nil {
		t.Fatalf("rgSearch failed: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match (only .go), got %d", len(matches))
	}
	if matches[0].File != "main.go" {
		t.Errorf("expected file 'main.go', got %q", matches[0].File)
	}
}

func TestRgSearchTypeFilter(t *testing.T) {
	skipIfNoRg(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "main.go"), "hello from go\n")
	writeTestFile(t, filepath.Join(dir, "main.py"), "hello from python\n")

	matches, _, err := rgSearch(context.Background(), "hello", dir, dir, rgSearchOptions{
		fileType:   "go",
		maxResults: 100,
	})
	if err != nil {
		t.Fatalf("rgSearch failed: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match (only go type), got %d", len(matches))
	}
}

func TestRgSearchTruncation(t *testing.T) {
	skipIfNoRg(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "test.txt"), "match\nmatch\nmatch\nmatch\nmatch\n")

	matches, truncated, err := rgSearch(context.Background(), "match", dir, dir, rgSearchOptions{
		maxResults: 3,
	})
	if err != nil {
		t.Fatalf("rgSearch failed: %v", err)
	}
	if !truncated {
		t.Error("expected truncated=true")
	}
	if len(matches) != 3 {
		t.Errorf("expected 3 matches, got %d", len(matches))
	}
}

func TestRgSearchRespectGitignore(t *testing.T) {
	skipIfNoRg(t)

	dir := t.TempDir()
	// Initialize a git repo so rg respects .gitignore.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, ".gitignore"), "ignored/\n")
	writeTestFile(t, filepath.Join(dir, "visible.txt"), "hello\n")
	if err := os.MkdirAll(filepath.Join(dir, "ignored"), 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "ignored", "hidden.txt"), "hello\n")

	matches, _, err := rgSearch(context.Background(), "hello", dir, dir, rgSearchOptions{
		maxResults:       100,
		respectGitignore: true,
	})
	if err != nil {
		t.Fatalf("rgSearch failed: %v", err)
	}
	if len(matches) != 1 {
		t.Errorf("expected 1 match (gitignored file should be excluded), got %d", len(matches))
	}
	if len(matches) > 0 && matches[0].File != "visible.txt" {
		t.Errorf("expected 'visible.txt', got %q", matches[0].File)
	}
}

func TestGrepToolUsesRgBackend(t *testing.T) {
	skipIfNoRg(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "test.go"), "func main() {\n\tfmt.Println(\"hello\")\n}\n")

	tool := NewGrepToolWithRoot(dir)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"pattern":     "hello",
		"path":        dir,
		"output_mode": "content",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}
	// Verify the rg backend was used.
	if data, ok := result.Data.(map[string]interface{}); ok {
		if backend, ok := data["backend"]; ok {
			if backend != "ripgrep" {
				t.Errorf("expected backend 'ripgrep', got %v", backend)
			}
		}
	}
}

// --- Helpers ---

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func assertContains(t *testing.T, args []string, want string) {
	t.Helper()
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Errorf("args %v does not contain %q", args, want)
}
