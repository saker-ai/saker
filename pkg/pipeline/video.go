package pipeline

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/tool"
)

// RunToolFunc is the function signature for dispatching a pipeline step to a tool.
type RunToolFunc = func(context.Context, Step, []artifact.ArtifactRef) (*tool.ToolResult, error)

// RunVideoStream runs a non-interactive video stream pipeline, writing output
// to out. It uses the provided runTool function to execute pipeline steps and
// dispatches to either the frame processor or stream executor depending on
// whether event keywords are provided.
func RunVideoStream(out io.Writer, source string, segDuration time.Duration, windowSize, sampleRate int, events string, timeoutMs int, runTool RunToolFunc) error {
	ctx := context.Background()
	cancel := func() {}
	if timeoutMs > 0 {
		ctxT, c := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		ctx = ctxT
		cancel = c
	}
	defer cancel()

	// Build source
	src := BuildStreamSource(out, source, segDuration, sampleRate)
	defer src.Close()

	// Choose mode: frame processor (with events) or stream executor
	if strings.TrimSpace(events) != "" {
		// Phase 3: frame-level event detection
		var rules []EventRule
		for _, kw := range strings.Split(events, ",") {
			kw = strings.TrimSpace(kw)
			if kw != "" {
				rules = append(rules, NewKeywordEventRule(kw+"_detected", kw, 3*time.Second))
			}
		}
		fp := &FrameProcessor{
			Executor: Executor{RunTool: runTool},
			Config: FrameProcessorConfig{
				Step:          Step{Name: "analyze-frame", Tool: "frame_analyzer"},
				SampleRate:    sampleRate,
				ContextWindow: windowSize,
				EventRules:    rules,
				FrameInterval: segDuration,
				OnEvent: func(ev Event) {
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
		se := &StreamExecutor{
			Executor: Executor{RunTool: runTool},
			Config: StreamExecutorConfig{
				Step:            Step{Name: "analyze-segment", Tool: "frame_analyzer"},
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

// BuildStreamSource creates a StreamSource from a source string. It detects
// directory-watch, go2rtc, or file-based sources and prints a banner line to out.
func BuildStreamSource(out io.Writer, source string, segDuration time.Duration, sampleRate int) StreamSource {
	switch {
	case strings.HasPrefix(source, "watch:"):
		dir := strings.TrimPrefix(source, "watch:")
		fmt.Fprintf(out, "Watching directory: %s\n", dir)
		return NewDirectoryWatchSource(dir, 500*time.Millisecond)
	case IsStreamScheme(source):
		fmt.Fprintf(out, "Streaming via go2rtc: %s (sample rate: 1/%d)\n", source, sampleRate)
		return NewGo2RTCStreamSource(source, Go2RTCSourceOptions{
			SampleRate: sampleRate,
		})
	default:
		fmt.Fprintf(out, "Streaming from file: %s (segment: %s)\n", source, segDuration)
		return NewFileStreamSource(source, segDuration)
	}
}

// fmtStreamDuration formats a duration as MM:SS for display.
func fmtStreamDuration(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// VideoController manages a background video stream processor.
type VideoController struct {
	Cancel    context.CancelFunc
	Done      chan struct{}
	processed int
	skipped   int
	events    int
	mu        sync.Mutex
}

// Stop cancels the background processing and waits for completion.
func (vc *VideoController) Stop() {
	vc.Cancel()
	<-vc.Done
}

// Stats returns the current processing statistics.
func (vc *VideoController) Stats() (processed, skipped, events int) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	return vc.processed, vc.skipped, vc.events
}

// IncProcessed atomically increments the processed count.
func (vc *VideoController) IncProcessed() { vc.mu.Lock(); vc.processed++; vc.mu.Unlock() }

// IncSkipped atomically increments the skipped count.
func (vc *VideoController) IncSkipped() { vc.mu.Lock(); vc.skipped++; vc.mu.Unlock() }

// AddEvents atomically adds n to the event count.
func (vc *VideoController) AddEvents(n int) {
	vc.mu.Lock()
	vc.events += n
	vc.mu.Unlock()
}
