// Package clip extracts video segments from source files using ffmpeg.
package clip

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	// ErrNoFFmpeg indicates ffmpeg is not available.
	ErrNoFFmpeg = errors.New("clip: ffmpeg not found in PATH")
	// ErrNoResults indicates no results to trim.
	ErrNoResults = errors.New("clip: no results to trim")
)

// TrimRequest describes a segment to trim from a source video.
type TrimRequest struct {
	SourceFile string
	StartTime  float64
	EndTime    float64
}

// TrimClip extracts a segment from a source video file.
// Uses a three-stage ffmpeg fallback strategy for maximum compatibility:
//  1. Stream copy (fast, no quality loss)
//  2. Re-encode with mpeg4/aac
//  3. Output-seeking stream copy
func TrimClip(ctx context.Context, source string, start, end float64, output string, padding float64) error {
	if end <= start {
		return fmt.Errorf("end_time (%.1f) must be greater than start_time (%.1f)", end, start)
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return ErrNoFFmpeg
	}

	duration, err := probeDuration(ctx, source)
	if err != nil {
		return fmt.Errorf("probe duration: %w", err)
	}

	paddedStart := start - padding
	if paddedStart < 0 {
		paddedStart = 0
	}
	paddedEnd := end + padding
	if paddedEnd > duration {
		paddedEnd = duration
	}
	length := paddedEnd - paddedStart

	outDir := filepath.Dir(output)
	if outDir == "" {
		outDir = "."
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// Attempt 1: stream copy (fast)
	run(ctx, "ffmpeg", "-y",
		"-ss", fmt.Sprintf("%.3f", paddedStart),
		"-i", source,
		"-t", fmt.Sprintf("%.3f", length),
		"-c", "copy",
		output)

	if fileUsable(output) {
		return nil
	}

	// Attempt 2: re-encode
	run(ctx, "ffmpeg", "-y",
		"-i", source,
		"-ss", fmt.Sprintf("%.3f", paddedStart),
		"-t", fmt.Sprintf("%.3f", length),
		"-c:v", "mpeg4", "-q:v", "5",
		"-c:a", "aac", "-b:a", "128k",
		output)

	if fileUsable(output) {
		return nil
	}

	// Attempt 3: output-seeking copy
	run(ctx, "ffmpeg", "-y",
		"-i", source,
		"-ss", fmt.Sprintf("%.3f", paddedStart),
		"-t", fmt.Sprintf("%.3f", length),
		"-c", "copy",
		output)

	if fileUsable(output) {
		return nil
	}

	return fmt.Errorf("all trim attempts failed for %s", source)
}

// TrimTopRequests trims the top N requests and saves clips to outputDir.
func TrimTopRequests(ctx context.Context, requests []TrimRequest, outputDir string, count int) ([]string, error) {
	if len(requests) == 0 {
		return nil, ErrNoResults
	}
	if count < 1 {
		count = 1
	}
	if count > len(requests) {
		count = len(requests)
	}

	var paths []string
	for _, r := range requests[:count] {
		filename := safeFilename(r.SourceFile, r.StartTime, r.EndTime)
		outPath := filepath.Join(outputDir, filename)
		if err := TrimClip(ctx, r.SourceFile, r.StartTime, r.EndTime, outPath, 2.0); err != nil {
			return paths, fmt.Errorf("trim result: %w", err)
		}
		paths = append(paths, outPath)
	}
	return paths, nil
}

func probeDuration(ctx context.Context, path string) (float64, error) {
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

func fileUsable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 1024
}

var unsafeChars = regexp.MustCompile(`[^\w\-]`)

func safeFilename(source string, start, end float64) string {
	base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	base = unsafeChars.ReplaceAllString(base, "_")
	return fmt.Sprintf("match_%s_%s-%s.mp4", base, fmtTime(start), fmtTime(end))
}

func fmtTime(seconds float64) string {
	m, s := int(seconds)/60, int(seconds)%60
	return fmt.Sprintf("%02dm%02ds", m, s)
}
