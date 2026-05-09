package skillhub

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// InstallResult describes the outcome of an Install call.
type InstallResult struct {
	Slug        string
	Version     string
	ETag        string
	Dir         string
	FilesCount  int
	NotModified bool
}

// InstallOptions controls Install behavior.
type InstallOptions struct {
	// Dir is the target directory (absolute). The skill will be written to
	// Dir/<slug with '/' replaced by '__'>/.
	Dir string
	// Version to fetch; empty or "latest" → latest.
	Version string
	// ETag is sent as If-None-Match; on 304 Install returns NotModified=true
	// without touching the filesystem.
	ETag string
	// MaxBytes caps total uncompressed size (default 20MB).
	MaxBytes int64
}

// safeInstallRoot resolves the directory under which Install writes files.
// It rejects traversal-prone slugs and returns the absolute per-skill dir.
func safeInstallRoot(rootDir, slug string) (string, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" || strings.Contains(slug, "..") {
		return "", fmt.Errorf("invalid slug %q", slug)
	}
	// Flatten namespace into one directory component — preserves uniqueness
	// without introducing a second '/' level that could conflict with loader
	// assumptions (skills are expected one directory down).
	dir := strings.ReplaceAll(slug, "/", "__")
	if dir == "" {
		return "", fmt.Errorf("invalid slug %q", slug)
	}
	return filepath.Join(rootDir, dir), nil
}

// Install downloads a skill zip and extracts it into opts.Dir.
// Existing files are overwritten. Directory traversal in the zip is rejected.
func (c *Client) Install(ctx context.Context, slug string, opts InstallOptions) (*InstallResult, error) {
	if opts.Dir == "" {
		return nil, errors.New("opts.Dir is required")
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 20 * 1024 * 1024
	}
	targetDir, err := safeInstallRoot(opts.Dir, slug)
	if err != nil {
		return nil, err
	}

	body, etag, err := c.Download(ctx, slug, opts.Version, opts.ETag)
	if errors.Is(err, ErrNotModified) {
		return &InstallResult{Slug: slug, Version: opts.Version, ETag: opts.ETag, Dir: targetDir, NotModified: true}, nil
	}
	if err != nil {
		return nil, err
	}
	defer body.Close()

	// Read everything into memory (skills are tiny; Phase 3 may stream).
	buf := bytes.Buffer{}
	if _, err := io.Copy(&buf, io.LimitReader(body, opts.MaxBytes+1)); err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	if int64(buf.Len()) > opts.MaxBytes {
		return nil, fmt.Errorf("archive exceeds max size (%d bytes)", opts.MaxBytes)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	// Fresh extract: remove existing target then recreate.
	if err := os.RemoveAll(targetDir); err != nil {
		return nil, fmt.Errorf("clean target: %w", err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, fmt.Errorf("create target: %w", err)
	}

	count := 0
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		rel := filepath.Clean(zf.Name)
		if filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") || strings.Contains(rel, "../") {
			return nil, fmt.Errorf("illegal path in archive: %s", zf.Name)
		}
		dst := filepath.Join(targetDir, rel)
		if !strings.HasPrefix(dst, targetDir+string(os.PathSeparator)) && dst != targetDir {
			return nil, fmt.Errorf("path traversal rejected: %s", zf.Name)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", zf.Name, err)
		}
		content, err := io.ReadAll(io.LimitReader(rc, opts.MaxBytes))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", zf.Name, err)
		}
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", dst, err)
		}
		count++
	}

	// Write a sidecar origin file so the loader can reason about source.
	// Format: simple KEY=VALUE lines; intentionally not JSON to avoid the
	// loader picking it up as a skill.
	origin := fmt.Sprintf("source=skillhub\nregistry=%s\nslug=%s\nversion=%s\netag=%s\n",
		c.baseURL, slug, opts.Version, strings.Trim(etag, "\""))
	_ = os.WriteFile(filepath.Join(targetDir, ".skillhub-origin"), []byte(origin), 0o644)

	return &InstallResult{
		Slug:       slug,
		Version:    opts.Version,
		ETag:       etag,
		Dir:        targetDir,
		FilesCount: count,
	}, nil
}

// Uninstall removes a previously installed skill directory.
func Uninstall(rootDir, slug string) error {
	dir, err := safeInstallRoot(rootDir, slug)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}
