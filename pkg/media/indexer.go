package media

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"github.com/saker-ai/saker/pkg/media/chunk"
	"github.com/saker-ai/saker/pkg/media/describe"
	"github.com/saker-ai/saker/pkg/media/embedding"
	"github.com/saker-ai/saker/pkg/media/vecstore"
)

// Indexer orchestrates the full media indexing pipeline:
// chunk → preprocess → embed + annotate → store.
type Indexer struct {
	Chunker     chunk.Options
	Embedder    embedding.Embedder
	VecStore    *vecstore.ChromemStore
	Annotator   *describe.Annotator // nil to skip VLM annotation
	DescStore   *describe.Store     // nil to skip VLM annotation
	WorkDir     string              // directory for intermediate files
	Concurrency int                 // max parallel operations (default 4)
	OnProgress  func(done, total int)
}

// IndexFile indexes a single video file through the full pipeline.
func (idx *Indexer) IndexFile(ctx context.Context, videoPath string) error {
	if _, err := os.Stat(videoPath); err != nil {
		return fmt.Errorf("video not found: %w", err)
	}

	chunkDir := filepath.Join(idx.workDir(), "chunks", hashPath(videoPath))
	chunkSegs, err := chunk.ChunkVideo(ctx, videoPath, chunkDir, idx.Chunker)
	if err != nil {
		return fmt.Errorf("chunk video: %w", err)
	}

	// Convert chunk.Segment → MediaSegment
	segments := make([]MediaSegment, len(chunkSegs))
	for i, cs := range chunkSegs {
		segments[i] = MediaSegment{
			SourceFile: cs.SourceFile,
			StartTime:  cs.StartTime,
			EndTime:    cs.EndTime,
			Duration:   cs.Duration,
			ChunkPath:  cs.ChunkPath,
		}
	}

	concurrency := idx.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}

	total := len(segments)
	var done atomic.Int32

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)

	for i := range segments {
		i := i
		seg := segments[i]
		g.Go(func() error {
			if err := idx.indexSegment(gctx, seg); err != nil {
				return fmt.Errorf("index segment %d: %w", i, err)
			}
			cur := int(done.Add(1))
			if idx.OnProgress != nil {
				idx.OnProgress(cur, total)
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return err
	}

	// Clean up chunk files after indexing completes.
	os.RemoveAll(chunkDir)
	return nil
}

// IndexDirectory indexes all supported video files in a directory tree.
func (idx *Indexer) IndexDirectory(ctx context.Context, dir string) (*IndexStats, error) {
	files, err := chunk.ScanDirectory(dir, nil)
	if err != nil {
		return nil, fmt.Errorf("scan directory: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no supported video files found in %s", dir)
	}

	for _, f := range files {
		if err := idx.IndexFile(ctx, f); err != nil {
			return nil, fmt.Errorf("index %s: %w", filepath.Base(f), err)
		}
	}

	stats := &IndexStats{
		TotalFiles:  len(files),
		BackendInfo: "chromem-go",
	}

	if idx.VecStore != nil {
		vs := idx.VecStore.Stats()
		stats.VecStoreSize = vs.DocumentCount
		stats.TotalSegments = vs.DocumentCount
	}
	if idx.DescStore != nil {
		stats.DescStoreSize = idx.DescStore.Count()
	}

	return stats, nil
}

func (idx *Indexer) indexSegment(ctx context.Context, seg MediaSegment) error {
	chunkPath := seg.ChunkPath

	// Skip still frame chunks
	if idx.Chunker.SkipStillFrames {
		still, err := chunk.IsStillFrame(ctx, chunkPath)
		if err == nil && still {
			return nil
		}
	}

	// Preprocess: downscale for embedding
	prepPath, err := chunk.PreprocessChunk(ctx, chunkPath, idx.Chunker)
	if err != nil {
		// Fallback to original chunk
		prepPath = chunkPath
	}
	defer func() {
		if prepPath != chunkPath {
			os.Remove(prepPath)
		}
	}()

	id := makeChunkID(seg)

	// Vector embedding
	if idx.Embedder != nil && idx.VecStore != nil {
		vec, err := idx.Embedder.EmbedVideo(ctx, prepPath)
		if err != nil {
			return fmt.Errorf("embed: %w", err)
		}

		metadata := map[string]string{
			"source_file": seg.SourceFile,
			"start_time":  fmt.Sprintf("%.3f", seg.StartTime),
			"end_time":    fmt.Sprintf("%.3f", seg.EndTime),
		}

		if err := idx.VecStore.Add(ctx, id, vec, metadata); err != nil {
			return fmt.Errorf("store vector: %w", err)
		}
	}

	// VLM annotation
	if idx.Annotator != nil && idx.DescStore != nil {
		framesDir, frames, err := extractKeyFrames(ctx, chunkPath)
		if err == nil && len(frames) > 0 {
			defer os.RemoveAll(framesDir)

			descSeg := describe.Segment{
				SourceFile: seg.SourceFile,
				StartTime:  seg.StartTime,
				EndTime:    seg.EndTime,
				Duration:   seg.Duration,
				ChunkPath:  seg.ChunkPath,
			}
			ann, annErr := idx.Annotator.AnnotateSegment(ctx, descSeg, frames)
			if annErr != nil {
				slog.Warn("VLM annotation failed", "segment", seg.ChunkPath, "error", annErr)
			} else if storeErr := idx.DescStore.Append(ann); storeErr != nil {
				slog.Warn("store annotation failed", "segment", seg.ChunkPath, "error", storeErr)
			}
		}
	}

	return nil
}

func (idx *Indexer) workDir() string {
	if idx.WorkDir != "" {
		return idx.WorkDir
	}
	return filepath.Join(os.TempDir(), "saker-media")
}

func makeChunkID(seg MediaSegment) string {
	raw := fmt.Sprintf("%s:%.3f-%.3f", seg.SourceFile, seg.StartTime, seg.EndTime)
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h[:16])
}

func hashPath(path string) string {
	h := sha256.Sum256([]byte(path))
	return fmt.Sprintf("%x", h[:8])
}

// extractKeyFrames extracts 3 representative frames from a chunk for VLM analysis.
// Returns the temporary directory (caller must RemoveAll) and frame file paths.
func extractKeyFrames(ctx context.Context, chunkPath string) (tmpDir string, frames []string, err error) {
	tmpDir, err = os.MkdirTemp("", "media-frames-*")
	if err != nil {
		return "", nil, err
	}

	pattern := filepath.Join(tmpDir, "frame_%02d.jpg")
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-i", chunkPath,
		"-vf", "fps=0.1,scale=480:-2",
		"-frames:v", "3",
		"-q:v", "2",
		pattern,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmpDir)
		return "", nil, fmt.Errorf("extract frames: %w\n%s", err, string(out))
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", nil, err
	}

	for _, e := range entries {
		if !e.IsDir() {
			frames = append(frames, filepath.Join(tmpDir, e.Name()))
		}
	}
	return tmpDir, frames, nil
}
