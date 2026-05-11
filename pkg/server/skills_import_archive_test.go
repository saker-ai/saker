package server

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsSSHRepoURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"ssh://git@github.com/foo/bar.git", true},
		{"git@github.com:foo/bar.git", true},
		{"https://github.com/foo/bar.git", false},
		{"http://example.com/foo.git", false},
		{"file:///tmp/repo", false},
		{"plainstring", false},
	}
	for _, c := range cases {
		require.Equal(t, c.want, isSSHRepoURL(c.in), "isSSHRepoURL(%q)", c.in)
	}
}

func TestSafeArchivePath(t *testing.T) {
	t.Parallel()
	root := "/dest"
	good, err := safeArchivePath(root, "skill/inner/file.md")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "skill/inner/file.md"), good)

	_, err = safeArchivePath(root, "../escape.txt")
	require.Error(t, err)

	_, err = safeArchivePath(root, "/etc/passwd")
	require.Error(t, err)

	// "." cleans to itself and is rejected.
	_, err = safeArchivePath(root, ".")
	require.Error(t, err)
}

func TestDetectSingleRootDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	inner := filepath.Join(root, "only-skill")
	require.NoError(t, os.MkdirAll(inner, 0o755))

	got, err := detectSingleRootDir(root)
	require.NoError(t, err)
	require.Equal(t, inner, got)
}

func TestDetectSingleRootDir_MultipleEntries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "b"), 0o755))

	_, err := detectSingleRootDir(root)
	require.Error(t, err)
}

func TestDetectSingleRootDir_FileNotDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0o644))

	_, err := detectSingleRootDir(root)
	require.Error(t, err)
}

func TestDetectSingleRootDir_BadRoot(t *testing.T) {
	t.Parallel()
	_, err := detectSingleRootDir(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
}

func TestPrepareSkillImportSource_PathNoOp(t *testing.T) {
	t.Parallel()
	dir, cleanup, err := prepareSkillImportSource(skillImportSourcePath, skillImportParams{})
	require.NoError(t, err)
	require.Empty(t, dir)
	require.Nil(t, cleanup)
}

func TestPrepareSkillImportSource_UnsupportedSourceType(t *testing.T) {
	t.Parallel()
	_, _, err := prepareSkillImportSource("custom-thing", skillImportParams{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported source_type")
}

func TestCloneGitSkillImport_LocalGitDir(t *testing.T) {
	t.Parallel()
	repoDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755))

	dir, cleanup, err := cloneGitSkillImport(repoDir)
	require.NoError(t, err)
	require.Equal(t, repoDir, dir)
	require.Nil(t, cleanup, "local repo path returns nil cleanup")
}

func TestExtractSkillArchive_Zip(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	zipPath := filepath.Join(src, "archive.zip")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("alpha/SKILL.md")
	require.NoError(t, err)
	_, err = w.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, os.WriteFile(zipPath, buf.Bytes(), 0o644))

	dest := t.TempDir()
	require.NoError(t, extractSkillArchive(zipPath, dest))

	got, err := os.ReadFile(filepath.Join(dest, "alpha/SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))
}

func TestExtractSkillArchive_TarGz(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	tgzPath := filepath.Join(src, "archive.tar.gz")

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	body := []byte("body")
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "alpha/",
		Typeflag: tar.TypeDir,
		Mode:     0o755,
	}))
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "alpha/SKILL.md",
		Size:     int64(len(body)),
		Mode:     0o644,
		Typeflag: tar.TypeReg,
	}))
	_, err := tw.Write(body)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	require.NoError(t, os.WriteFile(tgzPath, buf.Bytes(), 0o644))

	dest := t.TempDir()
	require.NoError(t, extractSkillArchive(tgzPath, dest))

	got, err := os.ReadFile(filepath.Join(dest, "alpha/SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, "body", string(got))
}

func TestExtractSkillArchive_UnsafeEntryRejected(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	zipPath := filepath.Join(src, "evil.zip")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../../escape.txt")
	require.NoError(t, err)
	_, err = w.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, os.WriteFile(zipPath, buf.Bytes(), 0o644))

	err = extractSkillArchive(zipPath, t.TempDir())
	require.Error(t, err)
}

func TestDownloadSkillArchive_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, cleanup, err := downloadSkillArchive(srv.URL + "/missing.zip")
	require.Error(t, err)
	require.Nil(t, cleanup)
	require.Contains(t, err.Error(), "status 404")
}

func TestDownloadSkillArchive_HappyZip(t *testing.T) {
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	w, err := zw.Create("alpha/SKILL.md")
	require.NoError(t, err)
	_, err = w.Write([]byte("body"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipBuf.Bytes())
	}))
	defer srv.Close()

	dir, cleanup, err := downloadSkillArchive(srv.URL + "/release.zip")
	require.NoError(t, err)
	require.NotNil(t, cleanup)
	defer cleanup()
	require.True(t, strings.HasSuffix(filepath.Base(dir), "alpha"), "single-root collapse")
}

func TestPrepareSkillImportSource_GitLocalDir(t *testing.T) {
	t.Parallel()
	repoDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755))

	dir, cleanup, err := prepareSkillImportSource(skillImportSourceGit, skillImportParams{RepoURL: repoDir})
	require.NoError(t, err)
	require.Equal(t, repoDir, dir)
	require.Nil(t, cleanup)
}
