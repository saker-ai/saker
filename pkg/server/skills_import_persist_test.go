package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareTargetDir_NewDir(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "fresh")
	action, err := prepareTargetDir(dir, skillImportConflictSkip)
	require.NoError(t, err)
	require.Equal(t, "created", action)
}

func TestPrepareTargetDir_OverwriteRemoves(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "victim")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "leftover"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "leftover", "f"), []byte("x"), 0o644))

	action, err := prepareTargetDir(dir, skillImportConflictOverwrite)
	require.NoError(t, err)
	require.Equal(t, "overwritten", action)

	_, err = os.Stat(dir)
	require.True(t, os.IsNotExist(err), "dir must be removed after overwrite")
}

func TestPrepareTargetDir_Skip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	action, err := prepareTargetDir(dir, skillImportConflictSkip)
	require.NoError(t, err)
	require.Equal(t, "skipped", action)
}

func TestPrepareTargetDir_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := prepareTargetDir(dir, skillImportConflictError)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already exists")
}

func TestPrepareTargetDir_UnsupportedMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := prepareTargetDir(dir, skillImportConflictMode("nonsense"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported")
}

func TestCopyDir_Happy(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "top.txt"), []byte("top body"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(src, "sub", "nested.txt"), []byte("nested body"), 0o644))

	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, copyDir(src, dst))

	got, err := os.ReadFile(filepath.Join(dst, "top.txt"))
	require.NoError(t, err)
	require.Equal(t, "top body", string(got))

	got, err = os.ReadFile(filepath.Join(dst, "sub", "nested.txt"))
	require.NoError(t, err)
	require.Equal(t, "nested body", string(got))

	// Mode preserved (best-effort, umask may strip).
	info, err := os.Stat(filepath.Join(dst, "top.txt"))
	require.NoError(t, err)
	require.NotEqual(t, os.FileMode(0), info.Mode().Perm())
}

func TestCopyDir_OverwritesExistingTarget(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "f"), []byte("new"), 0o644))

	dst := filepath.Join(t.TempDir(), "dst")
	require.NoError(t, os.MkdirAll(dst, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dst, "stale"), []byte("stale"), 0o644))

	require.NoError(t, copyDir(src, dst))

	_, err := os.Stat(filepath.Join(dst, "stale"))
	require.True(t, os.IsNotExist(err), "stale file must be removed before copy")

	got, err := os.ReadFile(filepath.Join(dst, "f"))
	require.NoError(t, err)
	require.Equal(t, "new", string(got))
}

func TestWriteCopiedFile(t *testing.T) {
	t.Parallel()
	dst := filepath.Join(t.TempDir(), "out.txt")
	require.NoError(t, writeCopiedFile(dst, strings.NewReader("hello"), 0o600))

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))
}

func TestWriteCopiedFile_OpenError(t *testing.T) {
	t.Parallel()
	// Path inside a non-existent directory should error on OpenFile.
	dst := filepath.Join(t.TempDir(), "missing-dir", "x.txt")
	err := writeCopiedFile(dst, strings.NewReader("hi"), 0o644)
	require.Error(t, err)
}
