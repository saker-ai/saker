package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	neturl "net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/api"
	storagecfg "github.com/saker-ai/saker/pkg/storage"
	"github.com/mojatter/s2"
)

const canvasMediaCacheDir = ".saker/canvas-media"

type cachedMedia struct {
	Path      string
	URL       string
	MediaType string
	SourceURL string
}

func (h *Handler) handleMediaCache(req Request) Response {
	rawURL, _ := req.Params["url"].(string)
	mediaType, _ := req.Params["mediaType"].(string)
	if strings.TrimSpace(rawURL) == "" {
		return h.invalidParams(req.ID, "url is required")
	}

	result, err := cacheMediaForProject(h.runtime.ProjectRoot(), rawURL, mediaType)
	if err != nil {
		return h.internalError(req.ID, "cache media: "+err.Error())
	}

	return h.success(req.ID, map[string]any{
		"path":       result.Path,
		"url":        result.URL,
		"media_type": result.MediaType,
		"source_url": result.SourceURL,
	})
}

func (h *Handler) handleMediaDataURL(req Request) Response {
	rawPath, _ := req.Params["path"].(string)
	if strings.TrimSpace(rawPath) == "" {
		return h.invalidParams(req.ID, "path is required")
	}

	absPath, err := resolveLocalMediaPath(h.runtime.ProjectRoot(), rawPath)
	if err != nil {
		return h.internalError(req.ID, "resolve media path: "+err.Error())
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return h.internalError(req.ID, "read media: "+err.Error())
	}

	mediaType := strings.TrimSpace(detectMediaType(absPath, data))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}

	return h.success(req.ID, map[string]any{
		"data_url": "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data),
		"path":     absPath,
		"url":      toServeFileURL(absPath),
	})
}

func cacheMediaForProject(projectRoot, rawInput, hintedMediaType string) (*cachedMedia, error) {
	data, mediaType, sourceURL, ext, err := readMediaInput(projectRoot, rawInput, hintedMediaType)
	if err != nil {
		return nil, err
	}

	cacheDir := filepath.Join(projectRoot, canvasMediaCacheDir)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir cache dir: %w", err)
	}

	sum := sha256.Sum256(data)
	fileName := hex.EncodeToString(sum[:]) + ext
	absPath := filepath.Join(cacheDir, fileName)
	if _, err := os.Stat(absPath); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat cache file: %w", err)
		}
		if err := os.WriteFile(absPath, data, 0o644); err != nil {
			return nil, fmt.Errorf("write cache file: %w", err)
		}
	}

	return &cachedMedia{
		Path:      absPath,
		URL:       toServeFileURL(absPath),
		MediaType: mediaType,
		SourceURL: sourceURL,
	}, nil
}

func readMediaInput(projectRoot, rawInput, hintedMediaType string) ([]byte, string, string, string, error) {
	rawInput = strings.TrimSpace(rawInput)
	switch {
	case strings.HasPrefix(rawInput, "data:"):
		data, mediaType, err := decodeDataURL(rawInput)
		if err != nil {
			return nil, "", "", "", err
		}
		return data, mediaType, "", chooseMediaExt("", mediaType, hintedMediaType), nil
	case strings.HasPrefix(rawInput, "http://") || strings.HasPrefix(rawInput, "https://"):
		data, mediaType, err := fetchRemoteMedia(rawInput)
		if err != nil {
			return nil, "", "", "", err
		}
		return data, mediaType, rawInput, chooseMediaExt(rawInput, mediaType, hintedMediaType), nil
	default:
		absPath, err := resolveLocalMediaPath(projectRoot, rawInput)
		if err != nil {
			return nil, "", "", "", err
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil, "", "", "", fmt.Errorf("read local media: %w", err)
		}
		mediaType := detectMediaType(absPath, data)
		return data, mediaType, "", chooseMediaExt(absPath, mediaType, hintedMediaType), nil
	}
}

func decodeDataURL(raw string) ([]byte, string, error) {
	comma := strings.Index(raw, ",")
	if comma < 0 {
		return nil, "", fmt.Errorf("invalid data url")
	}

	header := raw[:comma]
	payload := raw[comma+1:]
	if !strings.HasSuffix(header, ";base64") {
		return nil, "", fmt.Errorf("unsupported data url encoding")
	}

	mediaType := strings.TrimPrefix(strings.TrimSuffix(header, ";base64"), "data:")
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}

	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, "", fmt.Errorf("decode data url: %w", err)
	}
	return data, mediaType, nil
}

func fetchRemoteMedia(rawURL string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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

	const maxMediaBytes = 200 << 20 // 200 MB
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxMediaBytes)+1))
	if err != nil {
		return nil, "", fmt.Errorf("read media body: %w", err)
	}
	if len(data) > maxMediaBytes {
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

func resolveLocalMediaPath(projectRoot, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty path")
	}

	if strings.HasPrefix(raw, "/api/files/") || raw == "/api/files" || strings.HasPrefix(raw, "/api/files") {
		raw = strings.TrimPrefix(raw, "/api/files/")
		raw = strings.TrimPrefix(raw, "/api/files")
	}

	resolved := filepath.Clean(raw)
	if filepath.IsAbs(resolved) {
		if _, err := os.Stat(resolved); err != nil {
			return "", err
		}
		return resolved, nil
	}

	absCandidate := "/" + strings.TrimPrefix(filepath.ToSlash(resolved), "/")
	if _, err := os.Stat(absCandidate); err == nil {
		return absCandidate, nil
	}

	joined := filepath.Join(projectRoot, resolved)
	if !strings.HasPrefix(joined, projectRoot) {
		return "", fmt.Errorf("path outside project root")
	}
	if _, err := os.Stat(joined); err != nil {
		return "", err
	}
	return joined, nil
}

func detectMediaType(filePath string, data []byte) string {
	mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filePath)))
	if mediaType != "" {
		return mediaType
	}
	if len(data) == 0 {
		return ""
	}
	mediaType = http.DetectContentType(data)
	if semi := strings.Index(mediaType, ";"); semi >= 0 {
		mediaType = strings.TrimSpace(mediaType[:semi])
	}
	return mediaType
}

func chooseMediaExt(rawInput, mediaType, hintedMediaType string) string {
	if parsed, err := neturl.Parse(rawInput); err == nil {
		if ext := strings.ToLower(path.Ext(parsed.Path)); ext != "" {
			return ext
		}
	}
	if ext := strings.ToLower(filepath.Ext(rawInput)); ext != "" {
		return ext
	}

	candidateType := mediaType
	if candidateType == "" {
		switch hintedMediaType {
		case "image":
			candidateType = "image/png"
		case "video":
			candidateType = "video/mp4"
		case "audio":
			candidateType = "audio/wav"
		}
	}

	if exts, _ := mime.ExtensionsByType(candidateType); len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

func toServeFileURL(absPath string) string {
	return "/api/files" + filepath.ToSlash(absPath)
}

// collectMediaReferences walks one SessionStore's threads and adds every
// `/api/files`-style artifact path to the referenced set. The set is the
// union of references across whichever stores the caller passes in — keeping
// this as a pure helper lets multi-tenant cleanup union refs from every
// project before sweeping the (still-shared) on-disk cache.
func collectMediaReferences(sessions *SessionStore, into map[string]bool) {
	if sessions == nil {
		return
	}
	for _, t := range sessions.ListThreads() {
		for _, item := range sessions.GetItems(t.ID) {
			for _, a := range item.Artifacts {
				if strings.HasPrefix(a.URL, "/api/files") {
					into[strings.TrimPrefix(a.URL, "/api/files")] = true
				}
			}
		}
	}
}

// sweepMediaCache removes files in cacheDir that are not in `referenced` and
// are older than 7 days. The age cutoff prevents racing with in-flight cache
// writes that haven't yet been recorded as session artifacts.
func sweepMediaCache(cacheDir string, referenced map[string]bool, logger *slog.Logger) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return // directory may not exist yet
	}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		absPath := filepath.Join(cacheDir, e.Name())
		if referenced[absPath] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(absPath) == nil {
			removed++
		}
	}
	if removed > 0 {
		logger.Info("cleaned up unreferenced media cache", "dir", cacheDir, "removed", removed)
	}
}

// cleanupMediaCache is the legacy single-project entry point. It collects refs
// from one SessionStore and sweeps the cache rooted at projectRoot. Multi-
// tenant deployments use sweepMediaCache directly with a unioned ref set.
func cleanupMediaCache(projectRoot string, sessions *SessionStore, logger *slog.Logger) {
	referenced := map[string]bool{}
	collectMediaReferences(sessions, referenced)
	sweepMediaCache(filepath.Join(projectRoot, canvasMediaCacheDir), referenced, logger)
}

// cacheMediaToStore downloads/decodes media described by rawInput, writes it
// to the configured object store under a content-addressed key, and returns
// the public URL the client should hit to fetch it back.
//
// The key shape mirrors storage.Config.Key: <projectID>/<type>/<sha[:2]>/<sha><ext>.
// projectID may be empty for legacy single-project mode (storage.Config will
// substitute "_default").
func cacheMediaToStore(ctx context.Context, st s2.Storage, cfg storagecfg.Config, projectID, rawInput, hintedMediaType string) (string, error) {
	if st == nil {
		return "", errors.New("cacheMediaToStore: nil storage")
	}

	data, mediaType, _, ext, err := readMediaInput("", rawInput, hintedMediaType)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])

	bucket := classifyMedia(mediaType, hintedMediaType)
	key := cfg.Key(projectID, bucket, sha, ext)

	exists, err := st.Exists(ctx, key)
	if err != nil {
		return "", fmt.Errorf("storage exists %s: %w", key, err)
	}
	if !exists {
		md := s2.Metadata{}
		if mediaType != "" {
			md.Set("Content-Type", mediaType)
		}
		obj := s2.NewObjectBytes(key, data, s2.WithMetadata(md))
		if err := st.Put(ctx, obj); err != nil {
			return "", fmt.Errorf("storage put %s: %w", key, err)
		}
	}

	return cfg.PublicURL(key), nil
}

// classifyMedia returns the high-level media bucket ("image" / "video" /
// "audio" / "blob") that the cached object should be filed under. The
// MIME type wins when present; the caller-supplied hint is the fallback.
func classifyMedia(mimeType, hint string) string {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	}
	switch hint {
	case "image", "video", "audio":
		return hint
	}
	return "blob"
}

// handleCanvasTextGen processes a prompt to generate text via the LLM.
func (h *Handler) handleCanvasTextGen(ctx context.Context, req Request) Response {
	prompt, _ := req.Params["prompt"].(string)
	if prompt == "" {
		return h.invalidParams(req.ID, "prompt is required")
	}

	// Create a timeout context for generation
	gctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	resp, err := h.runtime.Run(gctx, api.Request{
		Prompt:    prompt,
		Ephemeral: true, // Do not persist canvas scratchpad generations
	})
	if err != nil {
		h.logger.Error("canvas text generation failed", "error", err)
		return h.internalError(req.ID, "Generation failed: "+err.Error())
	}

	if resp.Result == nil {
		return h.internalError(req.ID, "Model returned empty result")
	}

	return h.success(req.ID, map[string]any{"text": resp.Result.Output})
}
