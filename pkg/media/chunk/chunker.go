// Package chunk splits video files into overlapping segments for embedding.
package chunk

import (
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/corona10/goimagehash"
)

// ErrNoFFmpeg indicates ffmpeg/ffprobe is not installed.
var ErrNoFFmpeg = errors.New("chunk: ffmpeg not found in PATH")

// Segment represents a chunk of a source video file.
type Segment struct {
	SourceFile string  `json:"source_file"`
	StartTime  float64 `json:"start_time"`
	EndTime    float64 `json:"end_time"`
	Duration   float64 `json:"duration"`
	ChunkPath  string  `json:"chunk_path,omitempty"`
}

// SupportedExtensions lists video file extensions that can be indexed.
var SupportedExtensions = map[string]bool{
	".mp4":  true,
	".mov":  true,
	".avi":  true,
	".mkv":  true,
	".webm": true,
}

// Options controls video chunking behavior.
type Options struct {
	// Duration of each chunk in seconds (default 30).
	Duration float64
	// Overlap between consecutive chunks in seconds (default 5).
	Overlap float64
	// MaxWidth for preprocessing — chunks are scaled down to this width (default 480).
	MaxWidth int
	// FPS for preprocessing — chunks are re-sampled to this frame rate (default 5).
	FPS int
	// SkipStillFrames removes chunks that appear to be still images (default true).
	SkipStillFrames bool
}

// DefaultOptions returns sensible chunking defaults.
func DefaultOptions() Options {
	return Options{
		Duration:        30,
		Overlap:         5,
		MaxWidth:        480,
		FPS:             5,
		SkipStillFrames: true,
	}
}

// ChunkVideo splits a video file into overlapping segments.
// Each segment is written as a separate .mp4 file under outputDir.
func ChunkVideo(ctx context.Context, sourcePath, outputDir string, opts Options) ([]Segment, error) {
	if err := requireFFmpeg(); err != nil {
		return nil, err
	}

	duration, err := videoDuration(ctx, sourcePath)
	if err != nil {
		return nil, fmt.Errorf("get video duration: %w", err)
	}
	if duration <= 0 {
		return nil, errors.New("chunk: video has zero duration")
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	step := opts.Duration - opts.Overlap
	if step <= 0 {
		step = opts.Duration
	}

	var segments []Segment
	for i := 0; ; i++ {
		start := float64(i) * step
		if start >= duration {
			break
		}
		end := start + opts.Duration
		if end > duration {
			end = duration
		}
		length := end - start
		if length < 1.0 {
			break // skip very short tail chunks
		}

		chunkName := fmt.Sprintf("chunk_%04d.mp4", i)
		chunkPath := filepath.Join(outputDir, chunkName)

		if err := extractChunk(ctx, sourcePath, chunkPath, start, length); err != nil {
			return nil, fmt.Errorf("extract chunk %d: %w", i, err)
		}

		segments = append(segments, Segment{
			SourceFile: sourcePath,
			StartTime:  start,
			EndTime:    end,
			Duration:   length,
			ChunkPath:  chunkPath,
		})
	}

	return segments, nil
}

// PreprocessChunk re-encodes a chunk at lower resolution and frame rate
// to reduce size before embedding. Returns path to the preprocessed file.
func PreprocessChunk(ctx context.Context, chunkPath string, opts Options) (string, error) {
	if err := requireFFmpeg(); err != nil {
		return "", err
	}

	outPath := chunkPath + ".prep.mp4"
	width := opts.MaxWidth
	if width <= 0 {
		width = 480
	}
	fps := opts.FPS
	if fps <= 0 {
		fps = 5
	}

	args := []string{
		"-y",
		"-i", chunkPath,
		"-vf", fmt.Sprintf("scale=%d:-2,fps=%d", width, fps),
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-crf", "28",
		"-an", // drop audio
		outPath,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Fallback: try mpeg4 codec if libx264 is unavailable
		fallbackArgs := []string{
			"-y",
			"-i", chunkPath,
			"-vf", fmt.Sprintf("scale=%d:-2,fps=%d", width, fps),
			"-c:v", "mpeg4", "-q:v", "8",
			"-an",
			outPath,
		}
		cmd2 := exec.CommandContext(ctx, "ffmpeg", fallbackArgs...)
		if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
			return "", fmt.Errorf("preprocess chunk: %w\n%s\n%s", err, string(out), string(out2))
		}
	}

	return outPath, nil
}

// stillFrameHashThreshold is the maximum Hamming distance between perceptual
// hashes of two frames to consider them identical. dHash produces a 64-bit
// hash; distance ≤ 4 means the frames are visually near-identical.
const stillFrameHashThreshold = 4

// IsStillFrame detects whether a video chunk is effectively a still image.
// It extracts 3 evenly-spaced frames, computes a perceptual difference hash
// (dHash) for each via goimagehash, and checks that all pairwise Hamming
// distances are within the threshold. On any error, returns false (not still).
func IsStillFrame(ctx context.Context, chunkPath string) (bool, error) {
	tmpDir, err := os.MkdirTemp("", "still-check-*")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmpDir)

	// Get total frame count.
	totalFrames, err := countFrames(ctx, chunkPath)
	if err != nil || totalFrames < 3 {
		return false, nil
	}

	// Pick 3 evenly-spaced frame indices: 0, 1/3, 2/3.
	f1 := totalFrames / 3
	f2 := 2 * totalFrames / 3
	outPattern := filepath.Join(tmpDir, "frame_%03d.jpg")

	cmd := exec.CommandContext(ctx, "ffmpeg", "-y",
		"-i", chunkPath,
		"-vf", fmt.Sprintf(`select=eq(n\,0)+eq(n\,%d)+eq(n\,%d)`, f1, f2),
		"-vsync", "vfr",
		"-q:v", "2",
		outPattern,
	)
	if _, err := cmd.CombinedOutput(); err != nil {
		return false, nil
	}

	// Collect extracted frame paths.
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return false, nil
	}
	var framePaths []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".jpg" {
			framePaths = append(framePaths, filepath.Join(tmpDir, e.Name()))
		}
	}
	sort.Strings(framePaths)
	if len(framePaths) < 2 {
		return false, nil
	}

	// Compute dHash for each frame.
	hashes := make([]*goimagehash.ImageHash, len(framePaths))
	for i, fp := range framePaths {
		h, err := dhashFile(fp)
		if err != nil {
			return false, nil
		}
		hashes[i] = h
	}

	// All pairwise distances must be within threshold.
	for i := 0; i < len(hashes); i++ {
		for j := i + 1; j < len(hashes); j++ {
			dist, err := hashes[i].Distance(hashes[j])
			if err != nil {
				return false, nil
			}
			if dist > stillFrameHashThreshold {
				return false, nil
			}
		}
	}
	return true, nil
}

// dhashFile computes a difference hash (dHash) for a JPEG file.
func dhashFile(path string) (*goimagehash.ImageHash, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, err := jpeg.Decode(f)
	if err != nil {
		return nil, err
	}
	return goimagehash.DifferenceHash(img)
}

// countFrames returns the total number of video frames by running ffmpeg null mux.
// Falls back to duration * fps estimation if the frame= line is not found.
func countFrames(ctx context.Context, videoPath string) (int, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", videoPath, "-map", "0:v:0",
		"-c", "copy", "-f", "null", "-",
	)
	out, _ := cmd.CombinedOutput()
	stderr := string(out)

	// Try parsing "frame= N" from ffmpeg progress output.
	if idx := strings.LastIndex(stderr, "frame="); idx >= 0 {
		s := strings.TrimSpace(stderr[idx+6:])
		if end := strings.IndexAny(s, " \t\n"); end > 0 {
			s = s[:end]
		}
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n, nil
		}
	}

	// Fallback: estimate from duration and fps.
	dur, err := videoDuration(ctx, videoPath)
	if err != nil {
		return 0, err
	}
	fps := parseFPS(stderr)
	if fps <= 0 {
		fps = 25 // sensible default
	}
	return int(dur * fps), nil
}

// parseFPS extracts fps from ffmpeg stderr output (e.g. "29.97 fps").
func parseFPS(stderr string) float64 {
	idx := strings.Index(stderr, " fps")
	if idx < 0 {
		return 0
	}
	// Walk backwards to find the number start.
	s := stderr[:idx]
	start := strings.LastIndexAny(s, " ,\t\n") + 1
	if start >= len(s) {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s[start:]), 64)
	if err != nil {
		return 0
	}
	return f
}

// ScanDirectory finds all supported video files in a directory tree.
func ScanDirectory(dir string, extensions map[string]bool) ([]string, error) {
	if extensions == nil {
		extensions = SupportedExtensions
	}

	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible files
		}
		if info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if extensions[ext] {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// extractChunk uses ffmpeg to extract a segment from a video file.
func extractChunk(ctx context.Context, source, output string, start, length float64) error {
	args := []string{
		"-y",
		"-ss", fmt.Sprintf("%.3f", start),
		"-i", source,
		"-t", fmt.Sprintf("%.3f", length),
		"-c", "copy",
		"-avoid_negative_ts", "make_zero",
		output,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w\n%s", err, string(out))
	}

	info, err := os.Stat(output)
	if err != nil || info.Size() < 1024 {
		return fmt.Errorf("chunk output too small or missing")
	}
	return nil
}

// videoDuration returns the duration of a video file in seconds.
// Prefers ffprobe; falls back to parsing "Duration:" from ffmpeg -i stderr.
func videoDuration(ctx context.Context, videoPath string) (float64, error) {
	if _, err := exec.LookPath("ffprobe"); err == nil {
		out, err := exec.CommandContext(ctx, "ffprobe",
			"-v", "error",
			"-show_entries", "format=duration",
			"-of", "default=noprint_wrappers=1:nokey=1",
			videoPath,
		).Output()
		if err == nil {
			if d, parseErr := strconv.ParseFloat(strings.TrimSpace(string(out)), 64); parseErr == nil {
				return d, nil
			}
		}
	}

	// Fallback: parse duration from ffmpeg -i stderr.
	cmd := exec.CommandContext(ctx, "ffmpeg", "-i", videoPath, "-f", "null", "-")
	out, _ := cmd.CombinedOutput()
	return parseDurationFromStderr(string(out))
}

// parseDurationFromStderr extracts "Duration: HH:MM:SS.ss" from ffmpeg stderr.
func parseDurationFromStderr(stderr string) (float64, error) {
	idx := strings.Index(stderr, "Duration:")
	if idx < 0 {
		return 0, fmt.Errorf("cannot determine video duration")
	}
	s := stderr[idx+9:]
	if comma := strings.Index(s, ","); comma > 0 {
		s = s[:comma]
	}
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("cannot parse duration: %q", s)
	}
	h, err1 := strconv.ParseFloat(parts[0], 64)
	m, err2 := strconv.ParseFloat(parts[1], 64)
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, fmt.Errorf("cannot parse duration: %q", s)
	}
	return h*3600 + m*60 + sec, nil
}

// requireFFmpeg checks that ffmpeg is available in PATH.
func requireFFmpeg() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return ErrNoFFmpeg
	}
	return nil
}
