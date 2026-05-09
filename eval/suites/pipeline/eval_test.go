package pipeline_eval

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cinience/saker/eval"
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
// Mock model & tools (reuse pattern from test/pipeline/harness_test.go)
// ---------------------------------------------------------------------------

type staticModel struct{}

func (staticModel) Complete(context.Context, model.Request) (*model.Response, error) {
	return &model.Response{Message: model.Message{Role: "assistant", Content: "unused"}}, nil
}

func (staticModel) CompleteStream(context.Context, model.Request, model.StreamHandler) error {
	return nil
}

type echoTool struct{}

func (echoTool) Name() string             { return "echo" }
func (echoTool) Description() string      { return "echoes params[text]" }
func (echoTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (echoTool) Execute(_ context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	text, _ := params["text"].(string)
	return &tool.ToolResult{Output: text}, nil
}

// orderTool records the order of invocations.
type orderTool struct {
	mu    sync.Mutex
	calls []string
}

func (o *orderTool) Name() string             { return "order" }
func (o *orderTool) Description() string      { return "records call order" }
func (o *orderTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (o *orderTool) Execute(_ context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	label, _ := params["label"].(string)
	o.mu.Lock()
	o.calls = append(o.calls, label)
	o.mu.Unlock()
	return &tool.ToolResult{Output: label}, nil
}

func (o *orderTool) getCalls() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	cp := make([]string, len(o.calls))
	copy(cp, o.calls)
	return cp
}

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

type failAlwaysTool struct{}

func (failAlwaysTool) Name() string             { return "fail-always" }
func (failAlwaysTool) Description() string      { return "always fails" }
func (failAlwaysTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (failAlwaysTool) Execute(_ context.Context, _ map[string]interface{}) (*tool.ToolResult, error) {
	return nil, fmt.Errorf("permanent error")
}

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
	return &tool.ToolResult{Output: fmt.Sprintf("generated %d", count), Artifacts: arts}, nil
}

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
	return &tool.ToolResult{
		Output:    strings.ToUpper(id),
		Artifacts: []artifact.ArtifactRef{artifact.NewGeneratedRef("upper-"+id, artifact.ArtifactKindText)},
	}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestRuntime(t *testing.T, opts ...func(*api.Options)) *api.Runtime {
	t.Helper()
	base := api.Options{
		ProjectRoot:         t.TempDir(),
		Model:               staticModel{},
		EnabledBuiltinTools: []string{},
		CustomTools: []tool.Tool{
			echoTool{}, upperTool{}, artifactGenTool{},
			failAlwaysTool{},
		},
	}
	for _, opt := range opts {
		opt(&base)
	}
	rt, err := api.New(context.Background(), base)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func runPipeline(t *testing.T, rt *api.Runtime, step pipeline.Step) *api.Response {
	t.Helper()
	resp, err := rt.Run(context.Background(), api.Request{Pipeline: &step})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Result)
	return resp
}

// ---------------------------------------------------------------------------
// Eval tests
// ---------------------------------------------------------------------------

func TestEval_BatchOrdering(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "pipeline_batch_ordering"}

	ot := &orderTool{}
	rt := newTestRuntime(t, func(o *api.Options) {
		o.CustomTools = append(o.CustomTools, ot)
	})

	steps := make([]pipeline.Step, 5)
	for i := range steps {
		steps[i] = pipeline.Step{
			Name: fmt.Sprintf("step-%d", i),
			Tool: "order",
			With: map[string]any{"label": fmt.Sprintf("s%d", i)},
		}
	}

	runPipeline(t, rt, pipeline.Step{Batch: &pipeline.Batch{Steps: steps}})

	calls := ot.getCalls()
	pass := len(calls) == 5
	if pass {
		for i, c := range calls {
			if c != fmt.Sprintf("s%d", i) {
				pass = false
				break
			}
		}
	}

	score := 1.0
	if !pass {
		score = 0.0
		t.Errorf("batch order: got %v", calls)
	}

	suite.Add(eval.EvalResult{Name: "five_steps_sequential", Pass: pass, Score: score})
	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func TestEval_FanOutDistribution(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "pipeline_fanout"}

	rt := newTestRuntime(t)

	step := pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{Name: "gen", Tool: "artifact-gen", With: map[string]any{"count": float64(5)}},
				{FanOut: &pipeline.FanOut{
					Step:        pipeline.Step{Name: "up", Tool: "upper"},
					Concurrency: 2,
				}},
			},
		},
	}

	resp := runPipeline(t, rt, step)
	artCount := len(resp.Result.Artifacts)
	pass := artCount == 5
	score := 1.0
	if !pass {
		score = 0.0
		t.Errorf("fan-out: expected 5 artifacts, got %d", artCount)
	}

	suite.Add(eval.EvalResult{
		Name:     "fanout_5_items_conc2",
		Pass:     pass,
		Score:    score,
		Expected: "5",
		Got:      fmt.Sprintf("%d", artCount),
	})
	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func TestEval_RetrySemantics(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "pipeline_retry"}

	t.Run("retry_succeeds_after_failures", func(t *testing.T) {
		t.Parallel()
		fn := &failNTool{failCount: 2}
		rt := newTestRuntime(t, func(o *api.Options) {
			o.CustomTools = append(o.CustomTools, fn)
		})

		step := pipeline.Step{
			Retry: &pipeline.Retry{
				Attempts:  3,
				BackoffMs: 1,
				Step:      pipeline.Step{Name: "flaky", Tool: "fail-n"},
			},
		}

		resp := runPipeline(t, rt, step)
		pass := strings.Contains(resp.Result.Output, "ok-after-2-failures")
		score := 1.0
		if !pass {
			score = 0.0
			t.Errorf("retry: expected success after 2 failures, got %q", resp.Result.Output)
		}

		suite.Add(eval.EvalResult{Name: "retry_3_attempts_2_fail", Pass: pass, Score: score})
	})

	t.Run("retry_exhausted_returns_error", func(t *testing.T) {
		t.Parallel()
		rt := newTestRuntime(t)

		step := pipeline.Step{
			Retry: &pipeline.Retry{
				Attempts:  2,
				BackoffMs: 1,
				Step:      pipeline.Step{Name: "always-fail", Tool: "fail-always"},
			},
		}

		_, err := rt.Run(context.Background(), api.Request{Pipeline: &step})
		pass := err != nil
		score := 1.0
		if !pass {
			score = 0.0
			t.Errorf("retry exhausted: expected error, got nil")
		}

		suite.Add(eval.EvalResult{Name: "retry_exhausted_error", Pass: pass, Score: score})
	})

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func TestEval_CacheCorrectness(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "pipeline_cache"}

	ct := &counterTool{}
	store := runtimecache.NewMemoryStore()
	rt := newTestRuntime(t, func(o *api.Options) {
		o.CustomTools = append(o.CustomTools, ct)
		o.CacheStore = store
	})

	step := pipeline.Step{Name: "cached", Tool: "counter"}

	// First call: should execute.
	resp1 := runPipeline(t, rt, step)
	// Second call: should hit cache.
	resp2 := runPipeline(t, rt, step)

	pass := resp1.Result.Output == resp2.Result.Output && ct.count.Load() == 1
	score := 1.0
	if !pass {
		score = 0.0
		t.Errorf("cache: first=%q, second=%q, executions=%d",
			resp1.Result.Output, resp2.Result.Output, ct.count.Load())
	}

	suite.Add(eval.EvalResult{
		Name:  "cache_hit_returns_same_result",
		Pass:  pass,
		Score: score,
		Details: map[string]any{
			"executions": ct.count.Load(),
		},
	})
	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func TestEval_CheckpointResume(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "pipeline_checkpoint"}

	store := checkpoint.NewMemoryStore()
	rt := newTestRuntime(t, func(o *api.Options) {
		o.CheckpointStore = store
	})

	const sid = "eval-checkpoint"

	// Create checkpoint.
	resp, err := rt.Run(context.Background(), api.Request{
		SessionID: sid,
		Pipeline: &pipeline.Step{
			Checkpoint: &pipeline.Checkpoint{
				Name: "cp1",
				Step: pipeline.Step{Name: "echo", Tool: "echo", With: map[string]any{"text": "checkpoint-data"}},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Result)

	cpID := resp.Result.CheckpointID
	hasCheckpoint := cpID != ""

	pass := hasCheckpoint
	score := 1.0
	if !pass {
		score = 0.0
		t.Errorf("checkpoint: no checkpoint ID returned")
	}

	suite.Add(eval.EvalResult{
		Name:  "checkpoint_created",
		Pass:  pass,
		Score: score,
	})
	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func TestEval_ErrorPropagation(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "pipeline_error_propagation"}

	rt := newTestRuntime(t)

	// Batch with a failing step should propagate error.
	step := pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{Name: "ok", Tool: "echo", With: map[string]any{"text": "fine"}},
				{Name: "bad", Tool: "fail-always"},
			},
		},
	}

	_, err := rt.Run(context.Background(), api.Request{Pipeline: &step})
	pass := err != nil
	score := 1.0
	if !pass {
		score = 0.0
		t.Errorf("error propagation: expected error from batch, got nil")
	}

	suite.Add(eval.EvalResult{
		Name:  "batch_error_propagates",
		Pass:  pass,
		Score: score,
	})
	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func TestEval_SingleStep(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "pipeline_single_step"}

	rt := newTestRuntime(t)
	step := pipeline.Step{Name: "echo", Tool: "echo", With: map[string]any{"text": "hello"}}
	resp := runPipeline(t, rt, step)

	pass := resp.Result.Output == "hello"
	score := 1.0
	if !pass {
		score = 0.0
		t.Errorf("single step: expected 'hello', got %q", resp.Result.Output)
	}

	suite.Add(eval.EvalResult{
		Name:     "echo_hello",
		Pass:     pass,
		Score:    score,
		Expected: "hello",
		Got:      resp.Result.Output,
	})
	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}
