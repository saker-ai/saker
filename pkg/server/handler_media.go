package server

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// isLowValueToolOutput returns true if the tool output is noise that shouldn't
// be persisted in the chat (e.g. "no matches", empty grep, short errors).
func isLowValueToolOutput(content string) bool {
	if content == "" {
		return true
	}
	// Extract the output part after "[ToolName] ".
	idx := strings.Index(content, "] ")
	if idx < 0 {
		return false
	}
	output := strings.TrimSpace(content[idx+2:])
	if output == "" {
		return true
	}
	lower := strings.ToLower(output)
	for _, p := range []string{
		"no matches",
		"no files found",
		"no results",
		"no such file or directory",
	} {
		if lower == p {
			return true
		}
	}
	return false
}

// formatToolResult extracts a readable summary from a tool result payload.
func formatToolResult(toolName string, output interface{}) string {
	payload, ok := output.(map[string]any)
	if !ok {
		return ""
	}
	out, _ := payload["output"].(string)
	if out == "" {
		return ""
	}
	// Truncate very long outputs to keep thread items manageable.
	const maxLen = 500
	summary := out
	if len(summary) > maxLen {
		summary = summary[:maxLen] + "…"
	}
	return fmt.Sprintf("[%s] %s", toolName, summary)
}

// extractArtifacts inspects a tool_execution_result output for media references.
func extractArtifacts(toolName string, output interface{}) []Artifact {
	payload, ok := output.(map[string]any)
	if !ok {
		return nil
	}

	metadata, _ := payload["metadata"].(map[string]any)

	if metadata != nil {
		// Path 1: structured metadata (aigo tools) — media_type + media_url
		if structured, _ := metadata["structured"].(map[string]any); structured != nil {
			mediaType, _ := structured["media_type"].(string)
			mediaURL, _ := structured["media_url"].(string)
			if mediaType != "" && mediaURL != "" {
				return []Artifact{{Type: mediaType, URL: mediaURL, Name: toolName}}
			}
		}

		// Path 2: data metadata (ImageRead etc.) — media_type + absolute_path/path
		if data, _ := metadata["data"].(map[string]any); data != nil {
			mime, _ := data["media_type"].(string)
			filePath, _ := data["absolute_path"].(string)
			if filePath == "" {
				filePath, _ = data["path"].(string)
			}
			if mime != "" && filePath != "" {
				artType := "image"
				if strings.HasPrefix(mime, "video/") {
					artType = "video"
				} else if strings.HasPrefix(mime, "audio/") {
					artType = "audio"
				}
				url := "/api/files/" + filePath
				if strings.HasPrefix(filePath, "/") {
					url = "/api/files" + filePath
				}
				return []Artifact{{Type: artType, URL: url, Name: toolName}}
			}
		}
	}

	// Path 3: detect media URLs or file paths in output text (e.g. Bash tools, video_sampler).
	if out, _ := payload["output"].(string); out != "" {
		var artifacts []Artifact
		seen := make(map[string]bool)

		// 3a: HTTP(S) media URLs (e.g. DashScope image generation response).
		for _, match := range mediaURLRe.FindAllString(out, -1) {
			if seen[match] {
				continue
			}
			seen[match] = true
			ext := strings.ToLower(filepath.Ext(strings.SplitN(match, "?", 2)[0]))
			artType := "image"
			if videoExts[ext] {
				artType = "video"
			} else if audioExts[ext] {
				artType = "audio"
			}
			artifacts = append(artifacts, Artifact{Type: artType, URL: match, Name: toolName})
		}

		// 3b: local file paths (absolute or relative).
		for _, filePath := range detectMediaPaths(out) {
			url := "/api/files/" + filePath.path
			if strings.HasPrefix(filePath.path, "/") {
				url = "/api/files" + filePath.path
			}
			if seen[url] {
				continue
			}
			seen[url] = true
			artifacts = append(artifacts, Artifact{Type: filePath.mediaType, URL: url, Name: toolName})
		}

		if len(artifacts) > 0 {
			return artifacts
		}
	}

	return nil
}

// mediaPathResult holds a detected media path and its type.
type mediaPathResult struct {
	path      string
	mediaType string
}

// mediaPathRe matches file paths (absolute or relative) ending with known media extensions.
var mediaPathRe = regexp.MustCompile(`(?:^|[\s"'=])(/[^\s"']+\.(?:png|jpe?g|gif|webp|svg|mp4|webm|mov|mp3|wav|ogg|flac))(?:\s|$|["'])`)

// relativeMediaPathRe matches relative file paths (must contain at least one /)
// with known media extensions. Bare filenames like "image.png" are excluded
// because they lack directory context and can't be resolved.
var relativeMediaPathRe = regexp.MustCompile(`(?:^|[\s"'=])([\w.][\w./_-]*/[\w./_-]*\.(?:png|jpe?g|gif|webp|svg|mp4|webm|mov|mp3|wav|ogg|flac))(?:\s|$|["'])`)

// mediaURLRe matches HTTP(S) URLs ending with known media extensions,
// including optional query strings (e.g. signed URLs with ?Expires=...&Signature=...).
var mediaURLRe = regexp.MustCompile(`https?://[^\s"']+\.(?:png|jpe?g|gif|webp|svg|mp4|webm|mov|mp3|wav|ogg)(?:\?[^\s"']*)?`)

// imageExts, videoExts, audioExts classify media extensions.
var (
	imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true}
	videoExts = map[string]bool{".mp4": true, ".webm": true, ".mov": true}
	audioExts = map[string]bool{".mp3": true, ".wav": true, ".ogg": true, ".flac": true}
)

// detectMediaPaths scans text for all file paths (absolute or relative) with known media extensions.
func detectMediaPaths(text string) []mediaPathResult {
	seen := make(map[string]bool)
	var results []mediaPathResult

	// Find all absolute paths.
	for _, match := range mediaPathRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 || seen[match[1]] {
			continue
		}
		p := match[1]
		ext := strings.ToLower(filepath.Ext(p))
		var mediaType string
		switch {
		case imageExts[ext]:
			mediaType = "image"
		case videoExts[ext]:
			mediaType = "video"
		case audioExts[ext]:
			mediaType = "audio"
		default:
			continue
		}
		seen[p] = true
		results = append(results, mediaPathResult{path: p, mediaType: mediaType})
	}

	// Find all relative paths.
	for _, match := range relativeMediaPathRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 || seen[match[1]] {
			continue
		}
		p := match[1]
		ext := strings.ToLower(filepath.Ext(p))
		var mediaType string
		switch {
		case imageExts[ext]:
			mediaType = "image"
		case videoExts[ext]:
			mediaType = "video"
		case audioExts[ext]:
			mediaType = "audio"
		default:
			continue
		}
		seen[p] = true
		results = append(results, mediaPathResult{path: p, mediaType: mediaType})
	}

	return results
}

// cacheArtifactMedia downloads remote media URLs and returns an artifact
// pointing to a locally cached copy. If the URL is already local or caching
// fails, returns the original artifact unchanged.
//
// When an object store is configured (h.objectStore != nil), new writes are
// routed through it and the returned URL is /media/<key>. Otherwise we fall
// back to the legacy on-disk path under <projectRoot>/.saker/canvas-media.
func (h *Handler) cacheArtifactMedia(a Artifact) Artifact {
	if !strings.HasPrefix(a.URL, "http://") && !strings.HasPrefix(a.URL, "https://") {
		return a
	}
	if store, cfg := h.objectStoreSnapshot(); store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		url, err := cacheMediaToStore(ctx, store, cfg, "", a.URL, a.Type)
		if err != nil {
			h.logger.Warn("failed to cache media artifact via object store", "url", a.URL, "error", err)
			return a
		}
		return Artifact{Type: a.Type, URL: url, Name: a.Name}
	}
	result, err := cacheMediaForProject(h.runtime.ProjectRoot(), a.URL, a.Type)
	if err != nil {
		h.logger.Warn("failed to cache media artifact", "url", a.URL, "error", err)
		return a
	}
	return Artifact{Type: a.Type, URL: result.URL, Name: a.Name}
}

// cacheArtifactAsync downloads a remote artifact URL in the background, then
// updates the persisted artifact and notifies connected clients.
// It deduplicates concurrent downloads and skips recently failed URLs.
//
// The caller passes the per-request SessionStore explicitly because this
// runs in a background goroutine — the request's ctx (and therefore the
// scope) is gone by the time the download finishes. Capturing the store at
// launch keeps the artifact update routed to the correct project.
func (h *Handler) cacheArtifactAsync(store *SessionStore, threadID, itemID string, a Artifact) {
	// Skip URLs that recently failed (10-minute cooldown).
	if failUntil, ok := h.cacheFailed.Load(a.URL); ok {
		if time.Now().Before(failUntil.(time.Time)) {
			return
		}
		h.cacheFailed.Delete(a.URL)
	}

	// Deduplicate concurrent downloads of the same URL.
	if _, loaded := h.cacheInflight.LoadOrStore(a.URL, struct{}{}); loaded {
		return // another goroutine is already downloading this URL
	}
	defer h.cacheInflight.Delete(a.URL)

	cached := h.cacheArtifactMedia(a)
	if cached.URL == a.URL {
		// Caching failed — remember for cooldown. Log so signed-URL
		// expiration and similar issues are visible: without this the
		// thread silently loses media on the next reload because
		// cacheArtifactMedia's warn never reaches the operator.
		h.logger.Warn("artifact cache failed; image will expire",
			"thread", threadID,
			"item", itemID,
			"url", a.URL,
			"type", a.Type,
		)
		h.cacheFailed.Store(a.URL, time.Now().Add(10*time.Minute))
		return
	}
	if store.UpdateItemArtifact(itemID, a.URL, cached.URL) {
		if updated, ok := store.GetItem(itemID); ok {
			h.notifySubscribers(threadID, "thread/item_updated", updated)
		}
	}
}

// migrateRemoteArtifacts scans a thread's items for remote artifact URLs
// and caches them locally in the background. Already-cached files are
// deduplicated by content hash so this is idempotent.
func (h *Handler) migrateRemoteArtifacts(store *SessionStore, threadID string) {
	items := store.GetItems(threadID)
	for _, item := range items {
		for _, a := range item.Artifacts {
			if strings.HasPrefix(a.URL, "http://") || strings.HasPrefix(a.URL, "https://") {
				h.cacheArtifactAsync(store, threadID, item.ID, a)
			}
		}
	}
}

// detectMediaPath scans text for the first file path with a known media extension.
// Kept for backward compatibility.
func detectMediaPath(text string) (path, mediaType string) {
	results := detectMediaPaths(text)
	if len(results) == 0 {
		return "", ""
	}
	return results[0].path, results[0].mediaType
}
