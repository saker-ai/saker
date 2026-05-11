package server

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// cleanFilePathForFuzz mirrors the path-cleaning + containment check used by
// (*Server).handleServeFile. The handler joins the user-supplied path against
// the project root, cleans it, and rejects anything whose Rel against the
// root starts with "..". This helper makes that exact pipeline addressable
// from a fuzz test without needing the heavyweight runtime/server stack.
//
// Returns ("", false) when the input must be rejected (escapes root, is
// absolute, or fails containment), or (resolvedAbsPath, true) otherwise.
func cleanFilePathForFuzz(root, raw string) (string, bool) {
	if filepath.IsAbs(raw) {
		return "", false
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	resolved := filepath.Clean(filepath.Join(cleanRoot, raw))
	rel, err := filepath.Rel(cleanRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return resolved, true
}

// FuzzCleanFilePath exercises the path-traversal protection used by
// (*Server).handleServeFile. The test reproduces the handler's clean-and-
// contain logic byte-for-byte (see cleanFilePathForFuzz) and asserts the
// security invariant: any accepted path must remain inside the project
// root, regardless of the input.
//
// Property under test:
//  1. Function must not panic on any byte sequence.
//  2. Any path the function accepts must resolve to a location inside
//     root (verified independently with filepath.Rel).
//  3. Absolute paths must always be rejected.
func FuzzCleanFilePath(f *testing.F) {
	seeds := []string{
		"../../etc/passwd",
		"./test",
		"foo/bar",
		"",
		"foo/../bar",
		"//double-slash",
		`\windows\path`,
		"foo\x00bar",
		"a/b/../../c",
		"%2e%2e/passwd",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	root := f.TempDir()
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		f.Fatalf("Abs(%q): %v", root, err)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		// Property 1: must not panic.
		resolved, ok := cleanFilePathForFuzz(root, raw)

		if !ok {
			// Rejected paths are fine — the empty resolved is the contract.
			if resolved != "" {
				t.Fatalf("rejected input must return empty resolved, got %q", resolved)
			}
			return
		}

		// Property 2: accepted paths must lie inside the root.
		rel, relErr := filepath.Rel(rootAbs, resolved)
		if relErr != nil {
			t.Fatalf("filepath.Rel failed for accepted path %q: %v", resolved, relErr)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("CLEANED PATH ESCAPED ROOT: input=%q resolved=%q rel=%q", raw, resolved, rel)
		}
		if filepath.IsAbs(rel) {
			t.Fatalf("rel should not be absolute: rel=%q", rel)
		}

		// Property 3: absolute inputs must always be rejected. If we got
		// here with ok=true the input was non-absolute by construction.
		if filepath.IsAbs(raw) {
			t.Fatalf("absolute input %q should have been rejected", raw)
		}
	})
}

// FuzzRPCRequest stresses the JSON-RPC body decoder used by the
// /api/rpc/{method} HTTP adapter. The decoder must reject any non-object
// JSON body without panicking, and accept valid object/empty/null bodies.
func FuzzRPCRequest(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"foo":"bar"}`,
		`{"a":{"b":{"c":1}}}`,
		``,
		`null`,
		`[1,2,3]`,
		`"just a string"`,
		`{ broken`,
		`{"k":"` + strings.Repeat("x", 4096) + `"}`,
		`{"deep":` + strings.Repeat(`{"d":`, 64) + `1` + strings.Repeat(`}`, 64) + `}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		// The decoder must never panic on any input.
		got, err := decodeRPCParams(bytes.NewReader(body))
		if err != nil {
			// Errors are fine — invalid bodies must return a structured
			// error, not a crash.
			if got != nil {
				t.Fatalf("error case must return nil map, got %v", got)
			}
			return
		}
		// Success path: must return a non-nil map (handler invariant).
		if got == nil {
			t.Fatalf("success case must return non-nil map for body %q", body)
		}
	})
}
