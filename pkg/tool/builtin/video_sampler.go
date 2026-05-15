package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/tool"
)

const videoSamplerDescription = `Extracts frames from a video file using ffmpeg.

Supports three sampling strategies:
- "uniform": extract N evenly-spaced frames (default: 8)
- "keyframe": extract only keyframes (I-frames) via ffprobe scene detection
- "interval": extract one frame every N seconds

Requires ffmpeg (and ffprobe for keyframe mode) to be installed on the system.
Returns image artifacts for each extracted frame.`

var videoSamplerSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"video_path": map[string]any{
			"type":        "string",
			"description": "Path to the input video file",
		},
		"strategy": map[string]any{
			"type":        "string",
			"enum":        []string{"uniform", "keyframe", "interval"},
			"description": "Frame sampling strategy (default: uniform)",
		},
		"count": map[string]any{
			"type":        "integer",
			"description": "Number of frames for uniform, or interval seconds for interval strategy (default: 8)",
		},
		"max_dimension": map[string]any{
			"type":        "integer",
			"description": "Maximum dimension (width or height) in pixels; frames are scaled down if larger",
		},
	},
	Required: []string{"video_path"},
}

// VideoSamplerTool extracts frames from video files using ffmpeg.
type VideoSamplerTool struct{}

func NewVideoSamplerTool() *VideoSamplerTool { return &VideoSamplerTool{} }

func (v *VideoSamplerTool) Name() string             { return "video_sampler" }
func (v *VideoSamplerTool) Description() string      { return videoSamplerDescription }
func (v *VideoSamplerTool) Schema() *tool.JSONSchema { return videoSamplerSchema }

func (v *VideoSamplerTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}

	videoPath, _ := params["video_path"].(string)
	if videoPath == "" {
		return nil, errors.New("video_path is required")
	}
	if _, err := os.Stat(videoPath); err != nil {
		return nil, fmt.Errorf("video file not found: %w", err)
	}

	strategy := "uniform"
	if s, ok := params["strategy"].(string); ok && s != "" {
		strategy = s
	}

	count := 8
	if c, ok := params["count"].(float64); ok && c > 0 {
		count = int(c)
	} else if c, ok := params["count"].(int); ok && c > 0 {
		count = c
	}

	maxDim := 0
	if d, ok := params["max_dimension"].(float64); ok && d > 0 {
		maxDim = int(d)
	} else if d, ok := params["max_dimension"].(int); ok && d > 0 {
		maxDim = d
	}

	// Create temp directory for extracted frames
	tmpDir, err := os.MkdirTemp("", "saker-frames-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	var frames []string
	switch strategy {
	case "uniform":
		frames, err = extractUniform(ctx, videoPath, tmpDir, count, maxDim)
	case "keyframe":
		frames, err = extractKeyframes(ctx, videoPath, tmpDir, maxDim)
	case "interval":
		frames, err = extractInterval(ctx, videoPath, tmpDir, count, maxDim)
	default:
		return nil, fmt.Errorf("unsupported strategy: %s", strategy)
	}
	if err != nil {
		return nil, err
	}

	artifacts := make([]artifact.ArtifactRef, len(frames))
	for i, path := range frames {
		artifacts[i] = artifact.ArtifactRef{
			Source:     artifact.ArtifactSourceGenerated,
			Path:       path,
			ArtifactID: fmt.Sprintf("frame_%03d", i),
			Kind:       artifact.ArtifactKindImage,
		}
	}

	// Build output with explicit frame paths so the model knows where to find them
	var outputLines []string
	outputLines = append(outputLines, fmt.Sprintf("Extracted %d frames from %s:", len(frames), filepath.Base(videoPath)))
	for _, path := range frames {
		outputLines = append(outputLines, fmt.Sprintf("  - %s", path))
	}
	outputLines = append(outputLines, "")
	outputLines = append(outputLines, "Use frame_analyzer with frame_path parameter to analyze individual frames.")

	return &tool.ToolResult{
		Success:   true,
		Output:    strings.Join(outputLines, "\n"),
		Artifacts: artifacts,
		Structured: map[string]any{
			"frame_count": len(frames),
			"strategy":    strategy,
			"video_path":  videoPath,
			"temp_dir":    tmpDir,
		},
		// Cleanup MUST be called by the caller to remove the temporary frame directory.
		Cleanup: func() { os.RemoveAll(tmpDir) },
	}, nil
}

func videoDuration(ctx context.Context, videoPath string) (float64, error) {
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath,
	).Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe duration: %w", err)
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}

func extractUniform(ctx context.Context, videoPath, tmpDir string, count, maxDim int) ([]string, error) {
	duration, err := videoDuration(ctx, videoPath)
	if err != nil {
		return nil, err
	}
	if duration <= 0 {
		return nil, errors.New("video has zero duration")
	}

	outPattern := filepath.Join(tmpDir, "frame_%03d.jpg")

	// Build video filter: select frames at uniform intervals.
	interval := duration / float64(count)
	// Use select filter to pick one frame per interval.
	vf := fmt.Sprintf("select='lt(mod(t\\,%f)\\,%f)',setpts=N/FRAME_RATE/TB",
		interval, interval*0.5)
	if maxDim > 0 {
		vf += fmt.Sprintf(",scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease",
			maxDim, maxDim)
	}

	args := []string{
		"-i", videoPath,
		"-vf", vf,
		"-frames:v", strconv.Itoa(count),
		"-q:v", "2",
		"-vsync", "vfr",
		"-y", outPattern,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg uniform extract: %w\n%s", err, string(out))
	}

	// Collect output frames.
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("read frames dir: %w", err)
	}

	var frames []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jpg") {
			path := filepath.Join(tmpDir, e.Name())
			if info, err := os.Stat(path); err == nil && info.Size() > 0 {
				frames = append(frames, path)
			}
		}
	}
	return frames, nil
}

func extractKeyframes(ctx context.Context, videoPath, tmpDir string, maxDim int) ([]string, error) {
	// Use ffprobe to find keyframe timestamps
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "frame=pts_time,key_frame",
		"-of", "json",
		"-skip_frame", "nokey",
		videoPath,
	).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe keyframes: %w", err)
	}

	var probeResult struct {
		Frames []struct {
			PtsTime  string `json:"pts_time"`
			KeyFrame int    `json:"key_frame"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(out, &probeResult); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}

	var timestamps []float64
	for _, f := range probeResult.Frames {
		if f.KeyFrame == 1 {
			ts, err := strconv.ParseFloat(f.PtsTime, 64)
			if err == nil {
				timestamps = append(timestamps, ts)
			}
		}
	}

	if len(timestamps) == 0 {
		return nil, fmt.Errorf("no keyframes found in video")
	}

	frames := make([]string, 0, len(timestamps))
	for i, ts := range timestamps {
		outPath := filepath.Join(tmpDir, fmt.Sprintf("frame_%03d.jpg", i))

		args := []string{
			"-ss", fmt.Sprintf("%.3f", ts),
			"-i", videoPath,
			"-frames:v", "1",
			"-q:v", "2",
		}
		if maxDim > 0 {
			args = append(args, "-vf", fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", maxDim, maxDim))
		}
		args = append(args, "-y", outPath)

		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("ffmpeg extract keyframe %d: %w\n%s", i, err, string(out))
		}

		if info, err := os.Stat(outPath); err == nil && info.Size() > 0 {
			frames = append(frames, outPath)
		}
	}

	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames extracted from video")
	}

	return frames, nil
}

func extractInterval(ctx context.Context, videoPath, tmpDir string, intervalSecs, maxDim int) ([]string, error) {
	if intervalSecs <= 0 {
		intervalSecs = 1
	}
	args := []string{
		"-i", videoPath,
		"-vf", fmt.Sprintf("fps=1/%d", intervalSecs),
		"-q:v", "2",
	}
	if maxDim > 0 {
		args = []string{
			"-i", videoPath,
			"-vf", fmt.Sprintf("fps=1/%d,scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", intervalSecs, maxDim, maxDim),
			"-q:v", "2",
		}
	}
	outPattern := filepath.Join(tmpDir, "frame_%03d.jpg")
	args = append(args, "-y", outPattern)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg interval extract: %w\n%s", err, string(out))
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("read frames dir: %w", err)
	}

	var frames []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jpg") {
			frames = append(frames, filepath.Join(tmpDir, e.Name()))
		}
	}
	return frames, nil
}
