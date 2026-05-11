// skills_import_archive.go: archive download/extraction and git source preparation for skill imports.
package server

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	git "github.com/go-git/go-git/v5"
	transport "github.com/go-git/go-git/v5/plumbing/transport"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

func prepareSkillImportSource(sourceType skillImportSourceType, params skillImportParams) (string, func(), error) {
	switch sourceType {
	case skillImportSourcePath:
		return "", nil, nil
	case skillImportSourceGit:
		return cloneGitSkillImport(params.RepoURL)
	case skillImportSourceArchive:
		return downloadSkillArchive(params.ArchiveURL)
	default:
		return "", nil, fmt.Errorf("unsupported source_type %q", sourceType)
	}
}

func cloneGitSkillImport(repoURL string) (string, func(), error) {
	if info, err := os.Stat(repoURL); err == nil && info.IsDir() {
		if _, err := os.Stat(filepath.Join(repoURL, ".git")); err == nil {
			return repoURL, nil, nil
		}
	}
	tmpDir, err := os.MkdirTemp("", "saker-skill-import-git-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	auth, err := resolveGitCloneAuth(repoURL)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := git.PlainClone(tmpDir, false, &git.CloneOptions{
		URL:   repoURL,
		Depth: 1,
		Auth:  auth,
	}); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("git clone failed: %w", err)
	}
	return tmpDir, cleanup, nil
}

func resolveGitCloneAuth(repoURL string) (transport.AuthMethod, error) {
	if !isSSHRepoURL(repoURL) {
		return nil, nil
	}
	if strings.TrimSpace(os.Getenv("SSH_AUTH_SOCK")) != "" {
		auth, err := gitssh.NewSSHAgentAuth("git")
		if err == nil {
			return auth, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	for _, keyName := range []string{"id_ed25519", "id_rsa", "id_ecdsa", "id_dsa"} {
		keyPath := filepath.Join(home, ".ssh", keyName)
		if _, err := os.Stat(keyPath); err != nil {
			continue
		}
		auth, err := gitssh.NewPublicKeysFromFile("git", keyPath, "")
		if err == nil {
			return auth, nil
		}
	}
	return nil, errors.New("git clone failed: SSH repository requires authentication; configure SSH_AUTH_SOCK or use an HTTPS repository URL")
}

func isSSHRepoURL(repoURL string) bool {
	if strings.HasPrefix(repoURL, "ssh://") {
		return true
	}
	return strings.Contains(repoURL, "@") && strings.Contains(repoURL, ":") && !strings.Contains(repoURL, "://")
}

func downloadSkillArchive(archiveURL string) (string, func(), error) {
	resp, err := http.Get(archiveURL)
	if err != nil {
		return "", nil, fmt.Errorf("download archive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("download archive: status %d", resp.StatusCode)
	}
	tmpDir, err := os.MkdirTemp("", "saker-skill-import-archive-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	archiveName := filepath.Base(strings.Split(strings.TrimSpace(archiveURL), "?")[0])
	if archiveName == "." || archiveName == "" || archiveName == string(filepath.Separator) {
		archiveName = "source"
	}
	archivePath := filepath.Join(tmpDir, archiveName)
	file, err := os.Create(archivePath)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	extractDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := extractSkillArchive(archivePath, extractDir); err != nil {
		cleanup()
		return "", nil, err
	}
	rootDir, err := detectSingleRootDir(extractDir)
	if err != nil {
		rootDir = extractDir
	}
	return rootDir, cleanup, nil
}

func extractSkillArchive(archivePath string, dest string) error {
	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return unzipArchive(archivePath, dest)
	default:
		return untarArchive(archivePath, dest)
	}
}

func unzipArchive(path string, dest string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		target, err := safeArchivePath(dest, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		if err := writeCopiedFile(target, rc, file.Mode()); err != nil {
			_ = rc.Close()
			return err
		}
		_ = rc.Close()
	}
	return nil
}

func untarArchive(path string, dest string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	var reader io.Reader = file
	if gzReader, gzErr := gzip.NewReader(file); gzErr == nil {
		defer gzReader.Close()
		reader = gzReader
	} else {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		reader = file
	}

	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeArchivePath(dest, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeCopiedFile(target, tarReader, os.FileMode(header.Mode)); err != nil {
				return err
			}
		}
	}
}

func safeArchivePath(root string, name string) (string, error) {
	cleaned := filepath.Clean(name)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("unsafe archive entry %q", name)
	}
	return filepath.Join(root, cleaned), nil
}

func detectSingleRootDir(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return "", errors.New("archive has multiple roots")
	}
	return filepath.Join(root, entries[0].Name()), nil
}
