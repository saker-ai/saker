package tui

import (
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saker-ai/saker/pkg/api"
)

// createTestPNG creates a small PNG file and returns its path.
func createTestPNG(t *testing.T, w, h int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.png")

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	return path
}

func TestDetectImageProtocol_Default(t *testing.T) {
	// Unset relevant env vars for a clean test.
	orig := map[string]string{
		"TERM_PROGRAM":     os.Getenv("TERM_PROGRAM"),
		"TERM":             os.Getenv("TERM"),
		"ITERM_SESSION_ID": os.Getenv("ITERM_SESSION_ID"),
	}
	defer func() {
		for k, v := range orig {
			os.Setenv(k, v)
		}
	}()

	os.Setenv("TERM_PROGRAM", "")
	os.Setenv("TERM", "xterm-256color")
	os.Setenv("ITERM_SESSION_ID", "")

	proto := DetectImageProtocol()
	if proto != ProtocolNone {
		t.Fatalf("expected ProtocolNone for generic terminal, got %d", proto)
	}
}

func TestDetectImageProtocol_Kitty(t *testing.T) {
	orig := os.Getenv("TERM_PROGRAM")
	defer os.Setenv("TERM_PROGRAM", orig)

	os.Setenv("TERM_PROGRAM", "kitty")
	if DetectImageProtocol() != ProtocolKitty {
		t.Fatal("expected ProtocolKitty for TERM_PROGRAM=kitty")
	}
}

func TestDetectImageProtocol_WezTerm(t *testing.T) {
	orig := os.Getenv("TERM_PROGRAM")
	defer os.Setenv("TERM_PROGRAM", orig)

	os.Setenv("TERM_PROGRAM", "WezTerm")
	if DetectImageProtocol() != ProtocolKitty {
		t.Fatal("expected ProtocolKitty for WezTerm")
	}
}

func TestDetectImageProtocol_ITerm2(t *testing.T) {
	origTP := os.Getenv("TERM_PROGRAM")
	origIS := os.Getenv("ITERM_SESSION_ID")
	defer func() {
		os.Setenv("TERM_PROGRAM", origTP)
		os.Setenv("ITERM_SESSION_ID", origIS)
	}()

	os.Setenv("TERM_PROGRAM", "iTerm.app")
	os.Setenv("ITERM_SESSION_ID", "")
	if DetectImageProtocol() != ProtocolITerm2 {
		t.Fatal("expected ProtocolITerm2 for iTerm.app")
	}

	os.Setenv("TERM_PROGRAM", "")
	os.Setenv("ITERM_SESSION_ID", "w0t0p0:12345")
	if DetectImageProtocol() != ProtocolITerm2 {
		t.Fatal("expected ProtocolITerm2 for ITERM_SESSION_ID set")
	}
}

func TestRenderImage_Fallback(t *testing.T) {
	// Force no protocol.
	origTP := os.Getenv("TERM_PROGRAM")
	origTerm := os.Getenv("TERM")
	origIS := os.Getenv("ITERM_SESSION_ID")
	defer func() {
		os.Setenv("TERM_PROGRAM", origTP)
		os.Setenv("TERM", origTerm)
		os.Setenv("ITERM_SESSION_ID", origIS)
	}()
	os.Setenv("TERM_PROGRAM", "")
	os.Setenv("TERM", "dumb")
	os.Setenv("ITERM_SESSION_ID", "")

	path := createTestPNG(t, 4, 4)
	result := RenderImage(path, 40)
	if !strings.Contains(result, "[Image: test.png]") {
		t.Fatalf("expected fallback placeholder, got %q", result)
	}
}

func TestRenderImage_MissingFile(t *testing.T) {
	result := RenderImage("/nonexistent/image.png", 40)
	if !strings.Contains(result, "read error") {
		t.Fatalf("expected read error, got %q", result)
	}
}

func TestRenderImageData_Empty(t *testing.T) {
	result := RenderImageData(nil, "empty.png", 40)
	if !strings.Contains(result, "empty") {
		t.Fatalf("expected empty placeholder, got %q", result)
	}
}

func TestRenderKitty_SingleChunk(t *testing.T) {
	// Small image produces single chunk.
	path := createTestPNG(t, 2, 2)
	data, _ := os.ReadFile(path)
	b64 := base64.StdEncoding.EncodeToString(data)

	if len(b64) > kittyChunkSize {
		t.Skip("test image too large for single chunk test")
	}

	result := renderKitty(data, 30)
	if !strings.HasPrefix(result, "\033_Ga=T,f=100,t=d,") {
		t.Fatalf("expected kitty escape prefix, got %q", result[:min(len(result), 40)])
	}
	if !strings.Contains(result, b64) {
		t.Fatal("expected base64 data in output")
	}
	if !strings.Contains(result, "\033\\") {
		t.Fatal("expected kitty escape terminator")
	}
}

func TestRenderITerm2(t *testing.T) {
	path := createTestPNG(t, 2, 2)
	data, _ := os.ReadFile(path)

	result := renderITerm2(data, "test.png", 30)
	if !strings.HasPrefix(result, "\033]1337;File=inline=1;") {
		t.Fatalf("expected iTerm2 escape prefix, got %q", result[:min(len(result), 40)])
	}
	if !strings.Contains(result, "preserveAspectRatio=1") {
		t.Fatal("expected preserveAspectRatio in output")
	}
	if !strings.Contains(result, "\a") {
		t.Fatal("expected BEL terminator")
	}
}

func TestImageCellSize(t *testing.T) {
	path := createTestPNG(t, 100, 50)
	data, _ := os.ReadFile(path)

	cols, rows := imageCellSize(data, 40)
	if cols != 40 {
		t.Fatalf("expected cols=40, got %d", cols)
	}
	// 100x50 image at 40 cols: rows = 40 * 50/100 / 2 = 10
	if rows != 10 {
		t.Fatalf("expected rows=10 for 100x50 image at 40 cols, got %d", rows)
	}
}

func TestImageCellSize_InvalidData(t *testing.T) {
	cols, rows := imageCellSize([]byte("not an image"), 60)
	if cols != 60 || rows != 30 {
		t.Fatalf("expected fallback (60, 30), got (%d, %d)", cols, rows)
	}
}

func TestExtractImagePaths(t *testing.T) {
	// Simulate a tool_execution_result event with artifacts.
	evt := api.StreamEvent{
		Output: map[string]any{
			"metadata": map[string]any{
				"artifacts": []any{
					map[string]any{
						"path": "/tmp/frame_001.png",
						"kind": "image",
					},
					map[string]any{
						"path": "/tmp/data.json",
						"kind": "json",
					},
					map[string]any{
						"path": "/tmp/photo.jpg",
						"kind": "",
					},
				},
			},
		},
	}

	paths := extractImagePaths(evt)
	if len(paths) != 2 {
		t.Fatalf("expected 2 image paths, got %d: %v", len(paths), paths)
	}
	if paths[0] != "/tmp/frame_001.png" {
		t.Fatalf("expected first path /tmp/frame_001.png, got %s", paths[0])
	}
	if paths[1] != "/tmp/photo.jpg" {
		t.Fatalf("expected second path /tmp/photo.jpg, got %s", paths[1])
	}
}

func TestExtractImagePaths_NoArtifacts(t *testing.T) {
	evt := api.StreamEvent{Output: map[string]any{"output": "hello"}}
	paths := extractImagePaths(evt)
	if len(paths) != 0 {
		t.Fatalf("expected 0 paths, got %d", len(paths))
	}
}
