package server

import "testing"

func TestIsLowValueToolOutput(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"empty", "", true},
		{"no matches", "[Glob] no matches", true},
		{"no files found", "[Grep] No files found", true},
		{"no results", "[Bash] no results", true},
		{"no such file", "[Bash] No such file or directory", true},
		{"real output", "[Bash] hello world", false},
		{"tool output", "[Glob] file1.go\nfile2.go", false},
		{"no bracket", "just some text", false},
		{"partial match", "[Glob] no matches found here", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLowValueToolOutput(tt.content)
			if got != tt.want {
				t.Errorf("isLowValueToolOutput(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestDetectMediaPath(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantPath string
		wantType string
	}{
		{"absolute png", `saved to /home/user/output/image.png done`, "/home/user/output/image.png", "image"},
		{"absolute jpg", `file: /tmp/photo.jpg`, "/tmp/photo.jpg", "image"},
		{"absolute mp4", `video at /data/clip.mp4 ready`, "/data/clip.mp4", "video"},
		{"absolute mp3", `audio: /out/song.mp3`, "/out/song.mp3", "audio"},
		{"quoted path", `"path": "/output/test.png"`, "/output/test.png", "image"},
		{"no media ext", `saved to /home/user/data.json`, "", ""},
		{"no path", `hello world`, "", ""},
		{"bare filename", `image.png`, "", ""},
		{"relative with dir", `output/images/cat.png`, "output/images/cat.png", "image"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, mediaType := detectMediaPath(tt.text)
			if path != tt.wantPath {
				t.Errorf("path = %q, want %q", path, tt.wantPath)
			}
			if mediaType != tt.wantType {
				t.Errorf("type = %q, want %q", mediaType, tt.wantType)
			}
		})
	}
}

func TestFormatToolResult(t *testing.T) {
	tests := []struct {
		name   string
		tool   string
		output interface{}
		want   string
	}{
		{"nil output", "Bash", nil, ""},
		{"empty map", "Bash", map[string]any{}, ""},
		{"with output", "Bash", map[string]any{"output": "hello"}, "[Bash] hello"},
		{"truncated", "Bash", map[string]any{"output": string(make([]byte, 600))}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatToolResult(tt.tool, tt.output)
			if tt.name == "truncated" {
				if len(got) > 520 {
					t.Errorf("expected truncation, got len=%d", len(got))
				}
				return
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractArtifacts(t *testing.T) {
	t.Run("structured metadata", func(t *testing.T) {
		output := map[string]any{
			"metadata": map[string]any{
				"structured": map[string]any{
					"media_type": "image",
					"media_url":  "https://example.com/img.png",
				},
			},
		}
		arts := extractArtifacts("generate_image", output)
		if len(arts) != 1 || arts[0].Type != "image" || arts[0].URL != "https://example.com/img.png" {
			t.Fatalf("unexpected artifacts: %+v", arts)
		}
	})

	t.Run("data metadata", func(t *testing.T) {
		output := map[string]any{
			"metadata": map[string]any{
				"data": map[string]any{
					"media_type":    "image/png",
					"absolute_path": "/home/user/photo.png",
				},
			},
		}
		arts := extractArtifacts("ImageRead", output)
		if len(arts) != 1 || arts[0].Type != "image" || arts[0].URL != "/api/files/home/user/photo.png" {
			t.Fatalf("unexpected artifacts: %+v", arts)
		}
	})

	t.Run("output text path", func(t *testing.T) {
		output := map[string]any{
			"output": "Image saved to /output/ai-image/elephant.png successfully",
		}
		arts := extractArtifacts("Bash", output)
		if len(arts) != 1 || arts[0].Type != "image" || arts[0].URL != "/api/files/output/ai-image/elephant.png" {
			t.Fatalf("unexpected artifacts: %+v", arts)
		}
	})

	t.Run("no media", func(t *testing.T) {
		output := map[string]any{
			"output": "command completed successfully",
		}
		arts := extractArtifacts("Bash", output)
		if len(arts) != 0 {
			t.Fatalf("expected empty, got %+v", arts)
		}
	})

	t.Run("multiple paths", func(t *testing.T) {
		output := map[string]any{
			"output": "Extracted 3 frames:\n  - /tmp/frames/frame_001.jpg\n  - /tmp/frames/frame_002.jpg\n  - /tmp/frames/frame_003.jpg",
		}
		arts := extractArtifacts("video_sampler", output)
		if len(arts) != 3 {
			t.Fatalf("expected 3 artifacts, got %d: %+v", len(arts), arts)
		}
		for _, a := range arts {
			if a.Type != "image" {
				t.Errorf("expected image type, got %s", a.Type)
			}
		}
	})

	t.Run("mixed media types", func(t *testing.T) {
		output := map[string]any{
			"output": "Captured:\n  - /tmp/frame.jpg\n  - /tmp/audio.wav",
		}
		arts := extractArtifacts("stream_capture", output)
		if len(arts) != 2 {
			t.Fatalf("expected 2 artifacts, got %d: %+v", len(arts), arts)
		}
		if arts[0].Type != "image" {
			t.Errorf("first artifact: expected image, got %s", arts[0].Type)
		}
		if arts[1].Type != "audio" {
			t.Errorf("second artifact: expected audio, got %s", arts[1].Type)
		}
	})
}
