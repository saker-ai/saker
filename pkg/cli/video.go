package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/clikit"
	modelpkg "github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/pipeline"
	"github.com/saker-ai/saker/pkg/tool"
	toolbuiltin "github.com/saker-ai/saker/pkg/tool/builtin"
)

func runVideoStream(out io.Writer, source string, segDuration time.Duration, windowSize, sampleRate int, events string, timeoutMs int) error {
	runTool := buildToolRunner(nil) // no model in non-interactive mode
	return pipeline.RunVideoStream(out, source, segDuration, windowSize, sampleRate, events, timeoutMs, runTool)
}

// runVideoWithREPL starts video stream processing in background and enters
// the interactive REPL. The user can chat with the agent while events are
// detected and displayed inline. Use /video-status and /video-stop commands.
func runVideoWithREPL(stdout, stderr io.Writer, adapter clikit.ReplEngine,
	source string, segDuration time.Duration, windowSize, sampleRate int,
	events string, timeoutMs int, verbose bool, waterfall, sessionID string) error {

	ctx, cancel := context.WithCancel(context.Background())
	vc := &pipeline.VideoController{
		Cancel: cancel,
		Done:   make(chan struct{}),
	}

	// Build source
	src := pipeline.BuildStreamSource(stdout, source, segDuration, sampleRate)

	// Build tool runner that uses builtin tools directly
	runTool := buildToolRunner(nil) // model not available in pipeline mode

	// Start background video processing
	go func() {
		defer close(vc.Done)
		defer src.Close()

		if strings.TrimSpace(events) != "" {
			// Frame processor with event detection
			var rules []pipeline.EventRule
			for _, kw := range strings.Split(events, ",") {
				kw = strings.TrimSpace(kw)
				if kw != "" {
					rules = append(rules, pipeline.NewKeywordEventRule(kw+"_detected", kw, 3*time.Second))
				}
			}
			fp := &pipeline.FrameProcessor{
				Executor: pipeline.Executor{RunTool: runTool},
				Config: pipeline.FrameProcessorConfig{
					Step:          pipeline.Step{Name: "analyze-frame", Tool: "frame_analyzer"},
					SampleRate:    sampleRate,
					ContextWindow: windowSize,
					EventRules:    rules,
					FrameInterval: segDuration,
					OnEvent: func(ev pipeline.Event) {
						fmt.Fprintf(stdout, "\n  ** VIDEO EVENT [%s] frame %d: %s **\n> ", ev.Type, ev.Frame, ev.Detail)
					},
				},
			}
			results := fp.Run(ctx, src)
			for r := range results {
				if r.Skipped {
					vc.IncSkipped()
					continue
				}
				vc.IncProcessed()
				vc.AddEvents(len(r.Events))
			}
		} else {
			// Stream executor
			se := &pipeline.StreamExecutor{
				Executor: pipeline.Executor{RunTool: runTool},
				Config: pipeline.StreamExecutorConfig{
					Step:            pipeline.Step{Name: "analyze-segment", Tool: "frame_analyzer"},
					WindowSize:      windowSize,
					BufferSize:      16,
					SegmentInterval: segDuration,
				},
			}
			results := se.Run(ctx, src)
			for r := range results {
				vc.IncProcessed()
				if r.Dropped {
					vc.IncSkipped()
				}
			}
		}
	}()

	// Build custom commands for video control
	customCmds := func(input string, out io.Writer) (bool, bool) {
		fields := strings.Fields(input)
		if len(fields) == 0 {
			return false, false
		}
		cmd := strings.ToLower(fields[0])
		switch cmd {
		case "/video-status":
			p, s, e := vc.Stats()
			select {
			case <-vc.Done:
				fmt.Fprintf(out, "Video stream: stopped (processed: %d, skipped: %d, events: %d)\n", p, s, e)
			default:
				fmt.Fprintf(out, "Video stream: running (processed: %d, skipped: %d, events: %d)\n", p, s, e)
			}
			return true, false
		case "/video-stop":
			select {
			case <-vc.Done:
				fmt.Fprintln(out, "Video stream already stopped.")
			default:
				vc.Stop()
				p, s, e := vc.Stats()
				fmt.Fprintf(out, "Video stream stopped. (processed: %d, skipped: %d, events: %d)\n", p, s, e)
			}
			return true, false
		}
		return false, false
	}

	// Print banner and enter REPL
	clikit.PrintBanner(stdout, adapter.ModelName(), adapter.Skills())

	bannerExtra := fmt.Sprintf("Video stream active: %s\nCommands: /video-status /video-stop\n", source)

	err := clikit.RunInteractiveShellOpts(context.Background(), os.Stdin, stdout, stderr, clikit.InteractiveShellConfig{
		Engine:            adapter,
		InitialSessionID:  sessionID,
		TimeoutMs:         timeoutMs,
		Verbose:           verbose,
		WaterfallMode:     waterfall,
		ShowStatusPerTurn: true,
		CustomCommands:    customCmds,
		BannerExtra:       bannerExtra,
	})

	// Stop video on REPL exit
	select {
	case <-vc.Done:
	default:
		vc.Stop()
	}

	p, s, e := vc.Stats()
	fmt.Fprintf(stdout, "Video stream ended: %d processed, %d skipped, %d events\n", p, s, e)
	return err
}

// buildToolRunner creates a RunTool function that dispatches to builtin tools.
// If mdl is non-nil, model-aware tools (frame_analyzer, video_summarizer) are available.
func buildToolRunner(mdl modelpkg.Model) func(context.Context, pipeline.Step, []artifact.ArtifactRef) (*tool.ToolResult, error) {
	tools := map[string]tool.Tool{
		"video_sampler":  toolbuiltin.NewVideoSamplerTool(),
		"stream_capture": toolbuiltin.NewStreamCaptureTool(),
	}
	if mdl != nil {
		tools["frame_analyzer"] = toolbuiltin.NewFrameAnalyzerTool(mdl)
		tools["video_summarizer"] = toolbuiltin.NewVideoSummarizerTool(mdl)
		tools["analyze_video"] = toolbuiltin.NewAnalyzeVideoTool(mdl)
	}
	return func(ctx context.Context, step pipeline.Step, refs []artifact.ArtifactRef) (*tool.ToolResult, error) {
		t, ok := tools[step.Tool]
		if !ok {
			return nil, fmt.Errorf("unknown tool %q", step.Tool)
		}
		params := make(map[string]any)
		for k, v := range step.With {
			params[k] = v
		}
		if len(refs) > 0 {
			params["artifacts"] = refs
		}
		return t.Execute(ctx, params)
	}
}
