package pipeline

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/runtime/cache"
	"github.com/cinience/saker/pkg/tool"
)

func makeArtifacts(n int) Input {
	refs := make([]artifact.ArtifactRef, n)
	for i := range refs {
		refs[i] = artifact.NewGeneratedRef(fmt.Sprintf("art_%d", i), artifact.ArtifactKindImage)
	}
	return Input{
		Collections: map[string][]artifact.ArtifactRef{"items": refs},
	}
}

func slowToolRunner(delay time.Duration) func(context.Context, Step, []artifact.ArtifactRef) (*tool.ToolResult, error) {
	return func(ctx context.Context, step Step, refs []artifact.ArtifactRef) (*tool.ToolResult, error) {
		time.Sleep(delay)
		return &tool.ToolResult{Output: "ok"}, nil
	}
}

func BenchmarkFanOutSerial(b *testing.B) {
	exec := Executor{RunTool: slowToolRunner(1 * time.Millisecond)}
	input := makeArtifacts(50)
	step := Step{FanOut: &FanOut{
		Collection:  "items",
		Concurrency: 1,
		Step:        Step{Name: "work", Tool: "worker"},
	}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := exec.Execute(context.Background(), step, input); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFanOutConcurrent4(b *testing.B) {
	exec := Executor{RunTool: slowToolRunner(1 * time.Millisecond)}
	input := makeArtifacts(50)
	step := Step{FanOut: &FanOut{
		Collection:  "items",
		Concurrency: 4,
		Step:        Step{Name: "work", Tool: "worker"},
	}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := exec.Execute(context.Background(), step, input); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFanOutConcurrent16(b *testing.B) {
	exec := Executor{RunTool: slowToolRunner(1 * time.Millisecond)}
	input := makeArtifacts(50)
	step := Step{FanOut: &FanOut{
		Collection:  "items",
		Concurrency: 16,
		Step:        Step{Name: "work", Tool: "worker"},
	}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := exec.Execute(context.Background(), step, input); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFanOutUnbounded(b *testing.B) {
	exec := Executor{RunTool: slowToolRunner(1 * time.Millisecond)}
	input := makeArtifacts(50)
	step := Step{FanOut: &FanOut{
		Collection: "items",
		Step:       Step{Name: "work", Tool: "worker"},
	}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := exec.Execute(context.Background(), step, input); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBatchSequential10Steps(b *testing.B) {
	exec := Executor{RunTool: slowToolRunner(100 * time.Microsecond)}
	steps := make([]Step, 10)
	for i := range steps {
		steps[i] = Step{Name: fmt.Sprintf("step_%d", i), Tool: "worker"}
	}
	step := Step{Batch: &Batch{Steps: steps}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := exec.Execute(context.Background(), step, Input{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCacheHit(b *testing.B) {
	store := cache.NewMemoryStore()
	exec := Executor{
		Cache: store,
		RunTool: func(ctx context.Context, step Step, refs []artifact.ArtifactRef) (*tool.ToolResult, error) {
			return &tool.ToolResult{Output: "expensive"}, nil
		},
	}
	step := Step{
		Name: "cached-step",
		Tool: "expensive-tool",
		With: map[string]any{"prompt": "describe"},
		Input: []artifact.ArtifactRef{
			artifact.NewGeneratedRef("art_1", artifact.ArtifactKindImage),
		},
	}
	// Warm the cache
	exec.Execute(context.Background(), step, Input{})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := exec.Execute(context.Background(), step, Input{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRetryWithBackoff(b *testing.B) {
	attempt := 0
	exec := Executor{
		RunTool: func(ctx context.Context, step Step, refs []artifact.ArtifactRef) (*tool.ToolResult, error) {
			attempt++
			if attempt%3 != 0 {
				return nil, fmt.Errorf("fail")
			}
			return &tool.ToolResult{Output: "ok"}, nil
		},
	}
	step := Step{Retry: &Retry{
		Attempts:  3,
		BackoffMs: 1, // minimal backoff for benchmark
		Step:      Step{Name: "flaky", Tool: "worker"},
	}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		exec.Execute(context.Background(), step, Input{})
	}
}
