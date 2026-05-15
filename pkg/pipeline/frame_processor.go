package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/saker-ai/saker/pkg/artifact"
)

// Event represents a detected event during frame-level processing.
type Event struct {
	Type       string        `json:"type"`
	Frame      int           `json:"frame"`
	Timestamp  time.Duration `json:"timestamp"`
	Detail     string        `json:"detail"`
	Confidence float64       `json:"confidence,omitempty"`
}

// EventRule defines a condition for detecting events from frame analysis.
type EventRule struct {
	// Name identifies the event type (e.g., "person_enters").
	Name string
	// Match is a function that inspects the frame analysis output and returns
	// whether the event fired. If nil, the rule never fires.
	Match func(frameOutput string) (detail string, confidence float64, fired bool)
	// Cooldown prevents the same rule from firing again within this duration.
	Cooldown time.Duration
}

// FrameProcessorConfig configures the stateful frame processor.
type FrameProcessorConfig struct {
	// Step is the pipeline step applied to each sampled frame.
	Step Step
	// SampleRate processes every Nth frame (default: 1 = every frame).
	SampleRate int
	// ContextWindow retains the last N frame analyses as context (default: 5).
	ContextWindow int
	// EventRules defines the event detection rules.
	EventRules []EventRule
	// OnEvent is called when an event is detected. May be called from a goroutine.
	OnEvent func(Event)
	// FrameInterval is the expected interval between frames (for timestamp estimation).
	FrameInterval time.Duration
	// AudioContext returns the recent audio transcription text to inject into
	// the frame analysis prompt. When non-nil and returning a non-empty string,
	// the transcription is appended to the Step's prompt. This enables the AI
	// model to consider both visual and audio content for event detection.
	AudioContext func() string
}

// FrameResult carries the output of processing a single frame.
type FrameResult struct {
	FrameIndex int
	Timestamp  time.Duration
	Analysis   string
	Events     []Event
	Skipped    bool // true if this frame was skipped due to SampleRate
}

// FrameProcessor runs a pipeline step against each frame from a StreamSource,
// maintaining cross-frame context and detecting events via configurable rules.
type FrameProcessor struct {
	Executor Executor
	Config   FrameProcessorConfig
}

// Run processes frames from the source and emits results. The returned channel
// is closed when the source is exhausted or the context is cancelled.
//
// to inference, error funnel, and back-pressure handling — extracting helpers
// would shuffle complexity without removing it. Legacy hot path.
//
//nolint:gocognit // Frame pipeline coordinator with sample-rate gating, fan-out
func (fp *FrameProcessor) Run(ctx context.Context, source StreamSource) <-chan FrameResult {
	sampleRate := fp.Config.SampleRate
	if sampleRate <= 0 {
		sampleRate = 1
	}
	windowSize := fp.Config.ContextWindow
	if windowSize <= 0 {
		windowSize = 5
	}

	out := make(chan FrameResult, 16)

	go func() {
		defer close(out)

		fctx := &frameContext{
			maxSize: windowSize,
		}
		cooldowns := make(map[string]time.Time)
		frameIndex := 0

		for {
			ref, err := source.Next(ctx)
			if err != nil {
				if errors.Is(err, io.EOF) || ctx.Err() != nil {
					return
				}
				frameIndex++
				continue
			}

			ts := time.Duration(frameIndex) * fp.Config.FrameInterval

			// Apply sample rate
			if frameIndex%sampleRate != 0 {
				select {
				case out <- FrameResult{FrameIndex: frameIndex, Timestamp: ts, Skipped: true}:
				case <-ctx.Done():
					return
				}
				frameIndex++
				continue
			}

			// Build input with context window
			input := Input{
				Artifacts: []artifact.ArtifactRef{ref},
				Items:     fctx.recentResults(),
			}

			// Inject audio transcript into the step prompt if available.
			step := fp.Config.Step
			if fp.Config.AudioContext != nil {
				if transcript := fp.Config.AudioContext(); transcript != "" {
					with := make(map[string]any, len(step.With)+1)
					for k, v := range step.With {
						with[k] = v
					}
					originalPrompt, _ := with["prompt"].(string)
					with["prompt"] = originalPrompt + "\n\n[Audio transcript from recent stream audio]\n" + transcript
					step.With = with
				}
			}

			result, err := fp.Executor.Execute(ctx, step, input)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Warn("pipeline: frame processing error", "frame", frameIndex, "error", err)
				frameIndex++
				continue
			}

			// Update context window
			fctx.push(result)

			// Check event rules
			var events []Event
			now := time.Now()
			for _, rule := range fp.Config.EventRules {
				if rule.Match == nil {
					continue
				}
				// Check cooldown
				if last, ok := cooldowns[rule.Name]; ok && now.Before(last.Add(rule.Cooldown)) {
					continue
				}
				detail, confidence, fired := rule.Match(result.Output)
				if !fired {
					continue
				}
				ev := Event{
					Type:       rule.Name,
					Frame:      frameIndex,
					Timestamp:  ts,
					Detail:     detail,
					Confidence: confidence,
				}
				events = append(events, ev)
				cooldowns[rule.Name] = now
				if fp.Config.OnEvent != nil {
					fp.Config.OnEvent(ev)
				}
			}

			select {
			case out <- FrameResult{
				FrameIndex: frameIndex,
				Timestamp:  ts,
				Analysis:   result.Output,
				Events:     events,
			}:
			case <-ctx.Done():
				return
			}

			frameIndex++
		}
	}()

	return out
}

// frameContext maintains a sliding window of recent frame analysis results.
type frameContext struct {
	mu      sync.Mutex
	results []Result
	maxSize int
}

func (fc *frameContext) push(r Result) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.results = append(fc.results, r)
	if len(fc.results) > fc.maxSize {
		fc.results = fc.results[len(fc.results)-fc.maxSize:]
	}
}

func (fc *frameContext) recentResults() []Result {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.results) == 0 {
		return nil
	}
	out := make([]Result, len(fc.results))
	copy(out, fc.results)
	return out
}

// NewKeywordEventRule creates a simple event rule that fires when the analysis
// output contains the given keyword (case-insensitive matching via contains).
func NewKeywordEventRule(name, keyword string, cooldown time.Duration) EventRule {
	return EventRule{
		Name:     name,
		Cooldown: cooldown,
		Match: func(output string) (string, float64, bool) {
			// Simple substring match
			if strings.Contains(strings.ToLower(output), strings.ToLower(keyword)) {
				return "detected: " + keyword, 1.0, true
			}
			return "", 0, false
		},
	}
}
