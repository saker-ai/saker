package pipeline

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/saker-ai/saker/pkg/artifact"
)

// StreamSource produces artifacts on demand from an ongoing stream.
type StreamSource interface {
	// Next blocks until the next segment is available or the stream ends.
	// Returns io.EOF when the stream is finished.
	Next(ctx context.Context) (artifact.ArtifactRef, error)
	// Done reports whether the stream has ended.
	Done() bool
	// Close releases any resources held by the source.
	Close() error
}

// BackpressurePolicy controls behavior when processing cannot keep up with input.
type BackpressurePolicy string

const (
	BackpressureBlock      BackpressurePolicy = "block"
	BackpressureDropOldest BackpressurePolicy = "drop_oldest"
	BackpressureDropNewest BackpressurePolicy = "drop_newest"
)

// ---------------------------------------------------------------------------
// FileStreamSource — simulates streaming by slicing a video file into segments
// ---------------------------------------------------------------------------

// FileStreamSource reads a video file and produces time-based segments using ffmpeg.
type FileStreamSource struct {
	videoPath       string
	segmentDuration time.Duration
	maxDimension    int

	mu       sync.Mutex
	tmpDir   string
	segments []string
	index    int
	prepared bool
	done     bool
}

// NewFileStreamSource creates a source that slices a video file into segments.
func NewFileStreamSource(videoPath string, segmentDuration time.Duration) *FileStreamSource {
	if segmentDuration <= 0 {
		segmentDuration = 2 * time.Second
	}
	return &FileStreamSource{
		videoPath:       videoPath,
		segmentDuration: segmentDuration,
	}
}

// SetMaxDimension limits the longest edge of extracted frames.
func (f *FileStreamSource) SetMaxDimension(d int) { f.maxDimension = d }

func (f *FileStreamSource) Next(ctx context.Context) (artifact.ArtifactRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.prepared {
		if err := f.prepare(ctx); err != nil {
			return artifact.ArtifactRef{}, err
		}
		f.prepared = true
	}

	if f.index >= len(f.segments) {
		f.done = true
		return artifact.ArtifactRef{}, io.EOF
	}

	path := f.segments[f.index]
	ref := artifact.ArtifactRef{
		Source:     artifact.ArtifactSourceGenerated,
		Path:       path,
		ArtifactID: fmt.Sprintf("segment_%03d", f.index),
		Kind:       artifact.ArtifactKindImage,
	}
	f.index++
	return ref, nil
}

func (f *FileStreamSource) Done() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.done
}

func (f *FileStreamSource) Close() error {
	f.mu.Lock()
	dir := f.tmpDir
	f.mu.Unlock()
	if dir != "" {
		return os.RemoveAll(dir)
	}
	return nil
}

func (f *FileStreamSource) prepare(ctx context.Context) error {
	tmpDir, err := os.MkdirTemp("", "saker-stream-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	f.tmpDir = tmpDir

	secs := int(f.segmentDuration.Seconds())
	if secs <= 0 {
		secs = 2
	}
	vf := fmt.Sprintf("fps=1/%d", secs)
	if f.maxDimension > 0 {
		vf = fmt.Sprintf("fps=1/%d,scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease",
			secs, f.maxDimension, f.maxDimension)
	}

	outPattern := filepath.Join(tmpDir, "segment_%03d.jpg")
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", f.videoPath,
		"-vf", vf,
		"-q:v", "2",
		"-y", outPattern,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("ffmpeg segment: %w\n%s", err, string(out))
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("read segments dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jpg") {
			f.segments = append(f.segments, filepath.Join(tmpDir, e.Name()))
		}
	}
	sort.Strings(f.segments)
	return nil
}

// ---------------------------------------------------------------------------
// DirectoryWatchSource — watches a directory for new image files
// ---------------------------------------------------------------------------

// maxSeenEntries caps the size of the seen map to prevent unbounded memory growth.
const maxSeenEntries = 100000

// DirectoryWatchSource emits artifacts as new image files appear in a directory.
type DirectoryWatchSource struct {
	dir          string
	pollInterval time.Duration
	extensions   map[string]struct{}

	mu       sync.Mutex
	seen     map[string]struct{}
	seenRing []string // FIFO ring buffer for eviction order
	ringIdx  int      // next write position in the ring
	done     bool
}

// NewDirectoryWatchSource creates a source that polls a directory for new images.
func NewDirectoryWatchSource(dir string, pollInterval time.Duration) *DirectoryWatchSource {
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}
	return &DirectoryWatchSource{
		dir:          dir,
		pollInterval: pollInterval,
		extensions: map[string]struct{}{
			".jpg": {}, ".jpeg": {}, ".png": {}, ".bmp": {}, ".webp": {},
		},
		seen:     map[string]struct{}{},
		seenRing: make([]string, maxSeenEntries),
	}
}

func (d *DirectoryWatchSource) Next(ctx context.Context) (artifact.ArtifactRef, error) {
	for {
		select {
		case <-ctx.Done():
			d.mu.Lock()
			d.done = true
			d.mu.Unlock()
			return artifact.ArtifactRef{}, ctx.Err()
		default:
		}

		entries, err := os.ReadDir(d.dir)
		if err != nil {
			return artifact.ArtifactRef{}, fmt.Errorf("read watch dir: %w", err)
		}

		// Sort for deterministic order
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})

		d.mu.Lock()
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if _, ok := d.extensions[ext]; !ok {
				continue
			}
			path := filepath.Join(d.dir, e.Name())
			if _, ok := d.seen[path]; ok {
				continue
			}
			// Evict oldest entry via ring buffer before inserting new one.
			evicted := d.seenRing[d.ringIdx]
			if evicted != "" {
				delete(d.seen, evicted)
			}
			d.seenRing[d.ringIdx] = path
			d.ringIdx = (d.ringIdx + 1) % maxSeenEntries
			d.seen[path] = struct{}{}
			idx := len(d.seen) - 1
			d.mu.Unlock()
			return artifact.ArtifactRef{
				Source:     artifact.ArtifactSourceLocal,
				Path:       path,
				ArtifactID: fmt.Sprintf("watch_%03d", idx),
				Kind:       artifact.ArtifactKindImage,
			}, nil
		}
		d.mu.Unlock()

		select {
		case <-ctx.Done():
			d.mu.Lock()
			d.done = true
			d.mu.Unlock()
			return artifact.ArtifactRef{}, ctx.Err()
		case <-time.After(d.pollInterval):
		}
	}
}

func (d *DirectoryWatchSource) Done() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.done
}

func (d *DirectoryWatchSource) Close() error {
	d.mu.Lock()
	d.done = true
	d.mu.Unlock()
	return nil
}

// ---------------------------------------------------------------------------
// SliceSource — simple source from a pre-built slice (for testing)
// ---------------------------------------------------------------------------

// SliceSource emits artifacts from a fixed slice. Useful for testing.
type SliceSource struct {
	refs  []artifact.ArtifactRef
	mu    sync.Mutex
	index int
}

// NewSliceSource creates a source from a pre-built artifact list.
func NewSliceSource(refs []artifact.ArtifactRef) *SliceSource {
	return &SliceSource{refs: append([]artifact.ArtifactRef(nil), refs...)}
}

func (s *SliceSource) Next(ctx context.Context) (artifact.ArtifactRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index >= len(s.refs) {
		return artifact.ArtifactRef{}, io.EOF
	}
	ref := s.refs[s.index]
	s.index++
	return ref, nil
}

func (s *SliceSource) Done() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.index >= len(s.refs)
}

func (s *SliceSource) Close() error { return nil }
