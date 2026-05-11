package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RequireIntegration / RequireEnv / RequireBinary all call t.Skip on failure
// and t.Helper otherwise. We use sub-tests with a fake T to verify the
// skip behavior without affecting the parent test outcome.

// fakeT is a minimal subset of testing.TB that records whether Skip was called.
// For RequireBinary we still need to pass *testing.T (the helpers take a real
// *testing.T), so we run them in subtests and check t.Skipped() afterwards.

func TestRequireIntegrationShortMode(t *testing.T) {
	if !testing.Short() {
		// Run a subtest that calls RequireIntegration; with -short it should be
		// skipped. Outside short mode, RequireIntegration is a no-op so we just
		// document the path.
		t.Run("noop in normal mode", func(sub *testing.T) {
			RequireIntegration(sub)
			if sub.Skipped() {
				sub.Errorf("RequireIntegration should not skip in normal mode")
			}
		})
		return
	}
	t.Run("skips in short mode", func(sub *testing.T) {
		RequireIntegration(sub)
		if !sub.Skipped() {
			sub.Errorf("RequireIntegration should skip in -short mode")
		}
	})
}

func TestRequireEnvMissing(t *testing.T) {
	envVar := "TESTUTIL_NEVER_SET_QQQ_42"
	t.Setenv(envVar, "")
	t.Run("missing env skips", func(sub *testing.T) {
		RequireEnv(sub, envVar)
		if !sub.Skipped() {
			sub.Errorf("RequireEnv should skip when env unset")
		}
	})
}

func TestRequireEnvPresent(t *testing.T) {
	envVar := "TESTUTIL_PRESENT_VAR_42"
	t.Setenv(envVar, "value")
	t.Run("present env does not skip", func(sub *testing.T) {
		RequireEnv(sub, envVar)
		if sub.Skipped() {
			sub.Errorf("RequireEnv should not skip when env set")
		}
	})
}

func TestRequireBinaryMissing(t *testing.T) {
	t.Run("missing binary skips", func(sub *testing.T) {
		RequireBinary(sub, "this-binary-does-not-exist-zzz-9999")
		if !sub.Skipped() {
			sub.Errorf("RequireBinary should skip when binary missing")
		}
	})
}

func TestRequireBinaryPresent(t *testing.T) {
	// Create a fake binary in a temp dir and prepend that dir to PATH.
	dir := t.TempDir()
	name := "fakebin-testutil"
	binaryPath := filepath.Join(dir, name)
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	t.Run("present binary does not skip", func(sub *testing.T) {
		RequireBinary(sub, name)
		if sub.Skipped() {
			sub.Errorf("RequireBinary should not skip when binary on PATH")
		}
	})
}

// Re-cover WriteFile error paths by exercising directories that already exist.
func TestWriteFileNested(t *testing.T) {
	root := t.TempDir()
	abs := WriteFile(t, root, "deeply/nested/dir/file.txt", "abc")
	if !strings.HasPrefix(abs, root) {
		t.Errorf("returned path should be under root: %q", abs)
	}
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "abc" {
		t.Errorf("content: %q", got)
	}
}

func TestTempHomeIsolation(t *testing.T) {
	a := TempHome(t)
	b := TempHome(t)
	if a == b {
		t.Errorf("TempHome should return distinct dirs: %q == %q", a, b)
	}
}
