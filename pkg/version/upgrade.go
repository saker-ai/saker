package version

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// GitHubReleaseURL is the base URL for downloading release assets.
	GitHubReleaseURL = "https://github.com/saker-ai/saker/releases/download"

	// DownloadTimeout is the timeout for downloading a release binary.
	DownloadTimeout = 120 * time.Second
)

// SelfUpgrade downloads the specified version and replaces the current binary.
// It returns the path to the new binary on success.
func SelfUpgrade(version string, progressFn func(string)) error {
	version = normalizeVersion(version)
	if version == "" {
		return fmt.Errorf("upgrade: empty version")
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("upgrade: locate current binary: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("upgrade: resolve symlink: %w", err)
	}

	osName, archName := detectPlatform()
	assetName := fmt.Sprintf("saker-v%s-%s-%s.tar.gz", version, osName, archName)
	downloadURL := fmt.Sprintf("%s/v%s/%s", GitHubReleaseURL, version, assetName)

	if progressFn != nil {
		progressFn(fmt.Sprintf("Downloading %s ...", assetName))
	}

	// Download to temp file.
	tmpDir, err := os.MkdirTemp("", "saker-upgrade-*")
	if err != nil {
		return fmt.Errorf("upgrade: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, assetName)
	if err := downloadFile(downloadURL, archivePath); err != nil {
		return fmt.Errorf("upgrade: download: %w", err)
	}

	if progressFn != nil {
		progressFn("Extracting...")
	}

	// Extract binary from tarball.
	binaryPath, err := extractBinary(archivePath, tmpDir, "saker")
	if err != nil {
		return fmt.Errorf("upgrade: extract: %w", err)
	}

	if err := os.Chmod(binaryPath, 0o755); err != nil {
		return fmt.Errorf("upgrade: chmod: %w", err)
	}

	if progressFn != nil {
		progressFn("Replacing binary...")
	}

	// Replace current binary atomically.
	if err := replaceBinary(execPath, binaryPath); err != nil {
		return fmt.Errorf("upgrade: replace binary: %w", err)
	}

	return nil
}

func detectPlatform() (osName, archName string) {
	switch runtime.GOOS {
	case "darwin":
		osName = "darwin"
	default:
		osName = "linux"
	}
	switch runtime.GOARCH {
	case "arm64":
		archName = "arm64"
	default:
		archName = "amd64"
	}
	return
}

func downloadFile(url, dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), DownloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

func extractBinary(archivePath, destDir, binaryName string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("tar: %w", err)
		}

		// Match the binary by base name.
		base := filepath.Base(hdr.Name)
		if base != binaryName || hdr.Typeflag != tar.TypeReg {
			continue
		}

		outPath := filepath.Join(destDir, binaryName)
		out, err := os.Create(outPath)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return "", err
		}
		out.Close()
		return outPath, nil
	}

	return "", fmt.Errorf("binary %q not found in archive", binaryName)
}

// replaceBinary atomically replaces oldPath with newPath.
// On Unix, this renames the old binary first, then moves the new one in place.
func replaceBinary(oldPath, newPath string) error {
	dir := filepath.Dir(oldPath)
	base := filepath.Base(oldPath)

	// Check if we can write to the directory.
	backupPath := filepath.Join(dir, "."+base+".old")

	// Move current binary to backup.
	if err := os.Rename(oldPath, backupPath); err != nil {
		// Try writing to a temp location and copying instead (no write permission to dir).
		return replaceViaCopy(oldPath, newPath)
	}

	// Move new binary into place.
	if err := copyFile(newPath, oldPath); err != nil {
		// Restore backup on failure.
		_ = os.Rename(backupPath, oldPath)
		return err
	}

	// Clean up backup.
	_ = os.Remove(backupPath)
	return nil
}

func replaceViaCopy(oldPath, newPath string) error {
	return copyFile(newPath, oldPath)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// Get source file info for permissions.
	info, err := in.Stat()
	if err != nil {
		return err
	}

	// Write to temp file in same directory, then rename.
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".saker-upgrade-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	tmp.Close()

	if err := os.Chmod(tmpName, info.Mode()); err != nil {
		os.Remove(tmpName)
		return err
	}

	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Restart re-executes the current process with the same arguments.
// This function does not return on success.
func Restart() error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	// Resolve symlinks to get the actual binary path.
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return err
	}

	args := os.Args
	env := os.Environ()

	// Filter out any upgrade-related env vars to avoid loops.
	var filteredEnv []string
	for _, e := range env {
		if !strings.HasPrefix(e, "SAKER_UPGRADING=") {
			filteredEnv = append(filteredEnv, e)
		}
	}

	return syscallExec(execPath, args, filteredEnv)
}
