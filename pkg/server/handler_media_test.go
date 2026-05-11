package server

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/api"
	coreevents "github.com/cinience/saker/pkg/core/events"
)

// ---------------------------------------------------------------------------
// isLowValueToolOutput
// ---------------------------------------------------------------------------

func TestMediaIsLowValueToolOutput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "empty", content: "", want: true},
		{name: "bracket_only_no_output", content: "[Bash] ", want: true},
		{name: "bracket_only_whitespace", content: "[Grep]  \t\n", want: true},
		{name: "no_matches_exact", content: "[Grep] no matches", want: true},
		{name: "no_matches_case_insensitive", content: "[Grep] No Matches", want: true},
		{name: "no_files_found", content: "[Glob] no files found", want: true},
		{name: "no_results", content: "[Bash] no results", want: true},
		{name: "no_such_file", content: "[Bash] no such file or directory", want: true},
		{name: "no_bracket_separator", content: "just some text", want: false},
		{name: "meaningful_output", content: "[Bash] 3 files changed", want: false},
		{name: "partial_match_not_exact", content: "[Grep] no matches found for pattern", want: false},
		{name: "bracket_with_content", content: "[Read] file contents here", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isLowValueToolOutput(tc.content)
			if got != tc.want {
				t.Errorf("isLowValueToolOutput(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// formatToolResult
// ---------------------------------------------------------------------------

func TestMediaFormatToolResult(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		toolName string
		output   interface{}
		want     string
	}{
		{name: "non_map_output", toolName: "Bash", output: "just a string", want: ""},
		{name: "nil_output", toolName: "Bash", output: nil, want: ""},
		{name: "map_no_output_key", toolName: "Read", output: map[string]any{"error": "oops"}, want: ""},
		{name: "map_empty_output", toolName: "Read", output: map[string]any{"output": ""}, want: ""},
		{name: "short_output", toolName: "Bash", output: map[string]any{"output": "hello world"}, want: "[Bash] hello world"},
		{name: "truncation_at_500", toolName: "Read",
			output: map[string]any{"output": repeatChar('x', 600)},
			want:   "[Read] " + repeatChar('x', 500) + "…"},
		{name: "exact_500_no_truncation", toolName: "Read",
			output: map[string]any{"output": repeatChar('a', 500)},
			want:   "[Read] " + repeatChar('a', 500)},
		{name: "output_at_501", toolName: "Read",
			output: map[string]any{"output": repeatChar('b', 501)},
			want:   "[Read] " + repeatChar('b', 500) + "…"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := formatToolResult(tc.toolName, tc.output)
			if got != tc.want {
				// For truncation tests, compare lengths if strings are too long to display.
				if len(got) > 80 || len(tc.want) > 80 {
					t.Errorf("formatToolResult: got len=%d, want len=%d", len(got), len(tc.want))
				} else {
					t.Errorf("formatToolResult(%q, ...) = %q, want %q", tc.toolName, got, tc.want)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// extractArtifacts — Path 1: structured metadata
// ---------------------------------------------------------------------------

func TestMediaExtractArtifactsStructuredMetadata(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		toolName string
		output   interface{}
		want     []Artifact
	}{
		{name: "valid_structured", toolName: "ImageRead",
			output: map[string]any{
				"metadata": map[string]any{
					"structured": map[string]any{
						"media_type": "image/png",
						"media_url":  "https://cdn.example.com/img.png",
					},
				},
			},
			want: []Artifact{{Type: "image/png", URL: "https://cdn.example.com/img.png", Name: "ImageRead"}},
		},
		{name: "missing_media_type", toolName: "ImageRead",
			output: map[string]any{
				"metadata": map[string]any{
					"structured": map[string]any{
						"media_url": "https://cdn.example.com/img.png",
					},
				},
			},
			want: nil,
		},
		{name: "missing_media_url", toolName: "ImageRead",
			output: map[string]any{
				"metadata": map[string]any{
					"structured": map[string]any{
						"media_type": "image/png",
					},
				},
			},
			want: nil,
		},
		{name: "structured_nil", toolName: "ImageRead",
			output: map[string]any{
				"metadata": map[string]any{
					"structured": nil,
				},
			},
			want: nil,
		},
		{name: "metadata_nil", toolName: "ImageRead",
			output: map[string]any{
				"metadata": nil,
			},
			want: nil,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractArtifacts(tc.toolName, tc.output)
			if !artifactsEqual(got, tc.want) {
				t.Errorf("extractArtifacts = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// extractArtifacts — Path 2: data metadata
// ---------------------------------------------------------------------------

func TestMediaExtractArtifactsDataMetadata(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		toolName string
		output   interface{}
		want     []Artifact
	}{
		{name: "image_with_absolute_path", toolName: "ImageRead",
			output: map[string]any{
				"metadata": map[string]any{
					"data": map[string]any{
						"media_type":    "image/png",
						"absolute_path": "/tmp/output/result.png",
					},
				},
			},
			want: []Artifact{{Type: "image", URL: "/api/files/tmp/output/result.png", Name: "ImageRead"}},
		},
		{name: "image_with_relative_path_fallback", toolName: "ImageRead",
			output: map[string]any{
				"metadata": map[string]any{
					"data": map[string]any{
						"media_type": "image/png",
						"path":       "output/result.png",
					},
				},
			},
			want: []Artifact{{Type: "image", URL: "/api/files/output/result.png", Name: "ImageRead"}},
		},
		{name: "absolute_path_preferred_over_path", toolName: "ImageRead",
			output: map[string]any{
				"metadata": map[string]any{
					"data": map[string]any{
						"media_type":    "image/png",
						"absolute_path": "/abs/path/img.png",
						"path":          "rel/path/img.png",
					},
				},
			},
			want: []Artifact{{Type: "image", URL: "/api/files/abs/path/img.png", Name: "ImageRead"}},
		},
		{name: "video_type", toolName: "VideoSampler",
			output: map[string]any{
				"metadata": map[string]any{
					"data": map[string]any{
						"media_type":    "video/mp4",
						"absolute_path": "/tmp/output/clip.mp4",
					},
				},
			},
			want: []Artifact{{Type: "video", URL: "/api/files/tmp/output/clip.mp4", Name: "VideoSampler"}},
		},
		{name: "audio_type", toolName: "AudioTool",
			output: map[string]any{
				"metadata": map[string]any{
					"data": map[string]any{
						"media_type":    "audio/wav",
						"absolute_path": "/tmp/output/sound.wav",
					},
				},
			},
			want: []Artifact{{Type: "audio", URL: "/api/files/tmp/output/sound.wav", Name: "AudioTool"}},
		},
		{name: "missing_mime", toolName: "ImageRead",
			output: map[string]any{
				"metadata": map[string]any{
					"data": map[string]any{
						"absolute_path": "/tmp/output/img.png",
					},
				},
			},
			want: nil,
		},
		{name: "missing_path", toolName: "ImageRead",
			output: map[string]any{
				"metadata": map[string]any{
					"data": map[string]any{
						"media_type": "image/png",
					},
				},
			},
			want: nil,
		},
		{name: "data_nil", toolName: "ImageRead",
			output: map[string]any{
				"metadata": map[string]any{
					"data": nil,
				},
			},
			want: nil,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractArtifacts(tc.toolName, tc.output)
			if !artifactsEqual(got, tc.want) {
				t.Errorf("extractArtifacts = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// extractArtifacts — Path 3: URL/path detection in output text
// ---------------------------------------------------------------------------

func TestMediaExtractArtifactsOutputPaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		toolName string
		output   interface{}
		want     []Artifact
	}{
		{name: "http_url_image",
			toolName: "Bash",
			output: map[string]any{
				"output": "Image saved to https://cdn.example.com/generated/image.png",
			},
			want: []Artifact{{Type: "image", URL: "https://cdn.example.com/generated/image.png", Name: "Bash"}},
		},
		{name: "http_url_with_query",
			toolName: "Bash",
			output: map[string]any{
				"output": "Result: https://cdn.example.com/img.png?Expires=123&Signature=abc",
			},
			want: []Artifact{{Type: "image", URL: "https://cdn.example.com/img.png?Expires=123&Signature=abc", Name: "Bash"}},
		},
		{name: "http_url_video",
			toolName: "VideoSampler",
			output: map[string]any{
				"output": "Clip at https://cdn.example.com/clip.mp4",
			},
			want: []Artifact{{Type: "video", URL: "https://cdn.example.com/clip.mp4", Name: "VideoSampler"}},
		},
		{name: "http_url_audio",
			toolName: "AudioTool",
			output: map[string]any{
				"output": "Track: https://cdn.example.com/track.mp3",
			},
			want: []Artifact{{Type: "audio", URL: "https://cdn.example.com/track.mp3", Name: "AudioTool"}},
		},
		{name: "absolute_path_image",
			toolName: "Bash",
			output: map[string]any{
				"output": "Saved to /home/user/output/render.png",
			},
			want: []Artifact{{Type: "image", URL: "/api/files/home/user/output/render.png", Name: "Bash"}},
		},
		{name: "relative_path_image",
			toolName: "Bash",
			output: map[string]any{
				"output": "Saved to output/render.png",
			},
			want: []Artifact{{Type: "image", URL: "/api/files/output/render.png", Name: "Bash"}},
		},
		{name: "multiple_urls_deduped",
			toolName: "Bash",
			output: map[string]any{
				"output": "See https://cdn.example.com/a.png and https://cdn.example.com/a.png again",
			},
			want: []Artifact{{Type: "image", URL: "https://cdn.example.com/a.png", Name: "Bash"}},
		},
		{name: "url_and_path_no_dedup_cross_type",
			toolName: "Bash",
			output: map[string]any{
				"output": "Remote https://cdn.example.com/a.png and local path /tmp/a.png",
			},
			want: []Artifact{
				{Type: "image", URL: "https://cdn.example.com/a.png", Name: "Bash"},
				{Type: "image", URL: "/api/files/tmp/a.png", Name: "Bash"},
			},
		},
		{name: "empty_output",
			toolName: "Bash",
			output:   map[string]any{"output": ""},
			want:     nil,
		},
		{name: "no_media_in_output",
			toolName: "Bash",
			output:   map[string]any{"output": "just regular text output"},
			want:     nil,
		},
		{name: "non_string_output",
			toolName: "Bash",
			output:   map[string]any{"output": 42},
			want:     nil,
		},
		{name: "non_map_payload",
			toolName: "Bash",
			output:   "not a map",
			want:     nil,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractArtifacts(tc.toolName, tc.output)
			if !artifactsEqual(got, tc.want) {
				t.Errorf("extractArtifacts = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// extractArtifacts — Path 1 takes precedence over Path 3
// ---------------------------------------------------------------------------

func TestMediaExtractArtifactsStructuredOverridesOutput(t *testing.T) {
	t.Parallel()

	// When structured metadata is present, it should be returned even if
	// the output text also contains media URLs.
	output := map[string]any{
		"metadata": map[string]any{
			"structured": map[string]any{
				"media_type": "image/png",
				"media_url":  "https://structured.example.com/img.png",
			},
		},
		"output": "Also see https://output.example.com/img.png",
	}
	got := extractArtifacts("Tool", output)
	want := []Artifact{{Type: "image/png", URL: "https://structured.example.com/img.png", Name: "Tool"}}
	if !artifactsEqual(got, want) {
		t.Errorf("structured metadata must take precedence: got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// detectMediaPaths
// ---------------------------------------------------------------------------

func TestMediaDetectMediaPaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want []mediaPathResult
	}{
		{name: "empty", text: "", want: nil},
		{name: "no_media_paths", text: "just some text", want: nil},
		{name: "absolute_png",
			text: "Saved to /home/user/output/render.png",
			want: []mediaPathResult{{path: "/home/user/output/render.png", mediaType: "image"}},
		},
		{name: "absolute_jpg",
			text: "Image at /tmp/snapshot.jpg",
			want: []mediaPathResult{{path: "/tmp/snapshot.jpg", mediaType: "image"}},
		},
		{name: "absolute_jpeg",
			text: "Photo /photos/portrait.jpeg",
			want: []mediaPathResult{{path: "/photos/portrait.jpeg", mediaType: "image"}},
		},
		{name: "absolute_video_mp4",
			text: "Video /tmp/clip.mp4 created",
			want: []mediaPathResult{{path: "/tmp/clip.mp4", mediaType: "video"}},
		},
		{name: "absolute_audio_mp3",
			text: "Audio /tmp/track.mp3 saved",
			want: []mediaPathResult{{path: "/tmp/track.mp3", mediaType: "audio"}},
		},
		{name: "relative_path_with_dir",
			text: "Image at output/render.png",
			want: []mediaPathResult{{path: "output/render.png", mediaType: "image"}},
		},
		{name: "relative_path_nested",
			text: "File src/assets/icon.webp",
			want: []mediaPathResult{{path: "src/assets/icon.webp", mediaType: "image"}},
		},
		{name: "bare_filename_excluded",
			text: "Created image.png",
			want: nil, // bare filenames lack directory context
		},
		{name: "multiple_paths",
			text: "See /tmp/a.png and /tmp/b.mp4 also output/c.wav",
			want: []mediaPathResult{
				{path: "/tmp/a.png", mediaType: "image"},
				{path: "/tmp/b.mp4", mediaType: "video"},
				{path: "output/c.wav", mediaType: "audio"},
			},
		},
		{name: "dedup_same_path",
			text: "/tmp/img.png mentioned /tmp/img.png again",
			want: []mediaPathResult{{path: "/tmp/img.png", mediaType: "image"}},
		},
		{name: "path_in_quotes",
			text: `file="/tmp/render.png"`,
			want: []mediaPathResult{{path: "/tmp/render.png", mediaType: "image"}},
		},
		{name: "gif_extension",
			text: "Animation at /tmp/anim.gif",
			want: []mediaPathResult{{path: "/tmp/anim.gif", mediaType: "image"}},
		},
		{name: "svg_extension",
			text: "SVG /tmp/chart.svg",
			want: []mediaPathResult{{path: "/tmp/chart.svg", mediaType: "image"}},
		},
		{name: "webm_extension",
			text: "Video /tmp/recording.webm",
			want: []mediaPathResult{{path: "/tmp/recording.webm", mediaType: "video"}},
		},
		{name: "mov_extension",
			text: "Clip /tmp/movie.mov",
			want: []mediaPathResult{{path: "/tmp/movie.mov", mediaType: "video"}},
		},
		{name: "ogg_extension",
			text: "Audio /tmp/sound.ogg",
			want: []mediaPathResult{{path: "/tmp/sound.ogg", mediaType: "audio"}},
		},
		{name: "flac_extension",
			text: "Track /tmp/lossless.flac",
			want: []mediaPathResult{{path: "/tmp/lossless.flac", mediaType: "audio"}},
		},
		{name: "wav_extension",
			text: "Sample /tmp/audio.wav",
			want: []mediaPathResult{{path: "/tmp/audio.wav", mediaType: "audio"}},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := detectMediaPaths(tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("detectMediaPaths(%q): got %d results, want %d\ngot=%v\nwant=%v",
					tc.text, len(got), len(tc.want), got, tc.want)
			}
			for i, g := range got {
				if g.path != tc.want[i].path || g.mediaType != tc.want[i].mediaType {
					t.Errorf("result[%d]: got {path=%q, type=%q}, want {path=%q, type=%q}",
						i, g.path, g.mediaType, tc.want[i].path, tc.want[i].mediaType)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detectMediaPath (backward compat wrapper)
// ---------------------------------------------------------------------------

func TestMediaDetectMediaPathBackwardCompat(t *testing.T) {
	t.Parallel()

	path, mediaType := detectMediaPath("Image /tmp/render.png")
	if path != "/tmp/render.png" || mediaType != "image" {
		t.Errorf("detectMediaPath: got (%q, %q), want (%q, %q)", path, mediaType, "/tmp/render.png", "image")
	}

	path, mediaType = detectMediaPath("no media here")
	if path != "" || mediaType != "" {
		t.Errorf("detectMediaPath empty: got (%q, %q), want empty", path, mediaType)
	}
}

// ---------------------------------------------------------------------------
// cacheArtifactMedia — non-HTTP URLs returned unchanged
// ---------------------------------------------------------------------------

func TestMediaCacheArtifactMediaNonHTTP(t *testing.T) {
	t.Parallel()

	h := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	cases := []struct {
		name string
		art  Artifact
	}{
		{name: "api_files_path", art: Artifact{Type: "image", URL: "/api/files/tmp/img.png", Name: "Read"}},
		{name: "relative_path", art: Artifact{Type: "image", URL: "output/img.png", Name: "Bash"}},
		{name: "data_uri", art: Artifact{Type: "image", URL: "data:image/png;base64,abc", Name: "Gen"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := h.cacheArtifactMedia(context.Background(), tc.art)
			if got != tc.art {
				t.Errorf("cacheArtifactMedia(%v) = %v, want unchanged", tc.art, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// cacheArtifactMedia — HTTP URL cached via project root
// ---------------------------------------------------------------------------

func TestMediaCacheArtifactMediaHTTPViaProjectRoot(t *testing.T) {
	pngBytes, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer server.Close()

	h, projectRoot := newMediaTestHandler(t)

	art := Artifact{Type: "image", URL: server.URL + "/test.png", Name: "Bash"}
	got := h.cacheArtifactMedia(context.Background(), art)

	if got.URL == art.URL {
		t.Errorf("expected URL to be cached (changed from %q), got %q", art.URL, got.URL)
	}
	if got.Type != art.Type {
		t.Errorf("type changed: got %q, want %q", got.Type, art.Type)
	}
	if got.Name != art.Name {
		t.Errorf("name changed: got %q, want %q", got.Name, art.Name)
	}

	// Verify the cached file actually exists on disk.
	cacheDir := filepath.Join(projectRoot, ".saker/canvas-media")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("expected at least one cached file in %s", cacheDir)
	}
}

// ---------------------------------------------------------------------------
// cacheArtifactAsync — cooldown (recently failed URL skipped)
// ---------------------------------------------------------------------------

func TestMediaCacheArtifactAsyncCooldownSkips(t *testing.T) {
	t.Parallel()

	store, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	thread := store.CreateThread("test")
	item := store.AppendItemWithArtifacts(thread.ID, "assistant", "x", "turn-1", []Artifact{
		{Type: "image", URL: "https://cdn.example.com/fail.png", Name: "Bash"},
	})

	h := &Handler{
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		cacheFailed:   sync.Map{},
		cacheInflight: sync.Map{},
	}

	// Store a future cooldown time for the URL.
	h.cacheFailed.Store("https://cdn.example.com/fail.png", time.Now().Add(10*time.Minute))

	h.cacheArtifactAsync(store, thread.ID, item.ID, Artifact{
		Type: "image", URL: "https://cdn.example.com/fail.png", Name: "Bash",
	})

	// Verify the item's artifact URL was NOT updated (still the original remote URL).
	gotItem, ok := store.GetItem(item.ID)
	if !ok {
		t.Fatalf("item not found")
	}
	if gotItem.Artifacts[0].URL != "https://cdn.example.com/fail.png" {
		t.Errorf("cooldown should have skipped caching, got URL = %q", gotItem.Artifacts[0].URL)
	}
}

// ---------------------------------------------------------------------------
// cacheArtifactAsync — expired cooldown is cleared and proceeds
// ---------------------------------------------------------------------------

func TestMediaCacheArtifactAsyncExpiredCooldownProceeds(t *testing.T) {
	pngBytes, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer server.Close()

	h, _ := newMediaTestHandler(t)

	store, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	thread := store.CreateThread("test")
	artURL := server.URL + "/img.png"
	item := store.AppendItemWithArtifacts(thread.ID, "assistant", "x", "turn-1", []Artifact{
		{Type: "image", URL: artURL, Name: "Bash"},
	})

	// Store a past cooldown time (already expired).
	h.cacheFailed.Store(artURL, time.Now().Add(-1*time.Minute))

	h.cacheArtifactAsync(store, thread.ID, item.ID, Artifact{Type: "image", URL: artURL, Name: "Bash"})

	// The expired cooldown entry should have been deleted.
	if _, ok := h.cacheFailed.Load(artURL); ok {
		t.Errorf("expired cooldown entry should have been deleted")
	}

	// The item's artifact URL should have been updated to a local cached path.
	gotItem, ok := store.GetItem(item.ID)
	if !ok {
		t.Fatalf("item not found")
	}
	if gotItem.Artifacts[0].URL == artURL {
		t.Errorf("artifact URL should have been updated from remote to local, still %q", gotItem.Artifacts[0].URL)
	}
}

// ---------------------------------------------------------------------------
// cacheArtifactAsync — dedup (concurrent download skipped)
// ---------------------------------------------------------------------------

func TestMediaCacheArtifactAsyncDedupSkips(t *testing.T) {
	t.Parallel()

	store, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	thread := store.CreateThread("test")
	artURL := "https://cdn.example.com/dedup.png"
	item := store.AppendItemWithArtifacts(thread.ID, "assistant", "x", "turn-1", []Artifact{
		{Type: "image", URL: artURL, Name: "Bash"},
	})

	h := &Handler{
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		cacheFailed:   sync.Map{},
		cacheInflight: sync.Map{},
	}

	// Simulate an in-flight download for the same URL.
	h.cacheInflight.Store(artURL, struct{}{})

	h.cacheArtifactAsync(store, thread.ID, item.ID, Artifact{Type: "image", URL: artURL, Name: "Bash"})

	// The item's artifact URL should NOT have been updated (dedup skip).
	gotItem, ok := store.GetItem(item.ID)
	if !ok {
		t.Fatalf("item not found")
	}
	if gotItem.Artifacts[0].URL != artURL {
		t.Errorf("dedup should have skipped, got URL = %q", gotItem.Artifacts[0].URL)
	}
}

// ---------------------------------------------------------------------------
// cacheArtifactAsync — caching failure stores cooldown
// ---------------------------------------------------------------------------

func TestMediaCacheArtifactAsyncFailureStoresCooldown(t *testing.T) {
	h, _ := newMediaTestHandler(t)

	store, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	thread := store.CreateThread("test")
	// Use a URL that points to nothing -- caching will fail and return original URL.
	artURL := "https://nonexistent.invalid/fail.png"
	item := store.AppendItemWithArtifacts(thread.ID, "assistant", "x", "turn-1", []Artifact{
		{Type: "image", URL: artURL, Name: "Bash"},
	})

	h.cacheArtifactAsync(store, thread.ID, item.ID, Artifact{Type: "image", URL: artURL, Name: "Bash"})

	// The URL should now be in cacheFailed with a future cooldown time.
	failUntil, ok := h.cacheFailed.Load(artURL)
	if !ok {
		t.Errorf("expected URL to be in cacheFailed after caching failure")
	} else {
		if !time.Now().Before(failUntil.(time.Time)) {
			t.Errorf("cooldown time should be in the future, got %v", failUntil)
		}
	}

	// The in-flight entry should have been cleaned up.
	if _, ok := h.cacheInflight.Load(artURL); ok {
		t.Errorf("in-flight entry should have been removed after completion")
	}
}

// ---------------------------------------------------------------------------
// migrateRemoteArtifacts — dedup path (no actual download)
// ---------------------------------------------------------------------------

func TestMediaMigrateRemoteArtifactsDedup(t *testing.T) {
	t.Parallel()

	store, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	thread := store.CreateThread("test")

	// Item with a remote artifact URL (should trigger async caching).
	store.AppendItemWithArtifacts(thread.ID, "assistant", "x", "turn-1", []Artifact{
		{Type: "image", URL: "https://cdn.example.com/img.png", Name: "Bash"},
	})
	// Item with a local artifact URL (should be skipped).
	store.AppendItemWithArtifacts(thread.ID, "assistant", "y", "turn-2", []Artifact{
		{Type: "image", URL: "/api/files/tmp/local.png", Name: "Read"},
	})
	// Item with no artifacts (should be skipped).
	store.AppendItem(thread.ID, "assistant", "z", "turn-3")

	h := &Handler{
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		cacheFailed:   sync.Map{},
		cacheInflight: sync.Map{},
	}

	// Pre-populate cacheInflight for the remote URL to prevent actual download
	// (we're just testing that migrateRemoteArtifacts calls cacheArtifactAsync
	// for remote URLs and skips local ones).
	h.cacheInflight.Store("https://cdn.example.com/img.png", struct{}{})

	h.migrateRemoteArtifacts(store, thread.ID)

	// The in-flight entry should still exist (dedup prevented re-download).
	if _, ok := h.cacheInflight.Load("https://cdn.example.com/img.png"); !ok {
		t.Errorf("expected dedup to keep in-flight entry for remote URL")
	}

	// Local URL should not be in cacheInflight.
	if _, ok := h.cacheInflight.Load("/api/files/tmp/local.png"); ok {
		t.Errorf("local URL should never be added to cacheInflight")
	}
}

// ---------------------------------------------------------------------------
// migrateRemoteArtifacts — actually caches a remote artifact
// ---------------------------------------------------------------------------

func TestMediaMigrateRemoteArtifactsCachesRemote(t *testing.T) {
	pngBytes, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer server.Close()

	h, _ := newMediaTestHandler(t)

	store, err := NewSessionStore("")
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	thread := store.CreateThread("test")
	artURL := server.URL + "/migrate.png"
	store.AppendItemWithArtifacts(thread.ID, "assistant", "x", "turn-1", []Artifact{
		{Type: "image", URL: artURL, Name: "Bash"},
	})
	// Local artifact should stay untouched.
	localURL := "/api/files/tmp/local.png"
	store.AppendItemWithArtifacts(thread.ID, "assistant", "y", "turn-2", []Artifact{
		{Type: "image", URL: localURL, Name: "Read"},
	})

	h.migrateRemoteArtifacts(store, thread.ID)

	// Check the remote artifact was updated to a local cached URL.
	items := store.GetItems(thread.ID)
	var remoteUpdated, localUntouched bool
	for _, item := range items {
		for _, a := range item.Artifacts {
			if a.Name == "Bash" && a.URL != artURL {
				remoteUpdated = true
			}
			if a.Name == "Read" && a.URL == localURL {
				localUntouched = true
			}
		}
	}
	if !remoteUpdated {
		t.Errorf("remote artifact URL should have been updated from %q", artURL)
	}
	if !localUntouched {
		t.Errorf("local artifact URL should remain unchanged at %q", localURL)
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// newMediaTestHandler creates a Handler backed by a real api.Runtime with a
// temp project root, suitable for media caching tests.
func newMediaTestHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	root := t.TempDir()
	sakerDir := filepath.Join(root, ".saker")
	if err := os.MkdirAll(sakerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sakerDir, "settings.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot:  root,
		Model:        noopModel{},
		SystemPrompt: "test",
	})
	if err != nil {
		t.Fatalf("create test runtime: %v", err)
	}
	t.Cleanup(func() { rt.Close() })

	h := &Handler{
		runtime:       rt,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		clients:       map[string]*wsClient{},
		subscribers:   map[string]map[string]*wsClient{},
		approvals:     map[string]chan coreevents.PermissionDecisionType{},
		questions:     map[string]chan map[string]string{},
		cancels:       map[string]context.CancelFunc{},
		turnThreads:   map[string]string{},
		cacheFailed:   sync.Map{},
		cacheInflight: sync.Map{},
	}
	return h, root
}

func repeatChar(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func artifactsEqual(got, want []Artifact) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
