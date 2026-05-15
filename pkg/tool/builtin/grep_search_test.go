package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saker-ai/saker/pkg/security"
)

func TestGrepToolExecuteContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tool := NewGrepToolWithSandbox(root, security.NewDisabledSandbox())
	tool.SetRespectGitignore(false)

	res, err := tool.Execute(context.Background(), map[string]interface{}{
		"pattern":     "hello",
		"output_mode": "content",
		"path":        root,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Success || !strings.Contains(res.Output, "hello") {
		t.Fatalf("unexpected output %q", res.Output)
	}
}

func TestGrepSearchOptionsAllow(t *testing.T) {
	t.Parallel()

	// Both glob and type match .go — allow.
	opts := grepSearchOptions{
		glob:      "*.go",
		typeGlobs: []string{"*.go"},
		root:      "/root",
	}
	ok, err := opts.allow("/root/file.go")
	if err != nil || !ok {
		t.Fatalf("expected allow, got %v err=%v", ok, err)
	}
	// Neither glob nor type match .txt — deny.
	ok, err = opts.allow("/root/file.txt")
	if err != nil || ok {
		t.Fatalf("expected deny, got %v err=%v", ok, err)
	}

	// Glob overrides type (consistent with rg): glob=*.go type=js — only glob matters.
	overrideOpts := grepSearchOptions{
		glob:      "*.go",
		typeGlobs: []string{"*.js"},
		root:      "/root",
	}
	ok, err = overrideOpts.allow("/root/file.go")
	if err != nil || !ok {
		t.Fatalf("glob override: expected allow for .go (glob match), got %v err=%v", ok, err)
	}
	// .js matches type but glob takes precedence — deny.
	ok, err = overrideOpts.allow("/root/file.js")
	if err != nil || ok {
		t.Fatalf("glob override: expected deny for .js (glob overrides type), got %v err=%v", ok, err)
	}
	// .txt matches neither — deny.
	ok, err = overrideOpts.allow("/root/file.txt")
	if err != nil || ok {
		t.Fatalf("glob override: expected deny for .txt, got %v err=%v", ok, err)
	}
}

func TestGrepGlobDoublestar(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sub := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "main.go"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepToolWithSandbox(root, security.NewDisabledSandbox())
	tool.SetRespectGitignore(false)

	res, err := tool.Execute(context.Background(), map[string]interface{}{
		"pattern": "hello",
		"path":    root,
		"glob":    "**/*.go",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Success {
		t.Fatal("expected success")
	}
	output := res.Output
	if !strings.Contains(output, "main.go") {
		t.Errorf("expected main.go in output, got %q", output)
	}
	if strings.Contains(output, "readme.txt") {
		t.Errorf("expected readme.txt excluded, got %q", output)
	}
}
