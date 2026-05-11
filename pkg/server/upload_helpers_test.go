package server

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIsAllowedMedia(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mediaType string
		want      bool
	}{
		{"image/png", true},
		{"image/jpeg", true},
		{"video/mp4", true},
		{"audio/mpeg", true},
		{"application/pdf", true},
		{"text/plain", false},
		{"application/octet-stream", false},
		{"application/zip", false},
		{"", false},
	}
	for _, c := range cases {
		got := isAllowedMedia(c.mediaType)
		require.Equal(t, c.want, got, "isAllowedMedia(%q)", c.mediaType)
	}
}

func TestSanitizeFilename(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"hello.png", "hello.png"},
		{"/etc/passwd", "passwd"},
		{"..\\evil.exe", "evil.exe"},
		{"a/b/c.txt", "c.txt"},
		{"x\x00y.zip", "xy.zip"},
		{"..", "upload"},
		{"weird name with spaces.jpg", "weird name with spaces.jpg"},
	}
	for _, c := range cases {
		got := sanitizeFilename(c.in)
		require.Equal(t, c.want, got, "sanitizeFilename(%q)", c.in)
	}
}

func TestCleanupUploads_RemovesOldFiles(t *testing.T) {
	dir := t.TempDir()
	uploads := filepath.Join(dir, "uploads")
	require.NoError(t, os.MkdirAll(uploads, 0o755))

	old := filepath.Join(uploads, "old.bin")
	fresh := filepath.Join(uploads, "fresh.bin")
	subDir := filepath.Join(uploads, "subdir")
	require.NoError(t, os.MkdirAll(subDir, 0o755))

	require.NoError(t, os.WriteFile(old, []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(fresh, []byte("b"), 0o644))

	yesterday := time.Now().Add(-uploadMaxAge - time.Hour)
	require.NoError(t, os.Chtimes(old, yesterday, yesterday))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cleanupUploads(dir, logger)

	_, err := os.Stat(old)
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(fresh)
	require.NoError(t, err)
	_, err = os.Stat(subDir)
	require.NoError(t, err, "subdir should remain")
}

func TestCleanupUploads_MissingDirNoOp(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// uploads dir does not exist — no panic, no error.
	cleanupUploads(dir, logger)
}
