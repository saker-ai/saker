package pipeline_test

import (
	"context"
	"testing"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/pipeline"
	runtimecache "github.com/cinience/saker/pkg/runtime/cache"
	"github.com/cinience/saker/pkg/runtime/checkpoint"
	"github.com/cinience/saker/pkg/tool"
)

func newBenchRuntime(b *testing.B, opts ...func(*api.Options)) *api.Runtime {
	b.Helper()
	base := api.Options{
		ProjectRoot:         b.TempDir(),
		Model:               staticModel{},
		EnabledBuiltinTools: []string{},
		CustomTools:         defaultMockTools(),
	}
	for _, opt := range opts {
		opt(&base)
	}
	rt, err := api.New(context.Background(), base)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = rt.Close() })
	return rt
}

func BenchmarkEndToEnd_SingleStep(b *testing.B) {
	rt := newBenchRuntime(b)
	step := pipeline.Step{Name: "echo", Tool: "echo", With: map[string]any{"text": "bench"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := rt.Run(context.Background(), api.Request{Pipeline: &step})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEndToEnd_Batch10Steps(b *testing.B) {
	steps := make([]pipeline.Step, 10)
	for i := range steps {
		steps[i] = pipeline.Step{Name: "echo", Tool: "echo", With: map[string]any{"text": "batch"}}
	}
	rt := newBenchRuntime(b)
	step := pipeline.Step{Batch: &pipeline.Batch{Steps: steps}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := rt.Run(context.Background(), api.Request{Pipeline: &step})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEndToEnd_FanOut50_Conc1(b *testing.B) {
	benchFanOut(b, 50, 1)
}

func BenchmarkEndToEnd_FanOut50_Conc8(b *testing.B) {
	benchFanOut(b, 50, 8)
}

func BenchmarkEndToEnd_FanOut50_Conc50(b *testing.B) {
	benchFanOut(b, 50, 50)
}

func benchFanOut(b *testing.B, items, concurrency int) {
	b.Helper()
	rt := newBenchRuntime(b)
	step := pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{
					Name: "gen",
					Tool: "artifact-gen",
					With: map[string]any{"count": float64(items)},
				},
				{
					FanOut: &pipeline.FanOut{
						Step:        pipeline.Step{Name: "up", Tool: "upper"},
						Concurrency: concurrency,
					},
				},
			},
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := rt.Run(context.Background(), api.Request{Pipeline: &step})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEndToEnd_CacheHit(b *testing.B) {
	cacheStore := runtimecache.NewMemoryStore()
	ct := &counterTool{}
	rt := newBenchRuntime(b, func(o *api.Options) {
		o.CacheStore = cacheStore
		o.CustomTools = []tool.Tool{ct}
	})
	step := pipeline.Step{Name: "cached", Tool: "counter"}
	// Warm up cache
	_, _ = rt.Run(context.Background(), api.Request{Pipeline: &step})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := rt.Run(context.Background(), api.Request{Pipeline: &step})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEndToEnd_CheckpointResume(b *testing.B) {
	store := checkpoint.NewMemoryStore()
	rt := newBenchRuntime(b, func(o *api.Options) {
		o.CheckpointStore = store
	})

	const sid = "bench-session"

	// Create checkpoint
	resp, err := rt.Run(context.Background(), api.Request{
		SessionID: sid,
		Pipeline: &pipeline.Step{
			Checkpoint: &pipeline.Checkpoint{
				Name: "bench-cp",
				Step: pipeline.Step{Name: "echo", Tool: "echo", With: map[string]any{"text": "cp"}},
			},
		},
	})
	if err != nil {
		b.Fatal(err)
	}
	cpID := resp.Result.CheckpointID

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Re-save checkpoint for each iteration since resume deletes it
		_, _ = store.Save(context.Background(), checkpoint.Entry{
			ID:        cpID,
			SessionID: sid,
		})
		_, err := rt.Run(context.Background(), api.Request{
			SessionID:            sid,
			ResumeFromCheckpoint: cpID,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
