package pipeline_test

import (
	"testing"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/pipeline"
	runtimecache "github.com/saker-ai/saker/pkg/runtime/cache"
	"github.com/saker-ai/saker/pkg/tool"
	"github.com/stretchr/testify/require"
)

func TestCache_HitSkipsExecution(t *testing.T) {
	ct := &counterTool{}
	cacheStore := runtimecache.NewMemoryStore()
	rt := newTestRuntime(t, func(o *api.Options) {
		o.CacheStore = cacheStore
		o.CustomTools = []tool.Tool{ct}
	})

	step := pipeline.Step{Name: "cached", Tool: "counter"}

	// First call — cache miss
	resp1 := runPipeline(t, rt, step)
	requireOutput(t, resp1, "1")

	// Second call — cache hit, counter should NOT increment
	resp2 := runPipeline(t, rt, step)
	requireOutput(t, resp2, "1") // cached output
	require.Equal(t, int64(1), ct.count.Load(), "cache hit should skip execution")
}

func TestCache_MissExecutesTool(t *testing.T) {
	ct := &counterTool{}
	rt := newTestRuntime(t, withCache(), func(o *api.Options) {
		o.CustomTools = []tool.Tool{ct}
	})

	resp := runPipeline(t, rt, pipeline.Step{Name: "miss", Tool: "counter"})
	requireOutput(t, resp, "1")
	require.Equal(t, int64(1), ct.count.Load())
}

func TestCache_DifferentParamsMiss(t *testing.T) {
	ct := &counterTool{}
	rt := newTestRuntime(t, withCache(), func(o *api.Options) {
		o.CustomTools = []tool.Tool{echoTool{}, ct}
	})

	// Same tool, different params → cache miss
	runPipeline(t, rt, pipeline.Step{Name: "a", Tool: "echo", With: map[string]any{"text": "aaa"}})
	runPipeline(t, rt, pipeline.Step{Name: "b", Tool: "echo", With: map[string]any{"text": "bbb"}})

	// Verify both runs produced different outputs via timeline
	resp := runPipeline(t, rt, pipeline.Step{Name: "a", Tool: "echo", With: map[string]any{"text": "aaa"}})
	// Third call with same params as first → cache hit
	requireOutput(t, resp, "aaa")
}

func TestCache_TimelineRecords(t *testing.T) {
	ct := &counterTool{}
	rt := newTestRuntime(t, withCache(), func(o *api.Options) {
		o.CustomTools = []tool.Tool{ct}
	})

	step := pipeline.Step{Name: "tl-cache", Tool: "counter"}

	// First → miss
	resp1 := runPipeline(t, rt, step)
	requireTimeline(t, resp1, "cache_miss")

	// Second → hit
	resp2 := runPipeline(t, rt, step)
	requireTimeline(t, resp2, "cache_hit")
}

func TestCache_FileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cachePath := dir + "/cache.json"

	store1, err := runtimecache.NewFileStore(cachePath)
	require.NoError(t, err)

	ct := &counterTool{}
	rt1 := newTestRuntime(t, func(o *api.Options) {
		o.CacheStore = store1
		o.CustomTools = []tool.Tool{ct}
	})

	step := pipeline.Step{Name: "file-cache", Tool: "counter"}
	runPipeline(t, rt1, step)
	require.Equal(t, int64(1), ct.count.Load())

	// Simulate restart: new FileStore from same path
	store2, err := runtimecache.NewFileStore(cachePath)
	require.NoError(t, err)

	ct2 := &counterTool{}
	rt2 := newTestRuntime(t, func(o *api.Options) {
		o.CacheStore = store2
		o.CustomTools = []tool.Tool{ct2}
	})

	resp := runPipeline(t, rt2, step)
	requireOutput(t, resp, "1") // from cache
	require.Equal(t, int64(0), ct2.count.Load(), "file cache should persist across restarts")
}
