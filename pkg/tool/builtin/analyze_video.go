package toolbuiltin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/media/chunk"
	"github.com/cinience/saker/pkg/media/describe"
	"github.com/cinience/saker/pkg/media/embedding"
	"github.com/cinience/saker/pkg/media/vecstore"

	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/tool"
)

// TranscribeFunc transcribes an audio file and returns the text.
// Mirrors pipeline.TranscribeFunc to avoid an import cycle.
type TranscribeFunc func(ctx context.Context, audioPath string) (string, error)

const analyzeVideoDescription = `Performs comprehensive deep analysis of a video file using chunked multi-track VLM annotation with optional audio transcription and vector embedding.

Produces structured annotations (visual/audio/text/entity/scene/action/search_tags) per segment,
a detailed markdown report, a searchable JSONL index, and optionally indexes segments into a vector
store for semantic search via media_search.

For quick single-frame analysis, use frame_analyzer instead.
For sampling frames only, use video_sampler instead.
Requires ffmpeg to be installed on the system.`

var analyzeVideoSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"video_path": map[string]any{
			"type":        "string",
			"description": "Path to the input video file",
		},
		"task": map[string]any{
			"type":        "string",
			"description": "What to analyze or answer about the video (default: comprehensive video summary)",
		},
		"concurrency": map[string]any{
			"type":        "integer",
			"description": "Number of parallel workers for segment analysis (default: 4 for streams, 8 for local files)",
		},
		"enable_embedding": map[string]any{
			"type":        "boolean",
			"description": "Enable vector embedding for semantic search via media_search (default: false, requires embedding API key)",
		},
	},
	Required: []string{"video_path"},
}

// AnalyzeVideoTool orchestrates comprehensive video analysis with multi-track annotation.
type AnalyzeVideoTool struct {
	Model      model.Model
	Transcribe TranscribeFunc // optional; nil = skip audio transcription
	StoreDir   string         // base directory for per-session JSONL and vector storage; empty = auto
}

// NewAnalyzeVideoTool creates an analyze_video tool with the given model.
// Callers should inject a TranscribeFunc via t.Transcribe for audio transcription.
// When constructed through builtinToolFactories, resolveTranscribeFunc handles this.
func NewAnalyzeVideoTool(m model.Model) *AnalyzeVideoTool {
	return &AnalyzeVideoTool{Model: m}
}

func (t *AnalyzeVideoTool) Name() string             { return "analyze_video" }
func (t *AnalyzeVideoTool) Description() string      { return analyzeVideoDescription }
func (t *AnalyzeVideoTool) Schema() *tool.JSONSchema { return analyzeVideoSchema }

func (t *AnalyzeVideoTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if t.Model == nil {
		return nil, errors.New("analyze_video: model not configured")
	}

	videoPath, _ := params["video_path"].(string)
	if videoPath == "" {
		return nil, errors.New("video_path is required")
	}

	task, _ := params["task"].(string)
	concurrency := parseConcurrency(params, videoPath)
	enableEmbedding, _ := params["enable_embedding"].(bool)

	return t.executeDeep(ctx, videoPath, task, concurrency, enableEmbedding)
}

// parseConcurrency extracts the concurrency param or picks a default.
// Local files default to 8 (more aggressive); streams/URLs default to 4.
func parseConcurrency(params map[string]any, videoPath string) int {
	if c, ok := params["concurrency"]; ok {
		switch v := c.(type) {
		case float64:
			if int(v) > 0 {
				return int(v)
			}
		case int:
			if v > 0 {
				return v
			}
		}
	}
	// Local files can be read in parallel more aggressively.
	if strings.HasPrefix(videoPath, "rtsp://") || strings.HasPrefix(videoPath, "rtmp://") || strings.HasPrefix(videoPath, "http") {
		return 4
	}
	return 8
}

// executeDeep runs the full analysis pipeline:
// chunk → extract representative frames → multi-track VLM annotation → optional audio transcription
// → optional vector embedding → report + JSONL.
func (t *AnalyzeVideoTool) executeDeep(ctx context.Context, videoPath, task string, concurrency int, enableEmbedding bool) (*tool.ToolResult, error) {
	tmpDir, err := os.MkdirTemp("", "analyze-video-deep-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Step 1: Chunk video into 30s segments with reduced overlap (2s) to minimize redundant VLM calls.
	chunkDir := filepath.Join(tmpDir, "chunks")
	deepOpts := chunk.DefaultOptions()
	deepOpts.Overlap = 2
	segments, err := chunk.ChunkVideo(ctx, videoPath, chunkDir, deepOpts)
	if err != nil {
		return nil, fmt.Errorf("chunk video: %w", err)
	}
	if len(segments) == 0 {
		return nil, errors.New("analyze_video deep: no segments produced")
	}

	// Step 2: For each segment, extract 3 representative frames and run multi-track annotation.
	annotator := describe.NewAnnotator(t.Model)
	annotations := make([]*describe.Annotation, len(segments))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	for i, seg := range segments {
		i, seg := i, seg
		g.Go(func() error {
			// Skip still-frame segments to save VLM tokens.
			if still, _ := chunk.IsStillFrame(gctx, seg.ChunkPath); still {
				slog.Info("deep: skipping still-frame segment", "segment", i)
				return nil
			}
			framePaths, err := extractSegmentFrames(gctx, seg.ChunkPath, filepath.Join(tmpDir, fmt.Sprintf("seg_%04d", i)), 3, seg.Duration)
			if err != nil {
				slog.Warn("deep: frame extraction failed", "segment", i, "error", err)
				return nil
			}
			dSeg := describe.Segment{
				SourceFile: seg.SourceFile,
				StartTime:  seg.StartTime,
				EndTime:    seg.EndTime,
				Duration:   seg.Duration,
				ChunkPath:  seg.ChunkPath,
			}
			ann, err := annotator.AnnotateSegment(gctx, dSeg, framePaths)
			if err != nil {
				slog.Warn("deep: annotation failed", "segment", i, "error", err)
				return nil
			}
			annotations[i] = ann
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("deep annotation: %w", err)
	}

	// Step 3: Optional audio transcription.
	var transcripts []deepTranscript
	if t.Transcribe != nil {
		transcripts = t.transcribeAudio(ctx, videoPath, tmpDir)
	}

	// Step 4: Build structured report and persist.
	reportPath, summary := t.buildDeepReport(videoPath, task, segments, annotations, transcripts)

	// Step 5: Resolve session-aware store directory.
	storeDir := t.resolveStoreDir(ctx, videoPath)

	// Step 6: Persist annotations to JSONL store.
	jsonlPath := t.persistAnnotations(videoPath, annotations, storeDir)

	// Step 7: Optional vector embedding for semantic search.
	var embeddedCount int
	if enableEmbedding {
		embeddedCount = t.embedSegments(ctx, segments, annotations, storeDir)
	}

	// Count valid annotations.
	validAnns := 0
	for _, ann := range annotations {
		if ann != nil {
			validAnns++
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "=== Video Analysis: %s ===\n\n", videoPath)
	fmt.Fprintf(&sb, "Segments: %d, Annotated: %d\n", len(segments), validAnns)
	if len(transcripts) > 0 {
		fmt.Fprintf(&sb, "Audio segments transcribed: %d\n", len(transcripts))
	} else if t.Transcribe == nil {
		sb.WriteString("Audio transcription: not available (no ASR configured)\n")
	}
	if enableEmbedding {
		fmt.Fprintf(&sb, "Vector embeddings: %d segments indexed\n", embeddedCount)
	}
	sb.WriteString("\nNote: Analysis is based on sampled frames (3 per 30s segment). Fast actions between frames may be missed.\n")
	sb.WriteString("Conflicting details across segments (e.g., different years or names) indicate OCR/recognition uncertainty.\n\n")
	sb.WriteString(summary)

	if reportPath != "" {
		fmt.Fprintf(&sb, "\n\n[Analysis report saved to: %s]", reportPath)
	}
	if jsonlPath != "" {
		fmt.Fprintf(&sb, "\n[Annotations stored in: %s]", jsonlPath)
	}

	return &tool.ToolResult{
		Success: true,
		Output:  sb.String(),
		Artifacts: []artifact.ArtifactRef{
			{
				Source:     artifact.ArtifactSourceGenerated,
				ArtifactID: "video_deep_analysis",
				Kind:       artifact.ArtifactKindJSON,
			},
		},
		Structured: map[string]any{
			"video_path":       videoPath,
			"segment_count":    len(segments),
			"annotation_count": validAnns,
			"transcript_count": len(transcripts),
			"embedded_count":   embeddedCount,
			"report_path":      reportPath,
			"jsonl_path":       jsonlPath,
			"summary":          summary,
		},
	}, nil
}

// deepTranscript holds a single audio transcription result.
type deepTranscript struct {
	StartTime float64
	EndTime   float64
	Text      string
}

// extractSegmentFrames extracts N evenly-spaced JPEG frames from a video chunk.
// duration is the chunk length in seconds, used to compute the correct fps filter.
func extractSegmentFrames(ctx context.Context, chunkPath, outDir string, count int, duration float64) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	if duration <= 0 {
		duration = 30 // safe default
	}
	// fps=count/duration gives exactly count frames spread across the chunk.
	// e.g. 3 frames / 30s = 0.1 fps → one frame every 10s.
	fpsFilter := fmt.Sprintf("fps=%.4f", float64(count)/duration)
	args := []string{
		"-y", "-i", chunkPath,
		"-vf", fpsFilter,
		"-frames:v", fmt.Sprintf("%d", count),
		"-q:v", "2",
		filepath.Join(outDir, "frame_%03d.jpg"),
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if _, err := cmd.CombinedOutput(); err != nil {
		// Fallback: use select filter to pick evenly-spaced frames by timestamp.
		interval := duration / float64(count+1)
		var selects []string
		for i := 1; i <= count; i++ {
			selects = append(selects, fmt.Sprintf("lt(prev_pts*TB\\,%v)", float64(i)*interval))
		}
		selectFilter := fmt.Sprintf("select='%s',setpts=N/TB", strings.Join(selects, "+"))
		args2 := []string{
			"-y", "-i", chunkPath,
			"-vf", selectFilter, "-vsync", "vfr",
			"-frames:v", fmt.Sprintf("%d", count),
			"-q:v", "2",
			filepath.Join(outDir, "frame_%03d.jpg"),
		}
		cmd2 := exec.CommandContext(ctx, "ffmpeg", args2...)
		if _, err2 := cmd2.CombinedOutput(); err2 != nil {
			return nil, fmt.Errorf("extract frames: %w (fallback: %w)", err, err2)
		}
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jpg") {
			paths = append(paths, filepath.Join(outDir, e.Name()))
		}
	}
	return paths, nil
}

// extractAudioSegments splits the video audio track into 30s WAV segments for transcription.
func extractAudioSegments(ctx context.Context, videoPath, outDir string) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	args := []string{
		"-y", "-i", videoPath,
		"-vn", "-acodec", "pcm_s16le", "-ar", "16000", "-ac", "1",
		"-f", "segment", "-segment_time", "30",
		filepath.Join(outDir, "audio_%03d.wav"),
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("extract audio: %w\n%s", err, string(out))
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".wav") {
			paths = append(paths, filepath.Join(outDir, e.Name()))
		}
	}
	return paths, nil
}

// transcribeAudio extracts audio and transcribes each segment.
func (t *AnalyzeVideoTool) transcribeAudio(ctx context.Context, videoPath, tmpDir string) []deepTranscript {
	audioDir := filepath.Join(tmpDir, "audio")
	wavPaths, err := extractAudioSegments(ctx, videoPath, audioDir)
	if err != nil {
		slog.Warn("deep: audio extraction failed", "error", err)
		return nil
	}

	// Compute cumulative timestamps from actual WAV file sizes.
	// PCM format: 16000 Hz * 16-bit * 1 channel + 44-byte header.
	const pcmBytesPerSec = 16000 * 2 * 1 // 32000 bytes/sec
	const wavHeaderSize = 44

	var transcripts []deepTranscript
	var offset float64
	for i, wavPath := range wavPaths {
		if ctx.Err() != nil {
			break
		}
		// Derive actual segment duration from WAV file size.
		segDur := 30.0 // fallback
		if info, err := os.Stat(wavPath); err == nil && info.Size() > wavHeaderSize {
			segDur = float64(info.Size()-wavHeaderSize) / float64(pcmBytesPerSec)
		}

		text, err := t.Transcribe(ctx, wavPath)
		if err != nil {
			slog.Warn("deep: transcription failed", "segment", i, "error", err)
			offset += segDur
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			offset += segDur
			continue
		}
		transcripts = append(transcripts, deepTranscript{
			StartTime: offset,
			EndTime:   offset + segDur,
			Text:      text,
		})
		offset += segDur
	}
	return transcripts
}

// buildDeepReport generates a structured markdown report and saves it alongside the video.
func (t *AnalyzeVideoTool) buildDeepReport(videoPath, task string, segments []chunk.Segment, annotations []*describe.Annotation, transcripts []deepTranscript) (string, string) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Video Deep Analysis: %s\n\n", filepath.Base(videoPath)))
	sb.WriteString(fmt.Sprintf("- **Date**: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("- **Source**: %s\n", videoPath))
	sb.WriteString(fmt.Sprintf("- **Segments**: %d\n", len(segments)))
	if len(segments) > 0 {
		totalDur := segments[len(segments)-1].EndTime
		minutes := int(totalDur) / 60
		secs := int(totalDur) % 60
		sb.WriteString(fmt.Sprintf("- **Duration**: %d:%02d\n", minutes, secs))
	}
	if len(transcripts) > 0 {
		sb.WriteString("- **Audio transcription**: available\n")
	} else {
		sb.WriteString("- **Audio transcription**: unavailable\n")
	}
	if task != "" {
		sb.WriteString(fmt.Sprintf("- **Task**: %s\n", task))
	}

	sb.WriteString("\n## Timeline\n\n")
	for i, seg := range segments {
		startMin, startSec := int(seg.StartTime)/60, int(seg.StartTime)%60
		endMin, endSec := int(seg.EndTime)/60, int(seg.EndTime)%60
		fmt.Fprintf(&sb, "### [%02d:%02d - %02d:%02d] Segment %d\n\n", startMin, startSec, endMin, endSec, i+1)

		if i < len(annotations) && annotations[i] != nil {
			ann := annotations[i]
			// Fallback: if Visual looks like a raw JSON blob (all other fields empty),
			// try to re-parse it into the proper fields.
			if ann.Visual != "" && ann.Action == "" && ann.Entity == "" && ann.Scene == "" && strings.HasPrefix(strings.TrimSpace(ann.Visual), "{") {
				var parsed describe.Annotation
				if err := json.Unmarshal([]byte(ann.Visual), &parsed); err == nil {
					parsed.Segment = ann.Segment
					ann = &parsed
					annotations[i] = ann
				}
			}
			if ann.Visual != "" {
				fmt.Fprintf(&sb, "**Visual**: %s\n\n", ann.Visual)
			}
			if ann.Action != "" {
				fmt.Fprintf(&sb, "**Action**: %s\n\n", ann.Action)
			}
			if ann.Entity != "" {
				fmt.Fprintf(&sb, "**Entities**: %s\n\n", ann.Entity)
			}
			if ann.Scene != "" {
				fmt.Fprintf(&sb, "**Scene**: %s\n\n", ann.Scene)
			}
			if ann.Text != "" {
				fmt.Fprintf(&sb, "**Text**: %s\n\n", ann.Text)
			}
			if ann.Audio != "" {
				fmt.Fprintf(&sb, "**Audio (inferred)**: %s\n\n", ann.Audio)
			}
			if len(ann.SearchTags) > 0 {
				fmt.Fprintf(&sb, "**Tags**: %s\n\n", strings.Join(ann.SearchTags, ", "))
			}
		} else {
			sb.WriteString("*Annotation unavailable*\n\n")
		}

		// Attach any audio transcripts that overlap this segment.
		for _, tr := range transcripts {
			if tr.StartTime < seg.EndTime && tr.EndTime > seg.StartTime {
				trMin, trSec := int(tr.StartTime)/60, int(tr.StartTime)%60
				fmt.Fprintf(&sb, "**Audio [%02d:%02d]**: %s\n\n", trMin, trSec, tr.Text)
			}
		}
	}

	// Full transcript section.
	if len(transcripts) > 0 {
		sb.WriteString("## Full Audio Transcript\n\n")
		for _, tr := range transcripts {
			trMin, trSec := int(tr.StartTime)/60, int(tr.StartTime)%60
			fmt.Fprintf(&sb, "[%02d:%02d] %s\n\n", trMin, trSec, tr.Text)
		}
	}

	// Collect all search tags.
	var allTags []string
	seen := map[string]bool{}
	for _, ann := range annotations {
		if ann == nil {
			continue
		}
		for _, tag := range ann.SearchTags {
			lower := strings.ToLower(tag)
			if !seen[lower] {
				seen[lower] = true
				allTags = append(allTags, tag)
			}
		}
	}
	if len(allTags) > 0 {
		sb.WriteString("## Search Tags\n\n")
		sb.WriteString(strings.Join(allTags, ", "))
		sb.WriteString("\n")
	}

	// Consistency check: detect conflicting OCR text across segments.
	if notes := detectConsistencyIssues(annotations); notes != "" {
		sb.WriteString("\n## Consistency Notes\n\n")
		sb.WriteString(notes)
	}

	content := sb.String()

	// Save report alongside video.
	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	ts := time.Now().Format("20060102-150405")
	reportPath := filepath.Join(dir, fmt.Sprintf("%s_deep_analysis_%s.md", base, ts))

	if err := os.WriteFile(reportPath, []byte(content), 0o644); err != nil {
		slog.Warn("deep: failed to save report", "error", err)
		return "", content
	}
	return reportPath, content
}

// resolveStoreDir computes the session-aware base directory for JSONL and vector storage.
// Layout: {StoreDir}/{sessionID}/ when session ID is available, otherwise {StoreDir}/.
func (t *AnalyzeVideoTool) resolveStoreDir(ctx context.Context, videoPath string) string {
	base := t.StoreDir
	if base == "" {
		base = filepath.Join(filepath.Dir(videoPath), ".saker", "media")
	}
	if sid := bashSessionID(ctx); sid != "" {
		return filepath.Join(base, sanitizePathComponent(sid))
	}
	return base
}

// persistAnnotations saves annotations to a per-video JSONL store file.
// Each video gets its own file: {storeDir}/{videoBaseName}.jsonl
func (t *AnalyzeVideoTool) persistAnnotations(videoPath string, annotations []*describe.Annotation, storeDir string) string {
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		slog.Warn("deep: failed to create store dir", "error", err)
		return ""
	}
	videoBase := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	storePath := filepath.Join(storeDir, videoBase+".jsonl")

	store := describe.NewStore(storePath)
	persisted := 0
	for _, ann := range annotations {
		if ann == nil {
			continue
		}
		if err := store.Append(ann); err != nil {
			slog.Warn("deep: failed to append annotation", "error", err)
			continue
		}
		persisted++
	}
	if persisted == 0 {
		return ""
	}
	return storePath
}

// embedSegments creates vector embeddings for each chunk and stores them for semantic search.
// Returns the number of successfully embedded segments.
func (t *AnalyzeVideoTool) embedSegments(ctx context.Context, segments []chunk.Segment, annotations []*describe.Annotation, storeDir string) int {
	emb, err := embedding.NewEmbedder(embedding.Config{})
	if err != nil {
		slog.Warn("deep: embedding not available", "error", err)
		return 0
	}

	embedDir := filepath.Join(storeDir, "vectors")

	vs, err := vecstore.NewChromemStore(vecstore.ChromemOptions{
		PersistDir:     embedDir,
		CollectionName: "media_chunks",
		Compress:       true,
	})
	if err != nil {
		slog.Warn("deep: vector store creation failed", "error", err)
		return 0
	}

	var embedded int
	for i, seg := range segments {
		if ctx.Err() != nil {
			break
		}
		if seg.ChunkPath == "" {
			continue
		}

		vec, err := emb.EmbedVideo(ctx, seg.ChunkPath)
		if err != nil {
			slog.Warn("deep: embedding failed", "segment", i, "error", err)
			continue
		}

		metadata := map[string]string{
			"source_file": seg.SourceFile,
			"start_time":  fmt.Sprintf("%.3f", seg.StartTime),
			"end_time":    fmt.Sprintf("%.3f", seg.EndTime),
		}
		// Enrich metadata with annotation text for hybrid search.
		if i < len(annotations) && annotations[i] != nil {
			ann := annotations[i]
			if ann.Visual != "" {
				metadata["visual"] = ann.Visual
			}
			if ann.Action != "" {
				metadata["action"] = ann.Action
			}
			if ann.Scene != "" {
				metadata["scene"] = ann.Scene
			}
			if len(ann.SearchTags) > 0 {
				metadata["tags"] = strings.Join(ann.SearchTags, ",")
			}
		}

		raw := fmt.Sprintf("%s:%.3f-%.3f", seg.SourceFile, seg.StartTime, seg.EndTime)
		h := sha256.Sum256([]byte(raw))
		id := fmt.Sprintf("%x", h[:16])

		if err := vs.Add(ctx, id, vec, metadata); err != nil {
			slog.Warn("deep: vector store add failed", "segment", i, "error", err)
			continue
		}
		embedded++
	}
	return embedded
}

// yearPattern matches 4-digit years (1900-2099) for consistency checking.
var yearPattern = regexp.MustCompile(`\b(19|20)\d{2}\b`)

// detectConsistencyIssues scans annotations for conflicting OCR text across segments.
// Returns a markdown string with warnings, or empty if no issues found.
func detectConsistencyIssues(annotations []*describe.Annotation) string {
	// Track which years appear and in which segments.
	yearSegments := map[string][]int{}
	for i, ann := range annotations {
		if ann == nil || ann.Text == "" {
			continue
		}
		years := yearPattern.FindAllString(ann.Text, -1)
		seen := map[string]bool{}
		for _, y := range years {
			if !seen[y] {
				seen[y] = true
				yearSegments[y] = append(yearSegments[y], i+1)
			}
		}
	}

	// If multiple distinct years found, flag as inconsistency.
	var warnings []string
	if len(yearSegments) > 1 {
		var parts []string
		for year, segs := range yearSegments {
			segStrs := make([]string, len(segs))
			for i, s := range segs {
				segStrs[i] = fmt.Sprintf("%d", s)
			}
			parts = append(parts, fmt.Sprintf("'%s' in segment(s) %s", year, strings.Join(segStrs, ", ")))
		}
		warnings = append(warnings, fmt.Sprintf("- Year inconsistency detected: %s. This may indicate OCR recognition variance across frames.", strings.Join(parts, "; ")))
	}

	if len(warnings) == 0 {
		return ""
	}
	return strings.Join(warnings, "\n") + "\n"
}
