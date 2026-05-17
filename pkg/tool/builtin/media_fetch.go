package toolbuiltin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	mediaFetchTimeout  = 2 * time.Minute
	mediaFetchMaxBytes = 200 << 20 // 200 MB
)

// resolveMediaPath checks whether path is an HTTP(S) URL. If so it downloads
// the resource to a content-addressed temp file and returns the local path.
// Otherwise the input is returned unchanged. Files are named by content hash
// so repeated downloads of the same resource are no-ops and the file remains
// available for subsequent tool calls within the same session.
func ResolveMediaPath(ctx context.Context, path string) (localPath string, err error) {
	trimmed := strings.TrimSpace(path)
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		return path, nil
	}

	data, mediaType, err := fetchMediaURL(ctx, trimmed)
	if err != nil {
		return "", fmt.Errorf("fetch media URL: %w", err)
	}

	ext := extensionForMediaType(mediaType, trimmed)
	hash := sha256.Sum256(data)
	name := hex.EncodeToString(hash[:8]) + ext

	tmpDir := os.TempDir()
	localPath = filepath.Join(tmpDir, "saker-media-"+name)

	// Content-addressed: skip write if file already exists with same hash.
	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}

	if err := os.WriteFile(localPath, data, 0o644); err != nil {
		return "", fmt.Errorf("write temp media: %w", err)
	}

	return localPath, nil
}

func fetchMediaURL(ctx context.Context, rawURL string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, mediaFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download media: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download media: status %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(mediaFetchMaxBytes)+1))
	if err != nil {
		return nil, "", fmt.Errorf("read media body: %w", err)
	}
	if len(data) > mediaFetchMaxBytes {
		return nil, "", fmt.Errorf("media too large (>200MB)")
	}

	mediaType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if semi := strings.Index(mediaType, ";"); semi >= 0 {
		mediaType = strings.TrimSpace(mediaType[:semi])
	}
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}

	return data, mediaType, nil
}

func extensionForMediaType(mediaType, rawURL string) string {
	switch mediaType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	case "video/mp4":
		return ".mp4"
	case "audio/mpeg":
		return ".mp3"
	case "audio/wav":
		return ".wav"
	}
	// Fallback: try to extract from URL path.
	if idx := strings.LastIndex(rawURL, "."); idx >= 0 {
		ext := strings.ToLower(rawURL[idx:])
		if qm := strings.Index(ext, "?"); qm >= 0 {
			ext = ext[:qm]
		}
		if len(ext) <= 5 {
			return ext
		}
	}
	return ".bin"
}
