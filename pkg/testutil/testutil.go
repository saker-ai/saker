// Package testutil provides shared test helpers inspired by Hermes Agent's
// conftest.py pattern — isolated home directories, integration test guards,
// and common fixtures to reduce boilerplate across test files.
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TempHome creates an isolated .saker home directory inside t.TempDir()
// and returns the project root. This prevents tests from reading or writing
// the developer's real ~/.saker or project .saker directories.
//
// Equivalent to Hermes Agent's _isolate_hermes_home autouse fixture.
func TempHome(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	sakerDir := filepath.Join(root, ".saker")
	if err := os.MkdirAll(sakerDir, 0o755); err != nil {
		t.Fatalf("testutil.TempHome: mkdir .saker: %v", err)
	}
	return root
}

// TempHomeWithDirs creates an isolated .saker home with additional
// subdirectories pre-created (e.g., "memory", "skills", "history").
func TempHomeWithDirs(t *testing.T, dirs ...string) string {
	t.Helper()
	root := TempHome(t)
	for _, d := range dirs {
		path := filepath.Join(root, ".saker", d)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("testutil.TempHomeWithDirs: mkdir %s: %v", d, err)
		}
	}
	return root
}

// WriteFile creates a file at the given relative path under root with the
// specified content. Parent directories are created automatically.
func WriteFile(t *testing.T, root, relPath, content string) string {
	t.Helper()
	abs := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("testutil.WriteFile: mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("testutil.WriteFile: write %s: %v", relPath, err)
	}
	return abs
}

// RequireIntegration skips the test when running in short mode (-short flag).
// Use this for tests that depend on external services, network access, or
// take significant time (>1s).
//
// Usage:
//
//	func TestSlowThing(t *testing.T) {
//	    testutil.RequireIntegration(t)
//	    // ... test that calls external APIs
//	}
func RequireIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
}

// RequireEnv skips the test unless the given environment variable is set
// and non-empty. Use for tests that require API keys or external config.
//
// Usage:
//
//	func TestWithAPIKey(t *testing.T) {
//	    testutil.RequireEnv(t, "ANTHROPIC_API_KEY")
//	    // ...
//	}
func RequireEnv(t *testing.T, envVar string) {
	t.Helper()
	if os.Getenv(envVar) == "" {
		t.Skipf("skipping: %s not set", envVar)
	}
}

// RequireBinary skips the test unless the given binary is available in PATH.
// Use for tests that depend on external tools (ffmpeg, chromium, etc.).
func RequireBinary(t *testing.T, name string) {
	t.Helper()
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return
		}
	}
	t.Skipf("skipping: %s not found in PATH", name)
}
