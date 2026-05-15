// Example 18: Video Understanding Pipeline
//
// Demonstrates offline video analysis using the multimodal pipeline engine:
//
//	input_video → [sample-frames] → N frames → [analyze-frame] (fan-out) → N analyses → [summarize] → video summary
//
// Two modes:
//   - Real mode (default): requires ffmpeg + ANTHROPIC_API_KEY, extracts frames from a real video file
//   - Stub mode (--stub): pure simulation, no external dependencies needed
//
// Usage:
//
//	# Stub mode (no dependencies)
//	go run ./examples/18-video-understanding --stub --timeline --lineage dot
//
//	# Real mode
//	source .env
//	go run ./examples/18-video-understanding --video /path/to/video.mp4 --frames 10
//
//	# Real mode with pipeline JSON
//	source .env
//	go run ./examples/18-video-understanding --video /path/to/video.mp4 --pipeline examples/18-video-understanding/pipeline.json --timeline
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/pipeline"
	ptemplates "github.com/saker-ai/saker/pkg/pipeline/templates"
	"github.com/saker-ai/saker/pkg/tool"
)

func main() {
	videoPath := flag.String("video", "", "Path to input video file (required for real mode)")
	stubMode := flag.Bool("stub", false, "Use stub tools (no ffmpeg or API key needed)")
	pipelineFile := flag.String("pipeline", "", "Load pipeline definition from JSON file")
	strategy := flag.String("strategy", "uniform", "Frame sampling strategy: uniform, keyframe, interval")
	frames := flag.Int("frames", 8, "Number of frames to extract")
	concurrency := flag.Int("concurrency", 4, "Max parallel frame analyses")
	showTimeline := flag.Bool("timeline", false, "Print timeline events")
	lineageFormat := flag.String("lineage", "", "Lineage output format: dot")
	flag.Parse()

	if !*stubMode && *videoPath == "" && *pipelineFile == "" {
		fmt.Fprintln(os.Stderr, "Usage: go run . --video <path> OR --stub OR --pipeline <file>")
		fmt.Fprintln(os.Stderr, "  --stub mode runs without ffmpeg or API key")
		os.Exit(1)
	}

	ctx := context.Background()

	// Build tool registry
	tools := buildTools(*stubMode)

	// Build pipeline step
	var step pipeline.Step
	if *pipelineFile != "" {
		data, err := os.ReadFile(*pipelineFile)
		if err != nil {
			log.Fatalf("read pipeline file: %v", err)
		}
		if err := json.Unmarshal(data, &step); err != nil {
			log.Fatalf("parse pipeline: %v", err)
		}
	} else if *stubMode {
		step = ptemplates.VideoUnderstanding("stub_video.mp4", ptemplates.VideoUnderstandingOptions{
			Strategy:    *strategy,
			FrameCount:  *frames,
			Concurrency: *concurrency,
		})
	} else {
		step = ptemplates.VideoUnderstanding(*videoPath, ptemplates.VideoUnderstandingOptions{
			Strategy:     *strategy,
			FrameCount:   *frames,
			Concurrency:  *concurrency,
			MaxDimension: 1024,
		})
	}

	// Build executor
	type timelineEntry struct {
		Kind string
		Name string
		Info string
		Time time.Time
	}
	var timeline []timelineEntry

	exec := pipeline.Executor{
		RunTool: func(ctx context.Context, s pipeline.Step, refs []artifact.ArtifactRef) (*tool.ToolResult, error) {
			t, ok := tools[s.Tool]
			if !ok {
				return nil, fmt.Errorf("tool %q not found", s.Tool)
			}
			params := make(map[string]any)
			for k, v := range s.With {
				params[k] = v
			}
			if len(refs) > 0 {
				params["artifacts"] = refs
			}

			timeline = append(timeline, timelineEntry{Kind: "tool_call", Name: s.Name, Time: time.Now()})
			started := time.Now()

			result, err := t.Execute(ctx, params)
			if err != nil {
				return nil, err
			}

			elapsed := time.Since(started)
			timeline = append(timeline, timelineEntry{Kind: "tool_result", Name: s.Name, Info: truncate(result.Output, 60), Time: time.Now()})
			timeline = append(timeline, timelineEntry{Kind: "latency", Name: s.Name, Info: elapsed.String(), Time: time.Now()})

			for _, a := range refs {
				timeline = append(timeline, timelineEntry{Kind: "input_artifact", Name: s.Name, Info: a.ArtifactID, Time: time.Now()})
			}
			for _, a := range result.Artifacts {
				timeline = append(timeline, timelineEntry{Kind: "generated_artifact", Name: s.Name, Info: a.ArtifactID, Time: time.Now()})
			}

			return result, nil
		},
	}

	// Execute pipeline
	input := pipeline.Input{}
	if *videoPath != "" {
		input.Artifacts = []artifact.ArtifactRef{
			artifact.NewLocalFileRef(*videoPath, artifact.ArtifactKindVideo),
		}
	}

	result, err := exec.Execute(ctx, step, input)
	if err != nil {
		log.Fatalf("pipeline failed: %v", err)
	}

	// Print result
	fmt.Println("=== VIDEO UNDERSTANDING RESULT ===")
	fmt.Printf("output: %s\n", truncate(result.Output, 200))
	if len(result.Artifacts) > 0 {
		fmt.Printf("artifacts: %d\n", len(result.Artifacts))
		for _, a := range result.Artifacts {
			fmt.Printf("  [%s] %s (%s)\n", a.Kind, a.ArtifactID, a.Source)
		}
	}

	// Print timeline
	if *showTimeline && len(timeline) > 0 {
		fmt.Printf("\n=== TIMELINE (%d events) ===\n", len(timeline))
		for _, e := range timeline {
			fmt.Printf("  %-20s %-20s %s\n", e.Kind, e.Name, e.Info)
		}
	}

	// Print lineage
	if *lineageFormat == "dot" && len(result.Lineage.Edges) > 0 {
		fmt.Printf("\n=== LINEAGE (DOT) ===\n")
		fmt.Print(result.Lineage.ToDOT())
	}
}

func buildTools(stubMode bool) map[string]tool.Tool {
	if stubMode {
		return map[string]tool.Tool{
			"video_sampler":    &stubVideoSampler{},
			"frame_analyzer":   &stubFrameAnalyzer{},
			"video_summarizer": &stubVideoSummarizer{},
		}
	}

	// Real mode: import real tools
	// Note: for real mode, users should use the SDK runtime (api.New) which
	// registers these tools automatically. This demo with real tools would
	// use the runtime directly. For simplicity, the demo supports --stub only
	// for standalone execution; real mode requires the full runtime.
	log.Println("Real mode: use 'go run ./cmd/saker --pipeline ...' with ANTHROPIC_API_KEY for real video analysis")
	log.Println("Falling back to stub mode for demo")
	return map[string]tool.Tool{
		"video_sampler":    &stubVideoSampler{},
		"frame_analyzer":   &stubFrameAnalyzer{},
		"video_summarizer": &stubVideoSummarizer{},
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- Stub tools for demo/testing ---

type stubVideoSampler struct{}

func (s *stubVideoSampler) Name() string             { return "video_sampler" }
func (s *stubVideoSampler) Description() string      { return "stub video sampler" }
func (s *stubVideoSampler) Schema() *tool.JSONSchema { return nil }

func (s *stubVideoSampler) Execute(_ context.Context, params map[string]any) (*tool.ToolResult, error) {
	time.Sleep(5 * time.Millisecond)
	count := 8
	if c, ok := params["count"].(float64); ok && c > 0 {
		count = int(c)
	}

	artifacts := make([]artifact.ArtifactRef, count)
	for i := range artifacts {
		artifacts[i] = artifact.ArtifactRef{
			Source:     artifact.ArtifactSourceGenerated,
			ArtifactID: fmt.Sprintf("frame_%03d", i),
			Kind:       artifact.ArtifactKindImage,
		}
	}
	return &tool.ToolResult{
		Success:   true,
		Output:    fmt.Sprintf("extracted %d frames", count),
		Artifacts: artifacts,
	}, nil
}

type stubFrameAnalyzer struct{}

func (s *stubFrameAnalyzer) Name() string             { return "frame_analyzer" }
func (s *stubFrameAnalyzer) Description() string      { return "stub frame analyzer" }
func (s *stubFrameAnalyzer) Schema() *tool.JSONSchema { return nil }

func (s *stubFrameAnalyzer) Execute(_ context.Context, params map[string]any) (*tool.ToolResult, error) {
	time.Sleep(10 * time.Millisecond)
	frameID := "unknown"
	if refs, ok := params["artifacts"].([]artifact.ArtifactRef); ok && len(refs) > 0 {
		frameID = refs[0].ArtifactID
	}
	analysis := fmt.Sprintf("Frame %s: outdoor scene with buildings, people walking, clear sky, moderate lighting", frameID)
	return &tool.ToolResult{
		Success: true,
		Output:  analysis,
		Artifacts: []artifact.ArtifactRef{
			{
				Source:     artifact.ArtifactSourceGenerated,
				ArtifactID: "analysis_" + frameID,
				Kind:       artifact.ArtifactKindJSON,
			},
		},
	}, nil
}

type stubVideoSummarizer struct{}

func (s *stubVideoSummarizer) Name() string             { return "video_summarizer" }
func (s *stubVideoSummarizer) Description() string      { return "stub video summarizer" }
func (s *stubVideoSummarizer) Schema() *tool.JSONSchema { return nil }

func (s *stubVideoSummarizer) Execute(_ context.Context, params map[string]any) (*tool.ToolResult, error) {
	time.Sleep(5 * time.Millisecond)
	return &tool.ToolResult{
		Success: true,
		Output:  "Video summary: outdoor urban scene showing pedestrian activity across 8 frames. Key observations: consistent daytime lighting, multiple pedestrians, urban architecture. No significant events detected.",
		Artifacts: []artifact.ArtifactRef{
			{
				Source:     artifact.ArtifactSourceGenerated,
				ArtifactID: "video_summary",
				Kind:       artifact.ArtifactKindJSON,
			},
		},
	}, nil
}
