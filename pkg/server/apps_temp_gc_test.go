package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newGcTestHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()
	h := &Handler{
		dataDir: dir,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return h, dir
}

func TestRecordAppTempThread_Roundtrip(t *testing.T) {
	h, dir := newGcTestHandler(t)

	// Empty inputs are no-ops.
	h.recordAppTempThread("", dir)
	h.recordAppTempThread("t1", "")

	// Valid pair stores.
	h.recordAppTempThread("t1", dir)
	v, ok := h.appTempThreads.Load("t1")
	require.True(t, ok)
	require.Equal(t, dir, v.(string))
}

func TestDrainAppTempThread_RemovesFile(t *testing.T) {
	h, dir := newGcTestHandler(t)
	canvasDir := filepath.Join(dir, "canvas")
	require.NoError(t, os.MkdirAll(canvasDir, 0o755))

	tempFile := filepath.Join(canvasDir, "tt-1.json")
	require.NoError(t, os.WriteFile(tempFile, []byte(`{}`), 0o644))

	h.recordAppTempThread("tt-1", dir)
	h.drainAppTempThread("tt-1")

	_, err := os.Stat(tempFile)
	require.True(t, os.IsNotExist(err), "temp file should be removed")

	// Subsequent drain is harmless.
	h.drainAppTempThread("tt-1")
}

func TestDrainAppTempThread_NoOps(t *testing.T) {
	h, _ := newGcTestHandler(t)

	// Empty thread id no-ops.
	h.drainAppTempThread("")

	// Unknown thread id no-ops.
	h.drainAppTempThread("never-recorded")

	// Empty dataDir branch.
	h.appTempThreads.Store("empty", "")
	h.drainAppTempThread("empty")
}

func TestDrainAppTempThread_MissingFileNoError(t *testing.T) {
	h, dir := newGcTestHandler(t)
	h.recordAppTempThread("nofile", dir)
	// File never written; drain should still succeed.
	h.drainAppTempThread("nofile")
}

func newGcTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	h, dir := newGcTestHandler(t)
	s := &Server{
		handler: h,
		opts:    Options{DataDir: dir},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return s, dir
}

func TestAppTempCanvasDirs_LegacyOnly(t *testing.T) {
	s, dir := newGcTestServer(t)
	dirs := s.appTempCanvasDirs(context.Background())
	require.Equal(t, []string{filepath.Join(dir, "canvas")}, dirs)
}

func TestAppTempCanvasDirs_EmptyDataDir(t *testing.T) {
	h, _ := newGcTestHandler(t)
	s := &Server{
		handler: h,
		opts:    Options{},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	dirs := s.appTempCanvasDirs(context.Background())
	require.Empty(t, dirs)
}

func TestRunAppTempThreadSweep_RemovesOld(t *testing.T) {
	s, dir := newGcTestServer(t)
	canvasDir := filepath.Join(dir, "canvas")
	require.NoError(t, os.MkdirAll(canvasDir, 0o755))

	oldFile := filepath.Join(canvasDir, "app-run-old.json")
	freshFile := filepath.Join(canvasDir, "app-run-fresh.json")
	otherFile := filepath.Join(canvasDir, "thread.json")
	subDir := filepath.Join(canvasDir, "subdir")
	require.NoError(t, os.MkdirAll(subDir, 0o755))

	require.NoError(t, os.WriteFile(oldFile, []byte("{}"), 0o644))
	require.NoError(t, os.WriteFile(freshFile, []byte("{}"), 0o644))
	require.NoError(t, os.WriteFile(otherFile, []byte("{}"), 0o644))

	// Make oldFile look 25h old.
	old := time.Now().Add(-25 * time.Hour)
	require.NoError(t, os.Chtimes(oldFile, old, old))

	s.runAppTempThreadSweep()

	_, err := os.Stat(oldFile)
	require.True(t, os.IsNotExist(err), "old file should be swept")
	_, err = os.Stat(freshFile)
	require.NoError(t, err, "fresh file should remain")
	_, err = os.Stat(otherFile)
	require.NoError(t, err, "non-app-run file should remain")
	_, err = os.Stat(subDir)
	require.NoError(t, err, "subdir should remain")
}

func TestRunAppTempThreadSweep_MissingDir(t *testing.T) {
	s, _ := newGcTestServer(t)
	// Canvas dir doesn't exist — sweep should be a no-op without error.
	s.runAppTempThreadSweep()
}

func TestAppsScopeRoots_LegacyOnly(t *testing.T) {
	s, dir := newGcTestServer(t)
	roots := s.appsScopeRoots(context.Background())
	require.Equal(t, []string{dir}, roots)
}

func TestAppsScopeRoots_EmptyDataDir(t *testing.T) {
	h, _ := newGcTestHandler(t)
	s := &Server{
		handler: h,
		opts:    Options{},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	roots := s.appsScopeRoots(context.Background())
	require.Empty(t, roots)
}

func TestSweepAppVersionsOnce_NoApps(t *testing.T) {
	s, _ := newGcTestServer(t)
	// No apps written yet → store.List returns empty, no panic, no warns.
	s.sweepAppVersionsOnce(context.Background())
}

func TestRunAppVersionRetention_ContextCancel(t *testing.T) {
	s, _ := newGcTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so it bails quickly after the initial sweep.

	done := make(chan struct{})
	go func() {
		s.runAppVersionRetention(ctx)
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("runAppVersionRetention did not exit after cancel")
	}
}
