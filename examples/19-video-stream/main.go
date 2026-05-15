// Example 19: Near-Realtime Video Stream Analysis
//
// Demonstrates streaming video analysis using StreamExecutor + SliceSource/DirectoryWatchSource:
//
//	source → [segment arrives] → [analyze-segment] → [emit result] → repeat
//
// Two modes:
//   - Stub mode (default): simulates 10 segments with stub analysis tool
//   - Watch mode (--watch-dir): monitors a directory for new image files
//
// Usage:
//
//	# Stub mode
//	go run ./examples/19-video-stream
//
//	# Watch directory mode (write images to /tmp/frames while running)
//	go run ./examples/19-video-stream --watch-dir /tmp/frames --timeout 30s
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/pipeline"
	"github.com/saker-ai/saker/pkg/tool"
)

func main() {
	watchDir := flag.String("watch-dir", "", "Directory to watch for new image files")
	timeout := flag.Duration("timeout", 10*time.Second, "Timeout for stream processing")
	windowSize := flag.Int("window", 3, "Sliding window size for context")
	backpressure := flag.String("backpressure", "block", "Backpressure policy: block, drop_oldest, drop_newest")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Build stream source
	var source pipeline.StreamSource
	if *watchDir != "" {
		if info, err := os.Stat(*watchDir); err != nil || !info.IsDir() {
			log.Fatalf("watch dir %q does not exist or is not a directory", *watchDir)
		}
		fmt.Printf("Watching directory: %s (timeout: %s)\n", *watchDir, *timeout)
		fmt.Println("Drop image files (jpg/png) into the directory to trigger analysis.")
		source = pipeline.NewDirectoryWatchSource(*watchDir, 500*time.Millisecond)
	} else {
		fmt.Println("Stub mode: simulating 10 video segments")
		refs := make([]artifact.ArtifactRef, 10)
		for i := range refs {
			refs[i] = artifact.NewGeneratedRef(fmt.Sprintf("segment_%03d", i), artifact.ArtifactKindImage)
		}
		source = pipeline.NewSliceSource(refs)
	}
	defer source.Close()

	// Build stream executor
	bp := pipeline.BackpressureBlock
	switch *backpressure {
	case "drop_oldest":
		bp = pipeline.BackpressureDropOldest
	case "drop_newest":
		bp = pipeline.BackpressureDropNewest
	}

	exec := &pipeline.StreamExecutor{
		Executor: pipeline.Executor{
			RunTool: func(ctx context.Context, step pipeline.Step, refs []artifact.ArtifactRef) (*tool.ToolResult, error) {
				id := "unknown"
				if len(refs) > 0 {
					id = refs[0].ArtifactID
				}
				// Simulate analysis delay
				time.Sleep(20 * time.Millisecond)
				analysis := fmt.Sprintf("Segment %s: detected movement, 3 people, outdoor scene, daylight", id)
				return &tool.ToolResult{
					Success: true,
					Output:  analysis,
					Artifacts: []artifact.ArtifactRef{
						artifact.NewGeneratedRef("analysis_"+id, artifact.ArtifactKindJSON),
					},
				}, nil
			},
		},
		Config: pipeline.StreamExecutorConfig{
			Step:            pipeline.Step{Name: "analyze-segment", Tool: "stub_analyzer"},
			WindowSize:      *windowSize,
			Backpressure:    bp,
			BufferSize:      16,
			SegmentInterval: 2 * time.Second,
		},
	}

	// Run stream
	fmt.Println()
	results := exec.Run(ctx, source)

	count := 0
	for r := range results {
		count++
		if r.Dropped {
			fmt.Printf("[%s] DROPPED segment %d (backpressure)\n", fmtDuration(r.Timestamp), r.SegmentIndex)
			continue
		}
		output := strings.ReplaceAll(r.Result.Output, "\n", " ")
		if len(output) > 80 {
			output = output[:80] + "..."
		}
		fmt.Printf("[%s] segment %d: %s\n", fmtDuration(r.Timestamp), r.SegmentIndex, output)
		if len(r.Result.Lineage.Edges) > 0 {
			fmt.Printf("         lineage: %d edges\n", len(r.Result.Lineage.Edges))
		}
	}

	fmt.Printf("\nStream ended: processed %d segments\n", count)
}

func fmtDuration(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}
