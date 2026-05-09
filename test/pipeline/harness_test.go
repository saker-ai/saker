package pipeline_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/pipeline"
	runtimecache "github.com/cinience/saker/pkg/runtime/cache"
	"github.com/cinience/saker/pkg/runtime/checkpoint"
	"github.com/cinience/saker/pkg/tool"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// staticModel — pipeline mode does not call the LLM
// ---------------------------------------------------------------------------

type staticModel struct{}

func (staticModel) Complete(context.Context, model.Request) (*model.Response, error) {
	return &model.Response{Message: model.Message{Role: "assistant", Content: "unused"}}, nil
}

func (staticModel) CompleteStream(context.Context, model.Request, model.StreamHandler) error {
	return nil
}

// ---------------------------------------------------------------------------
// Mock tools
// ---------------------------------------------------------------------------

// echoTool returns params["text"] as output.
type echoTool struct{}

func (echoTool) Name() string             { return "echo" }
func (echoTool) Description() string      { return "echoes params[text]" }
func (echoTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (echoTool) Execute(_ context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	text, _ := params["text"].(string)
	return &tool.ToolResult{Output: text}, nil
}

// upperTool returns the first input artifact ID uppercased.
type upperTool struct{}

func (upperTool) Name() string             { return "upper" }
func (upperTool) Description() string      { return "uppercases first artifact ID" }
func (upperTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (upperTool) Execute(_ context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	arts, _ := params["artifacts"].([]artifact.ArtifactRef)
	id := ""
	if len(arts) > 0 {
		id = arts[0].ArtifactID
	}
	upper := strings.ToUpper(id)
	return &tool.ToolResult{
		Output: upper,
		Artifacts: []artifact.ArtifactRef{
			artifact.NewGeneratedRef("upper-"+id, artifact.ArtifactKindText),
		},
	}, nil
}

// counterTool atomically increments and returns call count.
type counterTool struct {
	count atomic.Int64
}

func (c *counterTool) Name() string             { return "counter" }
func (c *counterTool) Description() string      { return "returns incremented counter" }
func (c *counterTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (c *counterTool) Execute(_ context.Context, _ map[string]interface{}) (*tool.ToolResult, error) {
	n := c.count.Add(1)
	return &tool.ToolResult{Output: fmt.Sprintf("%d", n)}, nil
}

// slowTool sleeps for params["ms"] milliseconds.
type slowTool struct {
	active atomic.Int64
	peak   atomic.Int64
}

func (s *slowTool) Name() string             { return "slow" }
func (s *slowTool) Description() string      { return "sleeps ms milliseconds" }
func (s *slowTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (s *slowTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	cur := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		old := s.peak.Load()
		if cur <= old || s.peak.CompareAndSwap(old, cur) {
			break
		}
	}
	ms := 10 // default
	if v, ok := params["ms"].(float64); ok {
		ms = int(v)
	}
	select {
	case <-time.After(time.Duration(ms) * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &tool.ToolResult{Output: "done"}, nil
}

// failNTool fails the first N calls, then succeeds.
type failNTool struct {
	failCount int
	calls     atomic.Int64
}

func (f *failNTool) Name() string             { return "fail-n" }
func (f *failNTool) Description() string      { return "fails first N calls" }
func (f *failNTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (f *failNTool) Execute(_ context.Context, _ map[string]interface{}) (*tool.ToolResult, error) {
	n := f.calls.Add(1)
	if int(n) <= f.failCount {
		return nil, fmt.Errorf("fail-n: attempt %d of %d", n, f.failCount)
	}
	return &tool.ToolResult{Output: fmt.Sprintf("ok-after-%d-failures", f.failCount)}, nil
}

// failAlwaysTool always returns an error.
type failAlwaysTool struct{}

func (failAlwaysTool) Name() string             { return "fail-always" }
func (failAlwaysTool) Description() string      { return "always fails" }
func (failAlwaysTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (failAlwaysTool) Execute(_ context.Context, _ map[string]interface{}) (*tool.ToolResult, error) {
	return nil, fmt.Errorf("fail-always: permanent error")
}

// artifactGenTool produces N artifacts based on params["count"].
type artifactGenTool struct{}

func (artifactGenTool) Name() string             { return "artifact-gen" }
func (artifactGenTool) Description() string      { return "generates N artifacts" }
func (artifactGenTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (artifactGenTool) Execute(_ context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	count := 1
	if v, ok := params["count"].(float64); ok {
		count = int(v)
	}
	arts := make([]artifact.ArtifactRef, count)
	for i := range arts {
		arts[i] = artifact.NewGeneratedRef(fmt.Sprintf("gen-%d", i), artifact.ArtifactKindText)
	}
	return &tool.ToolResult{Output: fmt.Sprintf("generated %d artifacts", count), Artifacts: arts}, nil
}

// collectTool records all received artifacts to a shared slice.
type collectTool struct {
	mu        sync.Mutex
	collected []artifact.ArtifactRef
}

func (c *collectTool) Name() string             { return "collect" }
func (c *collectTool) Description() string      { return "collects artifacts" }
func (c *collectTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (c *collectTool) Execute(_ context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	arts, _ := params["artifacts"].([]artifact.ArtifactRef)
	c.mu.Lock()
	c.collected = append(c.collected, arts...)
	c.mu.Unlock()
	return &tool.ToolResult{Output: fmt.Sprintf("collected %d", len(arts))}, nil
}

// ---------------------------------------------------------------------------
// newTestRuntime creates a Runtime with mock tools for pipeline testing.
// ---------------------------------------------------------------------------

func newTestRuntime(t *testing.T, opts ...func(*api.Options)) *api.Runtime {
	t.Helper()
	base := api.Options{
		ProjectRoot:         t.TempDir(),
		Model:               staticModel{},
		EnabledBuiltinTools: []string{}, // disable all built-ins
		CustomTools:         defaultMockTools(),
	}
	for _, opt := range opts {
		opt(&base)
	}
	rt, err := api.New(context.Background(), base)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func defaultMockTools() []tool.Tool {
	return []tool.Tool{
		echoTool{},
		upperTool{},
		&counterTool{},
		&slowTool{},
		failAlwaysTool{},
		artifactGenTool{},
	}
}

// ---------------------------------------------------------------------------
// Option helpers
// ---------------------------------------------------------------------------

func withCache() func(*api.Options) {
	return func(o *api.Options) {
		o.CacheStore = runtimecache.NewMemoryStore()
	}
}

func withCheckpoint() func(*api.Options) {
	return func(o *api.Options) {
		o.CheckpointStore = checkpoint.NewMemoryStore()
	}
}

func withFileCache(t *testing.T) func(*api.Options) {
	t.Helper()
	return func(o *api.Options) {
		store, err := runtimecache.NewFileStore(t.TempDir() + "/cache.json")
		require.NoError(t, err)
		o.CacheStore = store
	}
}

func withFileCheckpoint(t *testing.T) func(*api.Options) {
	t.Helper()
	return func(o *api.Options) {
		store, err := checkpoint.NewFileStore(t.TempDir() + "/checkpoint.json")
		require.NoError(t, err)
		o.CheckpointStore = store
	}
}

func withTools(tools ...tool.Tool) func(*api.Options) {
	return func(o *api.Options) {
		o.CustomTools = tools
	}
}

// ---------------------------------------------------------------------------
// Assertion helpers
// ---------------------------------------------------------------------------

func requireTimeline(t *testing.T, resp *api.Response, kinds ...string) {
	t.Helper()
	found := map[string]bool{}
	for _, e := range resp.Timeline {
		found[e.Kind] = true
	}
	for _, k := range kinds {
		require.True(t, found[k], "timeline missing kind %q; got %v", k, timelineKinds(resp))
	}
}

func requireArtifacts(t *testing.T, resp *api.Response, count int) {
	t.Helper()
	require.NotNil(t, resp.Result)
	require.Len(t, resp.Result.Artifacts, count, "expected %d artifacts, got %d", count, len(resp.Result.Artifacts))
}

func requireOutput(t *testing.T, resp *api.Response, expected string) {
	t.Helper()
	require.NotNil(t, resp.Result)
	require.Equal(t, expected, resp.Result.Output)
}

func requireCheckpoint(t *testing.T, resp *api.Response) {
	t.Helper()
	require.NotNil(t, resp.Result)
	require.NotEmpty(t, resp.Result.CheckpointID, "expected checkpoint ID")
}

func requireNotInterrupted(t *testing.T, resp *api.Response) {
	t.Helper()
	require.NotNil(t, resp.Result)
	require.False(t, resp.Result.Interrupted, "expected non-interrupted response")
}

func timelineKinds(resp *api.Response) []string {
	kinds := make([]string, len(resp.Timeline))
	for i, e := range resp.Timeline {
		kinds[i] = e.Kind
	}
	return kinds
}

func runPipeline(t *testing.T, rt *api.Runtime, step pipeline.Step) *api.Response {
	t.Helper()
	resp, err := rt.Run(context.Background(), api.Request{Pipeline: &step})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Result)
	return resp
}

func runPipelineErr(t *testing.T, rt *api.Runtime, step pipeline.Step) error {
	t.Helper()
	_, err := rt.Run(context.Background(), api.Request{Pipeline: &step})
	return err
}

// Convenience request constructors for test readability.

func api_request_pipeline(step pipeline.Step) api.Request {
	return api.Request{Pipeline: &step}
}

func api_request_resume(checkpointID string) api.Request {
	return api.Request{ResumeFromCheckpoint: checkpointID}
}

func api_request_with_collection(name string, refs []artifact.ArtifactRef, step pipeline.Step) api.Request {
	// Collections are not directly settable via api.Request — they come from
	// pipeline.Input which is built internally. Instead, produce the artifacts
	// via a batch that generates them first, then fan-outs over them.
	return api.Request{
		Pipeline: &pipeline.Step{
			Batch: &pipeline.Batch{
				Steps: []pipeline.Step{
					{
						Name: "gen-collection",
						Tool: "artifact-gen",
						With: map[string]any{"count": float64(len(refs))},
					},
					step,
				},
			},
		},
	}
}
