package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/cinience/saker/pkg/artifact"
)

// StreamResult delivers the outcome of processing one stream segment.
type StreamResult struct {
	SegmentIndex int
	Timestamp    time.Duration
	Result       Result
	Dropped      bool // true if this segment was dropped due to backpressure
}

// StreamExecutorConfig configures the streaming pipeline executor.
type StreamExecutorConfig struct {
	// Step is the pipeline step applied to each segment.
	Step Step
	// WindowSize controls how many recent results to retain for context (default: 1).
	WindowSize int
	// Backpressure controls behavior when processing falls behind (default: block).
	Backpressure BackpressurePolicy
	// BufferSize is the channel buffer for pending segments (default: 16).
	BufferSize int
	// SegmentInterval is the expected interval between segments (for timestamp estimation).
	SegmentInterval time.Duration
}

// StreamExecutor runs a pipeline step against each artifact produced by a StreamSource.
type StreamExecutor struct {
	Executor Executor
	Config   StreamExecutorConfig
}

// Run consumes from source, processes each segment through the pipeline step,
// and emits results on the returned channel. The channel is closed when the
// source is exhausted or the context is cancelled.
func (se *StreamExecutor) Run(ctx context.Context, source StreamSource) <-chan StreamResult {
	bufSize := se.Config.BufferSize
	if bufSize <= 0 {
		bufSize = 16
	}
	windowSize := se.Config.WindowSize
	if windowSize <= 0 {
		windowSize = 1
	}

	out := make(chan StreamResult, bufSize)

	go func() {
		defer close(out)

		window := make([]Result, 0, windowSize)
		segIndex := 0

		for {
			ref, err := source.Next(ctx)
			if err != nil {
				if errors.Is(err, io.EOF) || ctx.Err() != nil {
					return
				}
				// Non-fatal: skip segment
				segIndex++
				continue
			}

			// Check backpressure
			if se.Config.Backpressure == BackpressureDropNewest && len(out) >= bufSize {
				select {
				case out <- StreamResult{SegmentIndex: segIndex, Dropped: true}:
				default:
				}
				segIndex++
				continue
			}
			if se.Config.Backpressure == BackpressureDropOldest && len(out) >= bufSize {
				// Drain oldest
				select {
				case <-out:
					slog.Warn("pipeline: stream segment dropped", "reason", "backpressure")
				default:
				}
			}

			// Build input with sliding window context
			input := Input{
				Artifacts: []artifact.ArtifactRef{ref},
			}
			if len(window) > 0 {
				input.Items = make([]Result, len(window))
				copy(input.Items, window)
			}

			result, err := se.Executor.Execute(ctx, se.Config.Step, input)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Warn("pipeline: stream segment processing error", "segment", segIndex, "error", err)
				// Skip failed segment
				segIndex++
				continue
			}

			// Update sliding window
			window = append(window, result)
			if len(window) > windowSize {
				window = window[len(window)-windowSize:]
			}

			ts := time.Duration(segIndex) * se.Config.SegmentInterval

			select {
			case out <- StreamResult{
				SegmentIndex: segIndex,
				Timestamp:    ts,
				Result:       result,
			}:
			case <-ctx.Done():
				return
			}

			segIndex++
		}
	}()

	return out
}
