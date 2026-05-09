package media

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
)

func TestMakeChunkID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		seg MediaSegment
	}{
		{seg: MediaSegment{SourceFile: "video.mp4", StartTime: 10.5, EndTime: 40.5}},
		{seg: MediaSegment{SourceFile: "", StartTime: 0, EndTime: 30}},
		{seg: MediaSegment{SourceFile: "long/path/to/video.mkv", StartTime: 123.456, EndTime: 153.456}},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%.3f", tt.seg.SourceFile, tt.seg.StartTime), func(t *testing.T) {
			t.Parallel()
			id := makeChunkID(tt.seg)

			// Verify ID is deterministic.
			id2 := makeChunkID(tt.seg)
			if id != id2 {
				t.Errorf("makeChunkID not deterministic: first=%q, second=%q", id, id2)
			}

			// Verify ID length (sha256[:16] hex = 32 chars).
			if len(id) != 32 {
				t.Errorf("makeChunkID length = %d, want 32", len(id))
			}

			// Verify different segments produce different IDs.
			diffSeg := MediaSegment{SourceFile: "other.mp4", StartTime: 0, EndTime: 10}
			diffID := makeChunkID(diffSeg)
			if id == diffID {
				t.Error("makeChunkID produced same ID for different segments")
			}
		})
	}
}

func TestMakeChunkID_Format(t *testing.T) {
	t.Parallel()
	seg := MediaSegment{SourceFile: "test.mp4", StartTime: 0, EndTime: 30}
	raw := fmt.Sprintf("%s:%.3f-%.3f", seg.SourceFile, seg.StartTime, seg.EndTime)
	h := sha256.Sum256([]byte(raw))
	expectedHash := fmt.Sprintf("%x", h[:16])

	got := makeChunkID(seg)
	if got != expectedHash {
		t.Errorf("makeChunkID = %q, want %q (sha256 of %q [:16])", got, expectedHash, raw)
	}
}

func TestHashPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
	}{
		{path: "video.mp4"},
		{path: "/home/user/videos/long_video_name.mkv"},
		{path: ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			got := hashPath(tt.path)

			// Verify deterministic.
			got2 := hashPath(tt.path)
			if got != got2 {
				t.Errorf("hashPath not deterministic: first=%q, second=%q", got, got2)
			}

			// Verify length (sha256[:8] hex = 16 chars).
			if len(got) != 16 {
				t.Errorf("hashPath length = %d, want 16", len(got))
			}
		})
	}

	// Verify different paths produce different hashes.
	if hashPath("a.mp4") == hashPath("b.mp4") {
		t.Error("hashPath produced same hash for different paths")
	}
}

func TestWorkDir(t *testing.T) {
	t.Parallel()
	idx := &Indexer{WorkDir: "/custom/work"}
	got := idx.workDir()
	if got != "/custom/work" {
		t.Errorf("workDir with WorkDir set = %q, want %q", got, "/custom/work")
	}

	idx2 := &Indexer{WorkDir: ""}
	got2 := idx2.workDir()
	expected := fmt.Sprintf("%s/saker-media", os.TempDir())
	if got2 != expected {
		t.Errorf("workDir with empty WorkDir = %q, want %q", got2, expected)
	}
}