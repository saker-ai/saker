package pipeline_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/pipeline"
	"github.com/saker-ai/saker/pkg/tool"
)

func TestFrameProcessorBasic(t *testing.T) {
	refs := make([]artifact.ArtifactRef, 6)
	for i := range refs {
		refs[i] = artifact.NewGeneratedRef(fmt.Sprintf("frame_%d", i), artifact.ArtifactKindImage)
	}
	src := pipeline.NewSliceSource(refs)

	fp := &pipeline.FrameProcessor{
		Executor: pipeline.Executor{
			RunTool: func(ctx context.Context, step pipeline.Step, inputRefs []artifact.ArtifactRef) (*tool.ToolResult, error) {
				id := "unknown"
				if len(inputRefs) > 0 {
					id = inputRefs[0].ArtifactID
				}
				return &tool.ToolResult{Output: "scene with " + id}, nil
			},
		},
		Config: pipeline.FrameProcessorConfig{
			Step:          pipeline.Step{Name: "analyze", Tool: "analyzer"},
			SampleRate:    1,
			ContextWindow: 3,
			FrameInterval: 100 * time.Millisecond,
		},
	}

	results := fp.Run(context.Background(), src)
	var collected []pipeline.FrameResult
	for r := range results {
		collected = append(collected, r)
	}

	if len(collected) != 6 {
		t.Fatalf("expected 6 results, got %d", len(collected))
	}
	for i, r := range collected {
		if r.FrameIndex != i {
			t.Errorf("result %d: frame index %d", i, r.FrameIndex)
		}
		if r.Skipped {
			t.Errorf("result %d: unexpectedly skipped", i)
		}
		expected := fmt.Sprintf("scene with frame_%d", i)
		if r.Analysis != expected {
			t.Errorf("result %d: %q, want %q", i, r.Analysis, expected)
		}
	}

	// Check timestamp
	if collected[3].Timestamp != 300*time.Millisecond {
		t.Errorf("timestamp for frame 3: got %v, want 300ms", collected[3].Timestamp)
	}
}

func TestFrameProcessorSampleRate(t *testing.T) {
	refs := make([]artifact.ArtifactRef, 10)
	for i := range refs {
		refs[i] = artifact.NewGeneratedRef(fmt.Sprintf("f%d", i), artifact.ArtifactKindImage)
	}
	src := pipeline.NewSliceSource(refs)

	var analyzed []int
	fp := &pipeline.FrameProcessor{
		Executor: pipeline.Executor{
			RunTool: func(ctx context.Context, step pipeline.Step, inputRefs []artifact.ArtifactRef) (*tool.ToolResult, error) {
				return &tool.ToolResult{Output: "ok"}, nil
			},
		},
		Config: pipeline.FrameProcessorConfig{
			Step:       pipeline.Step{Name: "a", Tool: "a"},
			SampleRate: 3, // process every 3rd frame: 0, 3, 6, 9
		},
	}

	results := fp.Run(context.Background(), src)
	var skipped, processed int
	for r := range results {
		if r.Skipped {
			skipped++
		} else {
			processed++
			analyzed = append(analyzed, r.FrameIndex)
		}
	}

	if processed != 4 {
		t.Fatalf("expected 4 processed frames, got %d (analyzed: %v)", processed, analyzed)
	}
	if skipped != 6 {
		t.Fatalf("expected 6 skipped frames, got %d", skipped)
	}

	// Should process frames 0, 3, 6, 9
	want := []int{0, 3, 6, 9}
	for i, idx := range want {
		if i >= len(analyzed) || analyzed[i] != idx {
			t.Errorf("analyzed[%d] = %v, want %d", i, analyzed, idx)
			break
		}
	}
}

func TestFrameProcessorEventDetection(t *testing.T) {
	refs := make([]artifact.ArtifactRef, 5)
	for i := range refs {
		refs[i] = artifact.NewGeneratedRef(fmt.Sprintf("f%d", i), artifact.ArtifactKindImage)
	}
	src := pipeline.NewSliceSource(refs)

	// Frame 2 and 4 will mention "fire"
	fp := &pipeline.FrameProcessor{
		Executor: pipeline.Executor{
			RunTool: func(ctx context.Context, step pipeline.Step, inputRefs []artifact.ArtifactRef) (*tool.ToolResult, error) {
				id := 0
				if len(inputRefs) > 0 {
					fmt.Sscanf(inputRefs[0].ArtifactID, "f%d", &id)
				}
				output := "normal scene"
				if id == 2 || id == 4 {
					output = "fire detected in building"
				}
				return &tool.ToolResult{Output: output}, nil
			},
		},
		Config: pipeline.FrameProcessorConfig{
			Step: pipeline.Step{Name: "a", Tool: "a"},
			EventRules: []pipeline.EventRule{
				pipeline.NewKeywordEventRule("fire_alarm", "fire", 0),
			},
		},
	}

	results := fp.Run(context.Background(), src)
	var events []pipeline.Event
	for r := range results {
		events = append(events, r.Events...)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 fire events, got %d: %+v", len(events), events)
	}
	if events[0].Frame != 2 {
		t.Errorf("first event at frame %d, want 2", events[0].Frame)
	}
	if events[1].Frame != 4 {
		t.Errorf("second event at frame %d, want 4", events[1].Frame)
	}
}

func TestFrameProcessorEventCooldown(t *testing.T) {
	refs := make([]artifact.ArtifactRef, 5)
	for i := range refs {
		refs[i] = artifact.NewGeneratedRef(fmt.Sprintf("f%d", i), artifact.ArtifactKindImage)
	}
	src := pipeline.NewSliceSource(refs)

	// All frames mention "alert" but cooldown should suppress duplicates
	fp := &pipeline.FrameProcessor{
		Executor: pipeline.Executor{
			RunTool: func(ctx context.Context, step pipeline.Step, inputRefs []artifact.ArtifactRef) (*tool.ToolResult, error) {
				return &tool.ToolResult{Output: "alert condition active"}, nil
			},
		},
		Config: pipeline.FrameProcessorConfig{
			Step: pipeline.Step{Name: "a", Tool: "a"},
			EventRules: []pipeline.EventRule{
				pipeline.NewKeywordEventRule("alert", "alert", 1*time.Hour), // long cooldown
			},
		},
	}

	results := fp.Run(context.Background(), src)
	var events []pipeline.Event
	for r := range results {
		events = append(events, r.Events...)
	}

	// Only 1 event due to cooldown
	if len(events) != 1 {
		t.Fatalf("expected 1 event (cooldown), got %d", len(events))
	}
}

func TestFrameProcessorOnEventCallback(t *testing.T) {
	refs := make([]artifact.ArtifactRef, 3)
	for i := range refs {
		refs[i] = artifact.NewGeneratedRef(fmt.Sprintf("f%d", i), artifact.ArtifactKindImage)
	}
	src := pipeline.NewSliceSource(refs)

	var mu sync.Mutex
	var callbackEvents []pipeline.Event

	fp := &pipeline.FrameProcessor{
		Executor: pipeline.Executor{
			RunTool: func(ctx context.Context, step pipeline.Step, inputRefs []artifact.ArtifactRef) (*tool.ToolResult, error) {
				return &tool.ToolResult{Output: "person entering zone"}, nil
			},
		},
		Config: pipeline.FrameProcessorConfig{
			Step: pipeline.Step{Name: "a", Tool: "a"},
			EventRules: []pipeline.EventRule{
				pipeline.NewKeywordEventRule("person_enters", "person", 0),
			},
			OnEvent: func(ev pipeline.Event) {
				mu.Lock()
				callbackEvents = append(callbackEvents, ev)
				mu.Unlock()
			},
		},
	}

	results := fp.Run(context.Background(), src)
	for range results {
	}

	mu.Lock()
	defer mu.Unlock()
	if len(callbackEvents) != 3 {
		t.Fatalf("expected 3 callback events, got %d", len(callbackEvents))
	}
}

func TestFrameProcessorCancellation(t *testing.T) {
	refs := make([]artifact.ArtifactRef, 1000)
	for i := range refs {
		refs[i] = artifact.NewGeneratedRef(fmt.Sprintf("f%d", i), artifact.ArtifactKindImage)
	}
	src := pipeline.NewSliceSource(refs)

	fp := &pipeline.FrameProcessor{
		Executor: pipeline.Executor{
			RunTool: func(ctx context.Context, step pipeline.Step, inputRefs []artifact.ArtifactRef) (*tool.ToolResult, error) {
				select {
				case <-time.After(10 * time.Millisecond):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return &tool.ToolResult{Output: "ok"}, nil
			},
		},
		Config: pipeline.FrameProcessorConfig{
			Step: pipeline.Step{Name: "a", Tool: "a"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	results := fp.Run(ctx, src)
	var count int
	for range results {
		count++
	}

	if count >= 1000 {
		t.Error("expected cancellation to stop early")
	}
}

func TestFrameProcessorAudioContext(t *testing.T) {
	refs := make([]artifact.ArtifactRef, 3)
	for i := range refs {
		refs[i] = artifact.NewGeneratedRef(fmt.Sprintf("f%d", i), artifact.ArtifactKindImage)
	}
	src := pipeline.NewSliceSource(refs)

	// Track what prompts are passed to the executor.
	var mu sync.Mutex
	var capturedPrompts []string

	fp := &pipeline.FrameProcessor{
		Executor: pipeline.Executor{
			RunTool: func(ctx context.Context, step pipeline.Step, inputRefs []artifact.ArtifactRef) (*tool.ToolResult, error) {
				prompt, _ := step.With["prompt"].(string)
				mu.Lock()
				capturedPrompts = append(capturedPrompts, prompt)
				mu.Unlock()
				return &tool.ToolResult{Output: "ok"}, nil
			},
		},
		Config: pipeline.FrameProcessorConfig{
			Step: pipeline.Step{
				Name: "analyze",
				Tool: "analyzer",
				With: map[string]any{"prompt": "Describe the frame."},
			},
			AudioContext: func() string {
				return "[0s] Someone says hello\n[5s] Background music playing"
			},
		},
	}

	results := fp.Run(context.Background(), src)
	for range results {
	}

	mu.Lock()
	defer mu.Unlock()

	if len(capturedPrompts) != 3 {
		t.Fatalf("expected 3 prompts, got %d", len(capturedPrompts))
	}
	for i, prompt := range capturedPrompts {
		if !strings.Contains(prompt, "Describe the frame.") {
			t.Errorf("prompt %d: missing original prompt", i)
		}
		if !strings.Contains(prompt, "[Audio transcript from recent stream audio]") {
			t.Errorf("prompt %d: missing audio transcript header", i)
		}
		if !strings.Contains(prompt, "Someone says hello") {
			t.Errorf("prompt %d: missing audio content", i)
		}
	}
}

func TestFrameProcessorAudioContextEmpty(t *testing.T) {
	refs := make([]artifact.ArtifactRef, 2)
	for i := range refs {
		refs[i] = artifact.NewGeneratedRef(fmt.Sprintf("f%d", i), artifact.ArtifactKindImage)
	}
	src := pipeline.NewSliceSource(refs)

	var mu sync.Mutex
	var capturedPrompts []string

	fp := &pipeline.FrameProcessor{
		Executor: pipeline.Executor{
			RunTool: func(ctx context.Context, step pipeline.Step, inputRefs []artifact.ArtifactRef) (*tool.ToolResult, error) {
				prompt, _ := step.With["prompt"].(string)
				mu.Lock()
				capturedPrompts = append(capturedPrompts, prompt)
				mu.Unlock()
				return &tool.ToolResult{Output: "ok"}, nil
			},
		},
		Config: pipeline.FrameProcessorConfig{
			Step: pipeline.Step{
				Name: "analyze",
				Tool: "analyzer",
				With: map[string]any{"prompt": "Describe the frame."},
			},
			// AudioContext returns empty string — should not modify prompt.
			AudioContext: func() string { return "" },
		},
	}

	results := fp.Run(context.Background(), src)
	for range results {
	}

	mu.Lock()
	defer mu.Unlock()

	for i, prompt := range capturedPrompts {
		if strings.Contains(prompt, "[Audio transcript") {
			t.Errorf("prompt %d: should not contain audio header when transcript is empty", i)
		}
		if prompt != "Describe the frame." {
			t.Errorf("prompt %d: expected unmodified prompt, got %q", i, prompt)
		}
	}
}

func TestNewKeywordEventRule(t *testing.T) {
	rule := pipeline.NewKeywordEventRule("test", "Fire", 0)

	// Case-insensitive match
	detail, conf, fired := rule.Match("there is a fire in the building")
	if !fired {
		t.Fatal("expected rule to fire")
	}
	if conf != 1.0 {
		t.Errorf("confidence: got %f, want 1.0", conf)
	}
	if detail == "" {
		t.Error("expected non-empty detail")
	}

	// No match
	_, _, fired = rule.Match("everything is normal")
	if fired {
		t.Error("rule should not fire on non-matching output")
	}
}
