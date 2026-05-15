package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/pipeline"
)

func loadPipeline(path string) (pipeline.Step, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pipeline.Step{}, err
	}
	var step pipeline.Step
	if err := json.Unmarshal(data, &step); err != nil {
		return pipeline.Step{}, fmt.Errorf("parse pipeline JSON: %w", err)
	}
	return step, nil
}

func printPipelineResponse(resp *api.Response, out io.Writer, showTimeline bool, lineageFormat string) {
	if resp == nil || resp.Result == nil || out == nil {
		return
	}
	r := resp.Result

	fmt.Fprintf(out, "=== PIPELINE RESULT ===\n")
	if r.Output != "" {
		fmt.Fprintf(out, "output: %s\n", r.Output)
	}
	if r.StopReason != "" {
		fmt.Fprintf(out, "stop_reason: %s\n", r.StopReason)
	}
	if len(r.Artifacts) > 0 {
		fmt.Fprintf(out, "artifacts: %d\n", len(r.Artifacts))
		for _, a := range r.Artifacts {
			fmt.Fprintf(out, "  [%s] %s (%s)\n", a.Kind, a.ArtifactID, a.Source)
		}
	}
	fmt.Fprintln(out)

	if showTimeline && len(resp.Timeline) > 0 {
		fmt.Fprintf(out, "=== TIMELINE (%d events) ===\n", len(resp.Timeline))
		for _, e := range resp.Timeline {
			switch e.Kind {
			case api.TimelineToolCall:
				fmt.Fprintf(out, "  %-20s %s\n", e.Kind, e.Name)
			case api.TimelineToolResult:
				fmt.Fprintf(out, "  %-20s %-20s %s\n", e.Kind, e.Name, e.Output)
			case api.TimelineLatencySnapshot:
				fmt.Fprintf(out, "  %-20s %-20s %v\n", e.Kind, e.Name, e.Duration)
			case api.TimelineCacheHit, api.TimelineCacheMiss:
				fmt.Fprintf(out, "  %-20s %s\n", e.Kind, e.CacheKey)
			case api.TimelineInputArtifact, api.TimelineGeneratedArtifact:
				id := ""
				if e.Artifact != nil {
					id = e.Artifact.ArtifactID
				}
				fmt.Fprintf(out, "  %-20s %-20s %s\n", e.Kind, e.Name, id)
			default:
				fmt.Fprintf(out, "  %-20s %s\n", e.Kind, e.Name)
			}
		}
		fmt.Fprintln(out)
	}

	if strings.EqualFold(lineageFormat, "dot") && len(r.Lineage.Edges) > 0 {
		fmt.Fprintf(out, "=== LINEAGE ===\n")
		fmt.Fprint(out, r.Lineage.ToDOT())
	}
}
