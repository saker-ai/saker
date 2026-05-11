package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/clikit"
	modelpkg "github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/pipeline"
	"github.com/cinience/saker/pkg/tool"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
)

func runVideoStream(out io.Writer, source string, segDuration time.Duration, windowSize, sampleRate int, events string, timeoutMs int) error {
	ctx := context.Background()
	cancel := func() {}
	if timeoutMs > 0 {
		ctxT, c := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		ctx = ctxT
		cancel = c
	}
	defer cancel()

	// Build source
	src := buildStreamSource(out, source, segDuration, sampleRate)
	defer src.Close()

	// Build tool runner that uses builtin tools directly
	runTool := buildToolRunner(nil) // no model in non-interactive mode

	// Choose mode: frame processor (with events) or stream executor
	if strings.TrimSpace(events) != "" {
		// Phase 3: frame-level event detection
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
					fmt.Fprintf(out, "  ** EVENT [%s] frame %d: %s\n", ev.Type, ev.Frame, ev.Detail)
				},
			},
		}
		results := fp.Run(ctx, src)
		processed, skipped, evCount := 0, 0, 0
		for r := range results {
			if r.Skipped {
				skipped++
				continue
			}
			processed++
			evCount += len(r.Events)
			marker := " "
			if len(r.Events) > 0 {
				marker = "!"
			}
			fmt.Fprintf(out, "%s frame %3d: %s\n", marker, r.FrameIndex, r.Analysis)
		}
		fmt.Fprintf(out, "\nDone: %d processed, %d skipped, %d events\n", processed, skipped, evCount)
	} else {
		// Phase 2: stream executor
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
		count := 0
		for r := range results {
			count++
			if r.Dropped {
				fmt.Fprintf(out, "[dropped] segment %d\n", r.SegmentIndex)
				continue
			}
			fmt.Fprintf(out, "[%s] segment %d: %s\n", fmtStreamDuration(r.Timestamp), r.SegmentIndex, r.Result.Output)
		}
		fmt.Fprintf(out, "\nStream ended: %d segments processed\n", count)
	}
	return nil
}

func buildStreamSource(out io.Writer, source string, segDuration time.Duration, sampleRate int) pipeline.StreamSource {
	switch {
	case strings.HasPrefix(source, "watch:"):
		dir := strings.TrimPrefix(source, "watch:")
		fmt.Fprintf(out, "Watching directory: %s\n", dir)
		return pipeline.NewDirectoryWatchSource(dir, 500*time.Millisecond)
	case pipeline.IsStreamScheme(source):
		fmt.Fprintf(out, "Streaming via go2rtc: %s (sample rate: 1/%d)\n", source, sampleRate)
		return pipeline.NewGo2RTCStreamSource(source, pipeline.Go2RTCSourceOptions{
			SampleRate: sampleRate,
		})
	default:
		fmt.Fprintf(out, "Streaming from file: %s (segment: %s)\n", source, segDuration)
		return pipeline.NewFileStreamSource(source, segDuration)
	}
}

func fmtStreamDuration(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// videoController manages a background video stream processor.
type videoController struct {
	cancel    context.CancelFunc
	done      chan struct{}
	processed int
	skipped   int
	events    int
	mu        sync.Mutex
}

func (vc *videoController) stop() {
	vc.cancel()
	<-vc.done
}

func (vc *videoController) stats() (processed, skipped, events int) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	return vc.processed, vc.skipped, vc.events
}

func (vc *videoController) incProcessed() { vc.mu.Lock(); vc.processed++; vc.mu.Unlock() }
func (vc *videoController) incSkipped()   { vc.mu.Lock(); vc.skipped++; vc.mu.Unlock() }
func (vc *videoController) addEvents(n int) {
	vc.mu.Lock()
	vc.events += n
	vc.mu.Unlock()
}

// runVideoWithREPL starts video stream processing in background and enters
// the interactive REPL. The user can chat with the agent while events are
// detected and displayed inline. Use /video-status and /video-stop commands.
func runVideoWithREPL(stdout, stderr io.Writer, adapter clikit.ReplEngine,
	source string, segDuration time.Duration, windowSize, sampleRate int,
	events string, timeoutMs int, verbose bool, waterfall, sessionID string) error {

	ctx, cancel := context.WithCancel(context.Background())
	vc := &videoController{
		cancel: cancel,
		done:   make(chan struct{}),
	}

	// Build source
	src := buildStreamSource(stdout, source, segDuration, sampleRate)

	// Build tool runner that uses builtin tools directly
	runTool := buildToolRunner(nil) // model not available in pipeline mode

	// Start background video processing
	go func() {
		defer close(vc.done)
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
					vc.incSkipped()
					continue
				}
				vc.incProcessed()
				vc.addEvents(len(r.Events))
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
				vc.incProcessed()
				if r.Dropped {
					vc.incSkipped()
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
			p, s, e := vc.stats()
			select {
			case <-vc.done:
				fmt.Fprintf(out, "Video stream: stopped (processed: %d, skipped: %d, events: %d)\n", p, s, e)
			default:
				fmt.Fprintf(out, "Video stream: running (processed: %d, skipped: %d, events: %d)\n", p, s, e)
			}
			return true, false
		case "/video-stop":
			select {
			case <-vc.done:
				fmt.Fprintln(out, "Video stream already stopped.")
			default:
				vc.stop()
				p, s, e := vc.stats()
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
	case <-vc.done:
	default:
		vc.stop()
	}

	p, s, e := vc.stats()
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
