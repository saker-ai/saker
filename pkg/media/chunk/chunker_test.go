package chunk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	if opts.Duration != 30 {
		t.Errorf("DefaultOptions().Duration = %v, want 30", opts.Duration)
	}
	if opts.Overlap != 5 {
		t.Errorf("DefaultOptions().Overlap = %v, want 5", opts.Overlap)
	}
	if opts.MaxWidth != 480 {
		t.Errorf("DefaultOptions().MaxWidth = %v, want 480", opts.MaxWidth)
	}
	if opts.FPS != 5 {
		t.Errorf("DefaultOptions().FPS = %v, want 5", opts.FPS)
	}
	if !opts.SkipStillFrames {
		t.Errorf("DefaultOptions().SkipStillFrames = %v, want true", opts.SkipStillFrames)
	}
}

func TestSupportedExtensions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ext  string
		want bool
	}{
		{".mp4", true},
		{".mov", true},
		{".avi", true},
		{".mkv", true},
		{".webm", true},
		{".MP4", false}, // map keys are lowercase, this won't match
		{".flv", false},
		{".wmv", false},
		{".txt", false},
		{".mp3", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			t.Parallel()
			got := SupportedExtensions[tt.ext]
			if got != tt.want {
				t.Errorf("SupportedExtensions[%q] = %v, want %v", tt.ext, got, tt.want)
			}
		})
	}
}

func TestScanDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create video files and non-video files.
	videoFiles := []string{"movie.mp4", "clip.mov", "video.avi", "film.mkv", "stream.webm"}
	nonVideoFiles := []string{"readme.txt", "data.json", "audio.mp3", "image.png"}

	for _, f := range videoFiles {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range nonVideoFiles {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := ScanDirectory(dir, nil)
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}
	if len(files) != len(videoFiles) {
		t.Errorf("expected %d files, got %d: %v", len(videoFiles), len(files), files)
	}

	// Test with custom extensions.
	customExt := map[string]bool{".mp4": true,".txt": true}
	files, err = ScanDirectory(dir, customExt)
	if err != nil {
		t.Fatalf("ScanDirectory custom: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files for custom extensions, got %d: %v", len(files), files)
	}

	// ScanDirectory on nonexistent directory: filepath.Walk calls the
	// walk function with an error, which returns nil (skip), so the
	// overall result is empty files, no error.
	files, err = ScanDirectory("/nonexistent/path/12345", nil)
	if err != nil {
		t.Errorf("ScanDirectory nonexistent dir: unexpected error %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files for nonexistent dir, got %d", len(files))
	}
}

func TestScanDirectory_NilExtensions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := ScanDirectory(dir, nil)
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("expected 1 file with nil extensions (defaults to SupportedExtensions), got %d", len(files))
	}
}

func TestParseDurationFromStderr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stderr string
		want   float64
		wantErr bool
	}{
		{
			name:   "standard ffmpeg duration line",
			stderr: "Input #0, mov,mp4, from 'test.mp4':\n  Duration: 01:23:45.67, start: 0.000000",
			want:   1*3600 + 23*60 + 45.67,
			wantErr: false,
		},
		{
			name:   "short video duration",
			stderr: "Duration: 00:00:30.00",
			want:   30.0,
			wantErr: false,
		},
		{
			name:   "hours only",
			stderr: "Duration: 02:00:00.00",
			want:   7200.0,
			wantErr: false,
		},
		{
			name:   "no Duration line",
			stderr: "some other output without duration",
			want:   0,
			wantErr: true,
		},
		{
			name:   "empty stderr",
			stderr: "",
			want:   0,
			wantErr: true,
		},
		{
			name:   "malformed duration missing parts",
			stderr: "Duration: 01:02",
			want:   0,
			wantErr: true,
		},
		{
			name:   "non-numeric duration parts",
			stderr: "Duration: aa:bb:cc",
			want:   0,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseDurationFromStderr(tt.stderr)
			if tt.wantErr && err == nil {
				t.Errorf("parseDurationFromStderr(%q): expected error, got nil", tt.stderr)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("parseDurationFromStderr(%q): unexpected error: %v", tt.stderr, err)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("parseDurationFromStderr(%q) = %v, want %v", tt.stderr, got, tt.want)
			}
		})
	}
}

func TestParseFPS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stderr string
		want   float64
	}{
		{
			name:   "standard fps",
			stderr: "Stream #0:0: Video: h264, 29.97 fps, 480x270",
			want:   29.97,
		},
		{
			name:   "integer fps",
			stderr: "25 fps, 1280x720",
			want:   25.0,
		},
		{
			name:   "fps at end of line",
			stderr: "Video: mpeg4, 23.976 fps",
			want:   23.976,
		},
		{
			name:   "no fps present",
			stderr: "some output without fps info",
			want:   0,
		},
		{
			name:   "fps after comma",
			stderr: "Video: h264, , 29.97 fps, 480x270",
			want:   29.97,
		},
		{
			name:   "empty string",
			stderr: "",
			want:   0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseFPS(tt.stderr)
			if got != tt.want {
				t.Errorf("parseFPS(%q) = %v, want %v", tt.stderr, got, tt.want)
			}
		})
	}
}

func TestErrNoFFmpeg(t *testing.T) {
	t.Parallel()
	if ErrNoFFmpeg == nil {
		t.Error("ErrNoFFmpeg should not be nil")
	}
	if ErrNoFFmpeg.Error() != "chunk: ffmpeg not found in PATH" {
		t.Errorf("ErrNoFFmpeg.Error() = %q, want %q", ErrNoFFmpeg.Error(), "chunk: ffmpeg not found in PATH")
	}
}