package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/canvas"
	"github.com/cinience/saker/pkg/tool"
)

// fakeCanvasRuntime is a tiny stand-in for canvas.Runtime so we can construct
// an Executor in tests without standing up the real api.Runtime + media cache.
type fakeCanvasRuntime struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeCanvasRuntime) ExecuteTool(_ context.Context, name string, _ map[string]any) (*tool.ToolResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
	return &tool.ToolResult{
		Success: true,
		Output:  "ok",
		Structured: map[string]any{
			"media_url":  "https://r/x.png",
			"media_type": "image",
		},
	}, nil
}

func (f *fakeCanvasRuntime) Run(_ context.Context, _ api.Request) (*api.Response, error) {
	return &api.Response{Result: &api.Result{Output: "ok"}}, nil
}

func (f *fakeCanvasRuntime) ProjectRoot() string { return "" }

func (f *fakeCanvasRuntime) CacheMedia(_ context.Context, rawURL, _ string) (string, string, error) {
	return "/local" + rawURL, rawURL, nil
}

// newCanvasTestHandler builds the minimum Handler shape needed by the canvas
// JSON-RPC handlers. The executor field is pre-populated so that
// ensureCanvasExecutor short-circuits on the lock and never tries to dereference
// the (intentionally nil) runtime.
func newCanvasTestHandler(t *testing.T) (*Handler, *fakeCanvasRuntime) {
	t.Helper()
	dir := t.TempDir()
	rt := &fakeCanvasRuntime{}
	h := &Handler{dataDir: dir}
	h.canvasExecutor = &canvas.Executor{
		Runtime:        rt,
		DataDir:        dir,
		Tracker:        canvas.NewRunTracker(),
		PerNodeTimeout: 5 * time.Second,
		SaveInterval:   time.Millisecond,
	}
	t.Cleanup(func() { h.canvasExecutor.Tracker.Stop() })
	return h, rt
}

func writeCanvasDoc(t *testing.T, dataDir, threadID string, doc *canvas.Document) {
	t.Helper()
	if err := canvas.Save(dataDir, threadID, doc); err != nil {
		t.Fatalf("save canvas: %v", err)
	}
}

// waitTerminal blocks until the run reaches a non-running status or fails the
// test. Required because the executor's background goroutine writes to disk
// and the t.TempDir cleanup races with that goroutine otherwise.
func waitTerminal(t *testing.T, exec *canvas.Executor, runID string) *canvas.RunSummary {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s, ok := exec.Tracker.Get(runID); ok && s.Status != canvas.RunStatusRunning {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s never reached terminal status", runID)
	return nil
}

func TestHandleCanvasExecuteRejectsMissingThreadID(t *testing.T) {
	t.Parallel()
	h, _ := newCanvasTestHandler(t)
	resp := h.handleCanvasExecute(context.Background(), Request{
		ID:     "1",
		Method: "canvas/execute",
		Params: map[string]any{},
	})
	if resp.Error == nil {
		t.Fatalf("expected error response for missing threadId, got %+v", resp)
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected InvalidParams, got %d", resp.Error.Code)
	}
}

func TestHandleCanvasExecuteHappyPathReturnsRunID(t *testing.T) {
	t.Parallel()
	h, _ := newCanvasTestHandler(t)
	writeCanvasDoc(t, h.dataDir, "t1", &canvas.Document{
		Nodes: []*canvas.Node{
			{ID: "g", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}},
		},
	})

	resp := h.handleCanvasExecute(context.Background(), Request{
		ID:     "1",
		Method: "canvas/execute",
		Params: map[string]any{"threadId": "t1"},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	out, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %+v", resp.Result)
	}
	runID, _ := out["runId"].(string)
	if runID == "" {
		t.Fatalf("missing runId: %+v", out)
	}
	if out["status"] != canvas.RunStatusRunning {
		t.Fatalf("status: %+v", out)
	}

	waitTerminal(t, h.canvasExecutor, runID)
}

func TestHandleCanvasExecuteParsesNodeIDsAndSkipDone(t *testing.T) {
	t.Parallel()
	h, _ := newCanvasTestHandler(t)
	writeCanvasDoc(t, h.dataDir, "t1", &canvas.Document{
		Nodes: []*canvas.Node{
			{ID: "a", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}},
			{ID: "b", Data: map[string]any{"nodeType": "imageGen", "prompt": "y"}},
		},
	})
	resp := h.handleCanvasExecute(context.Background(), Request{
		ID:     "1",
		Method: "canvas/execute",
		Params: map[string]any{
			"threadId": "t1",
			"nodeIds":  []any{"b"},
			"skipDone": true,
		},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	runID := resp.Result.(map[string]any)["runId"].(string)
	s := waitTerminal(t, h.canvasExecutor, runID)
	if s.Total != 1 || s.Succeeded != 1 {
		t.Fatalf("subset summary: %+v", s)
	}
}

func TestHandleCanvasRunStatusReturnsSummary(t *testing.T) {
	t.Parallel()
	h, _ := newCanvasTestHandler(t)
	writeCanvasDoc(t, h.dataDir, "t1", &canvas.Document{
		Nodes: []*canvas.Node{{ID: "g", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}}},
	})
	exec := h.canvasExecutor
	runID, err := exec.RunAsync(context.Background(), canvas.RunOptions{ThreadID: "t1"})
	if err != nil {
		t.Fatalf("RunAsync: %v", err)
	}
	waitTerminal(t, exec, runID)
	resp := h.handleCanvasRunStatus(context.Background(), Request{
		ID:     "1",
		Method: "canvas/run-status",
		Params: map[string]any{"runId": runID},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	sum, ok := resp.Result.(*canvas.RunSummary)
	if !ok {
		t.Fatalf("result not RunSummary: %T", resp.Result)
	}
	if sum.RunID != runID {
		t.Fatalf("runID mismatch: %s vs %s", sum.RunID, runID)
	}
}

func TestHandleCanvasRunStatusRejectsUnknownID(t *testing.T) {
	t.Parallel()
	h, _ := newCanvasTestHandler(t)
	resp := h.handleCanvasRunStatus(context.Background(), Request{
		ID:     "1",
		Method: "canvas/run-status",
		Params: map[string]any{"runId": "no-such"},
	})
	if resp.Error == nil {
		t.Fatalf("expected error for unknown run, got %+v", resp)
	}
}

func TestHandleCanvasRunStatusRequiresRunID(t *testing.T) {
	t.Parallel()
	h, _ := newCanvasTestHandler(t)
	resp := h.handleCanvasRunStatus(context.Background(), Request{
		ID:     "1",
		Method: "canvas/run-status",
		Params: map[string]any{},
	})
	if resp.Error == nil {
		t.Fatalf("expected error for missing runId")
	}
}

func TestHandleCanvasRunCancelTriggersCancel(t *testing.T) {
	t.Parallel()
	h, _ := newCanvasTestHandler(t)
	writeCanvasDoc(t, h.dataDir, "t1", &canvas.Document{
		Nodes: []*canvas.Node{
			{ID: "a", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}},
			{ID: "b", Data: map[string]any{"nodeType": "imageGen", "prompt": "y"}},
		},
		Edges: []*canvas.Edge{{Source: "a", Target: "b", Type: canvas.EdgeFlow}},
	})
	exec := h.canvasExecutor
	runID, err := exec.RunAsync(context.Background(), canvas.RunOptions{ThreadID: "t1"})
	if err != nil {
		t.Fatalf("RunAsync: %v", err)
	}

	resp := h.handleCanvasRunCancel(context.Background(), Request{
		ID:     "1",
		Method: "canvas/run-cancel",
		Params: map[string]any{"runId": runID},
	})
	// Either the run is still running (cancel succeeds) or it already
	// finished (cancel returns false → invalidParams). Both are acceptable;
	// what matters is that we got a well-formed response.
	if resp.Error == nil {
		out := resp.Result.(map[string]any)
		if out["ok"] != true {
			t.Fatalf("cancel result: %+v", out)
		}
	}
	waitTerminal(t, exec, runID)
}

func TestHandleCanvasRunCancelRequiresRunID(t *testing.T) {
	t.Parallel()
	h, _ := newCanvasTestHandler(t)
	resp := h.handleCanvasRunCancel(context.Background(), Request{
		ID:     "1",
		Method: "canvas/run-cancel",
		Params: map[string]any{},
	})
	if resp.Error == nil {
		t.Fatalf("expected error for missing runId")
	}
}
