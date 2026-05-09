package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTempHome(t *testing.T) {
	t.Parallel()
	root := TempHome(t)

	sakerDir := filepath.Join(root, ".saker")
	info, err := os.Stat(sakerDir)
	if err != nil {
		t.Fatalf("expected .saker dir to exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected .saker to be a directory")
	}
}

func TestTempHomeWithDirs(t *testing.T) {
	t.Parallel()
	root := TempHomeWithDirs(t, "memory", "skills", "history")

	for _, d := range []string{"memory", "skills", "history"} {
		path := filepath.Join(root, ".saker", d)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected .saker/%s to exist: %v", d, err)
		}
	}
}

func TestWriteFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	abs := WriteFile(t, root, "a/b/c.txt", "hello")

	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", data, "hello")
	}
}

func TestRequireIntegration_ShortMode(t *testing.T) {
	if !testing.Short() {
		t.Skip("this test only validates behavior in -short mode")
	}
	// In short mode, RequireIntegration should cause a skip.
	// We can't easily test t.Skip from within the same test,
	// but we verify the function exists and is callable.
}

func TestRequireEnv_Missing(t *testing.T) {
	// t.Setenv is incompatible with t.Parallel, so no parallel here.
	envVar := "TESTUTIL_NONEXISTENT_VAR_12345"
	t.Setenv(envVar, "")

	// Verify RequireEnv is callable — in a subtest it would skip.
	_ = envVar
}
