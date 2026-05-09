package landlockenv

import (
	"fmt"
	"path/filepath"
	"strings"

	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
)

// errAccessDenied is returned when a path falls outside the allowed boundaries.
var errAccessDenied = fmt.Errorf("landlockenv: access denied")

// checkReadAccess verifies that path is within ro_paths or rw_paths of the session.
func checkReadAccess(ps *sandboxenv.PreparedSession, path string) error {
	resolved := resolveSafe(ps, path)
	roPaths, rwPaths := pathsFromSession(ps)
	for _, root := range roPaths {
		if within(resolved, root) {
			return nil
		}
	}
	for _, root := range rwPaths {
		if within(resolved, root) {
			return nil
		}
	}
	return fmt.Errorf("%w: read %s", errAccessDenied, resolved)
}

// checkWriteAccess verifies that path is within rw_paths of the session.
func checkWriteAccess(ps *sandboxenv.PreparedSession, path string) error {
	resolved := resolveSafe(ps, path)
	_, rwPaths := pathsFromSession(ps)
	for _, root := range rwPaths {
		if within(resolved, root) {
			return nil
		}
	}
	return fmt.Errorf("%w: write %s", errAccessDenied, resolved)
}

// resolveSafe converts a path to an absolute, symlink-resolved form.
// If symlink resolution fails (e.g. path doesn't exist yet for writes),
// it falls back to cleaning the absolute path.
func resolveSafe(ps *sandboxenv.PreparedSession, path string) string {
	abs := absPath(ps, path)
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Path may not exist yet (e.g. new file write). Resolve the
		// deepest existing ancestor to catch symlink tricks in parent dirs.
		dir := filepath.Dir(abs)
		if resolvedDir, dirErr := filepath.EvalSymlinks(dir); dirErr == nil {
			return filepath.Join(resolvedDir, filepath.Base(abs))
		}
		return abs
	}
	return resolved
}

// absPath resolves a potentially relative path against the session's GuestCwd.
func absPath(ps *sandboxenv.PreparedSession, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if ps != nil && ps.GuestCwd != "" {
		return filepath.Clean(filepath.Join(ps.GuestCwd, path))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
}

// within reports whether path is equal to or nested inside root.
func within(path, root string) bool {
	if root == "" {
		return false
	}
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(root, sep) {
		root += sep
	}
	return strings.HasPrefix(path, root)
}
