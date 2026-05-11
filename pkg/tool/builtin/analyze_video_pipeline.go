package toolbuiltin

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/media/chunk"
	"github.com/cinience/saker/pkg/media/describe"
	"github.com/cinience/saker/pkg/media/embedding"
	"github.com/cinience/saker/pkg/media/vecstore"
	"github.com/cinience/saker/pkg/tool"
)

// analyze_video_pipeline.go contains the per-segment analysis pipeline:
// chunking + frame extraction + multi-track VLM annotation + optional audio
// transcription + optional vector embedding. Report/format helpers live in
// analyze_video_format.go.

// deepTranscript holds a single audio transcription result.
type deepTranscript struct {
	StartTime float64
	EndTime   float64
	Text      string
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
