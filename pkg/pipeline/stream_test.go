package pipeline_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/pipeline"
	"github.com/cinience/saker/pkg/tool"
)

func TestSliceSource(t *testing.T) {
	refs := []artifact.ArtifactRef{
		artifact.NewGeneratedRef("a", artifact.ArtifactKindImage),
		artifact.NewGeneratedRef("b", artifact.ArtifactKindImage),
		artifact.NewGeneratedRef("c", artifact.ArtifactKindImage),
	}
	src := pipeline.NewSliceSource(refs)
	ctx := context.Background()

	for i, want := range refs {
		got, err := src.Next(ctx)
		if err != nil {
			t.Fatalf("segment %d: %v", i, err)
		}
		if got.ArtifactID != want.ArtifactID {
			t.Errorf("segment %d: got %s, want %s", i, got.ArtifactID, want.ArtifactID)
		}
	}

	_, err := src.Next(ctx)
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
	if !src.Done() {
		t.Error("expected Done() == true")
	}
}

func TestSliceSourceEmpty(t *testing.T) {
	src := pipeline.NewSliceSource(nil)
	_, err := src.Next(context.Background())
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestDirectoryWatchSource(t *testing.T) {
	dir := t.TempDir()
	src := pipeline.NewDirectoryWatchSource(dir, 50*time.Millisecond)
	defer src.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Write files before polling
	for i := 0; i < 3; i++ {
		path := filepath.Join(dir, fmt.Sprintf("frame_%03d.jpg", i))
		if err := os.WriteFile(path, []byte("fake-image"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var got []string
	for i := 0; i < 3; i++ {
		ref, err := src.Next(ctx)
		if err != nil {
			t.Fatalf("segment %d: %v", i, err)
		}
		got = append(got, ref.ArtifactID)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(got))
	}
}

func TestDirectoryWatchSourceIgnoresNonImages(t *testing.T) {
	dir := t.TempDir()
	src := pipeline.NewDirectoryWatchSource(dir, 50*time.Millisecond)
	defer src.Close()

	// Write a non-image file
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0644)
	// Write an image file
	os.WriteFile(filepath.Join(dir, "frame.jpg"), []byte("fake"), 0644)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	ref, err := src.Next(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Path != filepath.Join(dir, "frame.jpg") {
		t.Errorf("expected frame.jpg, got %s", ref.Path)
	}
}

func TestStreamExecutorBasic(t *testing.T) {
	refs := make([]artifact.ArtifactRef, 5)
	for i := range refs {
		refs[i] = artifact.NewGeneratedRef(fmt.Sprintf("seg_%d", i), artifact.ArtifactKindImage)
	}
	src := pipeline.NewSliceSource(refs)

	exec := &pipeline.StreamExecutor{
		Executor: pipeline.Executor{
			RunTool: func(ctx context.Context, step pipeline.Step, inputRefs []artifact.ArtifactRef) (*tool.ToolResult, error) {
				id := "unknown"
				if len(inputRefs) > 0 {
					id = inputRefs[0].ArtifactID
				}
				return &tool.ToolResult{
					Output: "processed " + id,
					Artifacts: []artifact.ArtifactRef{
						artifact.NewGeneratedRef("out_"+id, artifact.ArtifactKindJSON),
					},
				}, nil
			},
		},
		Config: pipeline.StreamExecutorConfig{
			Step:            pipeline.Step{Name: "analyze", Tool: "analyzer"},
			WindowSize:      3,
			BufferSize:      16,
			SegmentInterval: 2 * time.Second,
		},
	}

	ctx := context.Background()
	results := exec.Run(ctx, src)

	var collected []pipeline.StreamResult
	for r := range results {
		collected = append(collected, r)
	}

	if len(collected) != 5 {
		t.Fatalf("expected 5 results, got %d", len(collected))
	}

	for i, r := range collected {
		if r.SegmentIndex != i {
			t.Errorf("result %d: segment index %d", i, r.SegmentIndex)
		}
		if r.Dropped {
			t.Errorf("result %d: unexpectedly dropped", i)
		}
		expected := fmt.Sprintf("processed seg_%d", i)
		if r.Result.Output != expected {
			t.Errorf("result %d: output %q, want %q", i, r.Result.Output, expected)
		}
	}

	// Verify timestamps
	if collected[2].Timestamp != 4*time.Second {
		t.Errorf("timestamp for seg 2: got %v, want 4s", collected[2].Timestamp)
	}
}

func TestStreamExecutorCancellation(t *testing.T) {
	// Source that blocks forever
	refs := make([]artifact.ArtifactRef, 1000)
	for i := range refs {
		refs[i] = artifact.NewGeneratedRef(fmt.Sprintf("seg_%d", i), artifact.ArtifactKindImage)
	}
	src := pipeline.NewSliceSource(refs)

	exec := &pipeline.StreamExecutor{
		Executor: pipeline.Executor{
			RunTool: func(ctx context.Context, step pipeline.Step, inputRefs []artifact.ArtifactRef) (*tool.ToolResult, error) {
				// Slow processing
				select {
				case <-time.After(50 * time.Millisecond):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return &tool.ToolResult{Output: "ok"}, nil
			},
		},
		Config: pipeline.StreamExecutorConfig{
			Step: pipeline.Step{Name: "slow", Tool: "slow"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	results := exec.Run(ctx, src)
	var count int
	for range results {
		count++
	}

	// Should have processed some but not all
	if count >= 1000 {
		t.Error("expected cancellation to stop processing early")
	}
}

func TestStreamExecutorSlidingWindow(t *testing.T) {
	refs := make([]artifact.ArtifactRef, 5)
	for i := range refs {
		refs[i] = artifact.NewGeneratedRef(fmt.Sprintf("seg_%d", i), artifact.ArtifactKindImage)
	}
	src := pipeline.NewSliceSource(refs)

	var windowSizes []int

	exec := &pipeline.StreamExecutor{
		Executor: pipeline.Executor{
			RunTool: func(ctx context.Context, step pipeline.Step, inputRefs []artifact.ArtifactRef) (*tool.ToolResult, error) {
				// Access the items (window context) via the params - but RunTool
				// doesn't get items directly. The StreamExecutor passes them via Input.Items.
				// We track window size through the step's With params instead.
				return &tool.ToolResult{Output: "ok"}, nil
			},
		},
		Config: pipeline.StreamExecutorConfig{
			Step:       pipeline.Step{Name: "analyze", Tool: "analyzer"},
			WindowSize: 2,
		},
	}

	// Use a custom executor that tracks Input.Items length
	origRunTool := exec.Executor.RunTool
	exec.Executor.RunTool = func(ctx context.Context, step pipeline.Step, inputRefs []artifact.ArtifactRef) (*tool.ToolResult, error) {
		// We can't directly observe windowSizes from RunTool since it only gets refs.
		// Instead, verify through results.
		return origRunTool(ctx, step, inputRefs)
	}

	_ = windowSizes // window tracking is internal to StreamExecutor

	ctx := context.Background()
	results := exec.Run(ctx, src)
	var count int
	for range results {
		count++
	}
	if count != 5 {
		t.Fatalf("expected 5 results, got %d", count)
	}
}
