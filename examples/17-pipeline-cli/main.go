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

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/pipeline"
	"github.com/saker-ai/saker/pkg/tool"
)

func main() {
	pipelineFile := flag.String("pipeline", "", "Pipeline JSON file")
	showTimeline := flag.Bool("timeline", false, "Print timeline events")
	lineageFormat := flag.String("lineage", "", "Lineage output format (dot)")
	flag.Parse()

	if *pipelineFile == "" {
		log.Fatal("--pipeline is required")
	}

	step, err := loadPipeline(*pipelineFile)
	if err != nil {
		log.Fatalf("load pipeline: %v", err)
	}

	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot: ".",
		Model:       staticModel{},
		CustomTools: []tool.Tool{
			frameExtractor{},
			stylizer{},
			composer{},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	resp, err := rt.Run(context.Background(), api.Request{
		Pipeline: &step,
	})
	if err != nil {
		log.Fatal(err)
	}

	printResult(resp, *showTimeline, *lineageFormat)
}

func loadPipeline(path string) (pipeline.Step, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pipeline.Step{}, err
	}
	var step pipeline.Step
	if err := json.Unmarshal(data, &step); err != nil {
		return pipeline.Step{}, err
	}
	return step, nil
}

func printResult(resp *api.Response, showTimeline bool, lineageFormat string) {
	if resp == nil || resp.Result == nil {
		return
	}
	r := resp.Result

	fmt.Println("=== PIPELINE RESULT ===")
	if r.Output != "" {
		fmt.Printf("output: %s\n", r.Output)
	}
	fmt.Printf("stop_reason: %s\n", r.StopReason)
	if len(r.Artifacts) > 0 {
		fmt.Printf("artifacts: %d\n", len(r.Artifacts))
		for _, a := range r.Artifacts {
			fmt.Printf("  [%s] %s (%s)\n", a.Kind, a.ArtifactID, a.Source)
		}
	}
	fmt.Println()

	if showTimeline && len(resp.Timeline) > 0 {
		fmt.Printf("=== TIMELINE (%d events) ===\n", len(resp.Timeline))
		for _, e := range resp.Timeline {
			switch e.Kind {
			case api.TimelineToolCall:
				fmt.Printf("  %-20s %s\n", e.Kind, e.Name)
			case api.TimelineToolResult:
				fmt.Printf("  %-20s %-20s %s\n", e.Kind, e.Name, truncate(e.Output, 60))
			case api.TimelineLatencySnapshot:
				fmt.Printf("  %-20s %-20s %v\n", e.Kind, e.Name, e.Duration)
			case api.TimelineCacheHit, api.TimelineCacheMiss:
				fmt.Printf("  %-20s %s\n", e.Kind, truncate(e.CacheKey, 40))
			case api.TimelineInputArtifact, api.TimelineGeneratedArtifact:
				id := ""
				if e.Artifact != nil {
					id = e.Artifact.ArtifactID
				}
				fmt.Printf("  %-20s %-20s %s\n", e.Kind, e.Name, id)
			default:
				fmt.Printf("  %-20s %s\n", e.Kind, e.Name)
			}
		}
		fmt.Println()
	}

	if strings.EqualFold(lineageFormat, "dot") && len(r.Lineage.Edges) > 0 {
		fmt.Println("=== LINEAGE ===")
		fmt.Print(r.Lineage.ToDOT())
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// --- Stub tools ---

const numFrames = 8

type frameExtractor struct{}

func (frameExtractor) Name() string             { return "frame_extractor" }
func (frameExtractor) Description() string      { return "Extracts frames from a video" }
func (frameExtractor) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (frameExtractor) Execute(_ context.Context, params map[string]any) (*tool.ToolResult, error) {
	time.Sleep(5 * time.Millisecond)
	arts := make([]artifact.ArtifactRef, numFrames)
	for i := range arts {
		arts[i] = artifact.NewGeneratedRef(fmt.Sprintf("frame_%03d", i), artifact.ArtifactKindImage)
	}
	return &tool.ToolResult{
		Output:    fmt.Sprintf("extracted %d frames", numFrames),
		Artifacts: arts,
	}, nil
}

type stylizer struct{}

func (stylizer) Name() string             { return "stylizer" }
func (stylizer) Description() string      { return "Applies style transfer to an image" }
func (stylizer) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (stylizer) Execute(_ context.Context, params map[string]any) (*tool.ToolResult, error) {
	time.Sleep(10 * time.Millisecond)
	refs, _ := params["artifacts"].([]artifact.ArtifactRef)
	inputID := "unknown"
	if len(refs) > 0 {
		inputID = refs[0].ArtifactID
	}
	outID := "styled_" + inputID
	return &tool.ToolResult{
		Output: fmt.Sprintf("stylized %s", inputID),
		Artifacts: []artifact.ArtifactRef{
			artifact.NewGeneratedRef(outID, artifact.ArtifactKindImage),
		},
	}, nil
}

type composer struct{}

func (composer) Name() string             { return "composer" }
func (composer) Description() string      { return "Composes styled frames into final output" }
func (composer) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (composer) Execute(_ context.Context, params map[string]any) (*tool.ToolResult, error) {
	time.Sleep(5 * time.Millisecond)
	return &tool.ToolResult{
		Output: "composed final video from styled frames",
		Artifacts: []artifact.ArtifactRef{
			artifact.NewGeneratedRef("final_video", artifact.ArtifactKindVideo),
		},
	}, nil
}

// staticModel satisfies the model.Model interface without calling any API.
type staticModel struct{}

func (staticModel) Complete(context.Context, model.Request) (*model.Response, error) {
	return &model.Response{Message: model.Message{Role: "assistant", Content: "unused"}}, nil
}

func (staticModel) CompleteStream(context.Context, model.Request, model.StreamHandler) error {
	return nil
}
