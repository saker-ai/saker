package canvas

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/tool"
)

// fakeRuntime captures every dispatch made by the executor so tests can
// assert on order and arguments without booting the real api.Runtime.
type fakeRuntime struct {
	mu          sync.Mutex
	toolCalls   []toolCall
	llmCalls    []string
	cacheCalls  []string
	mediaURL    string
	mediaType   string
	failTool    error
	failCache   error
	llmResponse string
	delay       time.Duration
}

type toolCall struct {
	Name   string
	Params map[string]any
}

func (f *fakeRuntime) ExecuteTool(ctx context.Context, name string, params map[string]any) (*tool.ToolResult, error) {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	cp := make(map[string]any, len(params))
	for k, v := range params {
		cp[k] = v
	}
	f.toolCalls = append(f.toolCalls, toolCall{Name: name, Params: cp})
	f.mu.Unlock()
	if f.failTool != nil {
		return nil, f.failTool
	}
	return &tool.ToolResult{
		Success: true,
		Output:  "ok",
		Structured: map[string]any{
			"media_url":  f.mediaURL,
			"media_type": f.mediaType,
		},
	}, nil
}

func (f *fakeRuntime) Run(_ context.Context, req api.Request) (*api.Response, error) {
	f.mu.Lock()
	f.llmCalls = append(f.llmCalls, req.Prompt)
	f.mu.Unlock()
	return &api.Response{Result: &api.Result{Output: f.llmResponse}}, nil
}

func (f *fakeRuntime) ProjectRoot() string { return "" }

func (f *fakeRuntime) CacheMedia(_ context.Context, rawURL, _ string) (string, string, error) {
	f.mu.Lock()
	f.cacheCalls = append(f.cacheCalls, rawURL)
	f.mu.Unlock()
	if f.failCache != nil {
		return "", "", f.failCache
	}
	return "/local" + rawURL, rawURL, nil
}

func (f *fakeRuntime) Tools() []toolCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]toolCall(nil), f.toolCalls...)
}

func newExecutor(rt Runtime, dataDir string) *Executor {
	return &Executor{
		Runtime:        rt,
		DataDir:        dataDir,
		Tracker:        NewRunTracker(),
		PerNodeTimeout: 5 * time.Second,
		SaveInterval:   time.Millisecond,
	}
}

func writeCanvas(t *testing.T, dir string, doc *Document) {
	t.Helper()
	if err := Save(dir, "t1", doc); err != nil {
		t.Fatalf("save: %v", err)
	}
}

func TestExecutorRunsLinearImageGen(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{mediaURL: "https://r/a.png", mediaType: "image"}
	dir := t.TempDir()
	writeCanvas(t, dir, &Document{
		Nodes: []*Node{
			{ID: "p", Data: map[string]any{"nodeType": "prompt"}},
			{ID: "g", Data: map[string]any{"nodeType": "imageGen", "prompt": "cat"}},
		},
		Edges: []*Edge{{Source: "p", Target: "g", Type: EdgeFlow}},
	})
	e := newExecutor(rt, dir)
	defer e.Tracker.Stop()

	sum, err := e.RunSync(context.Background(), RunOptions{ThreadID: "t1"})
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	if sum.Status != RunStatusDone || sum.Succeeded != 1 || sum.Total != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	if len(rt.Tools()) != 1 || rt.Tools()[0].Name != "generate_image" {
		t.Fatalf("tool calls: %+v", rt.Tools())
	}

	// Reload canvas to confirm result node + edge + history.
	doc, err := Load(dir, "t1")
	if err != nil {
		t.Fatalf("load after run: %v", err)
	}
	if len(doc.Nodes) != 3 {
		t.Fatalf("expected 3 nodes (prompt+gen+result), got %d", len(doc.Nodes))
	}
	resultEdges := 0
	for _, e := range doc.Edges {
		if e.Source == "g" && e.Type == EdgeFlow {
			resultEdges++
		}
	}
	if resultEdges != 1 {
		t.Fatalf("expected 1 flow edge from gen, got %d", resultEdges)
	}
	gen := doc.FindNode("g")
	if gen.Data["status"] != NodeStatusPending {
		t.Fatalf("gen status: %v", gen.Data["status"])
	}
	hist := loadHistory(gen.Data["generationHistory"])
	if len(hist) != 1 {
		t.Fatalf("history: %+v", gen.Data["generationHistory"])
	}
}

func TestExecutorRunsExistingChainInTopoOrder(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{mediaURL: "https://r/out.png", mediaType: "image"}
	dir := t.TempDir()
	// g1 → existing image → g2: a hand-wired chain where g2 already sees
	// an upstream image. Confirms ordering and that g2's params builder
	// resolves the reference from the existing graph.
	writeCanvas(t, dir, &Document{
		Nodes: []*Node{
			{ID: "g1", Data: map[string]any{"nodeType": "imageGen", "prompt": "first"}},
			{ID: "img", Data: map[string]any{"nodeType": "image", "mediaType": "image", "mediaUrl": "/seed.png"}},
			{ID: "g2", Data: map[string]any{"nodeType": "imageGen", "prompt": "second"}},
		},
		Edges: []*Edge{
			{Source: "g1", Target: "img", Type: EdgeFlow},
			{Source: "img", Target: "g2", Type: EdgeFlow},
		},
	})
	e := newExecutor(rt, dir)
	defer e.Tracker.Stop()

	sum, err := e.RunSync(context.Background(), RunOptions{ThreadID: "t1"})
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	if sum.Succeeded != 2 {
		t.Fatalf("succeeded: %d (%+v)", sum.Succeeded, sum)
	}
	calls := rt.Tools()
	if len(calls) != 2 {
		t.Fatalf("calls: %+v", calls)
	}
	// g1 runs before g2 (topo order).
	if calls[0].Params["prompt"] != "first" || calls[1].Params["prompt"] != "second" {
		t.Fatalf("topo ordering broken: %+v", calls)
	}
	// g2 sees the pre-wired upstream image.
	got, _ := calls[1].Params["reference_image"].(string)
	if got != "/seed.png" {
		t.Fatalf("g2 reference_image: %v", calls[1].Params)
	}
}

func TestExecutorSkipsDoneNodes(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{mediaURL: "/a.png", mediaType: "image"}
	dir := t.TempDir()
	writeCanvas(t, dir, &Document{
		Nodes: []*Node{
			{ID: "g", Data: map[string]any{"nodeType": "imageGen", "prompt": "x", "status": NodeStatusDone}},
		},
	})
	e := newExecutor(rt, dir)
	defer e.Tracker.Stop()

	sum, err := e.RunSync(context.Background(), RunOptions{ThreadID: "t1", SkipDone: true})
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	if sum.Skipped != 1 || sum.Succeeded != 0 {
		t.Fatalf("expected skip, got %+v", sum)
	}
	if len(rt.Tools()) != 0 {
		t.Fatalf("no tool should fire when skipDone hits, got %+v", rt.Tools())
	}
}

func TestExecutorRecordsToolFailure(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{failTool: errors.New("upstream 500")}
	dir := t.TempDir()
	writeCanvas(t, dir, &Document{
		Nodes: []*Node{{ID: "g", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}}},
	})
	e := newExecutor(rt, dir)
	defer e.Tracker.Stop()

	sum, _ := e.RunSync(context.Background(), RunOptions{ThreadID: "t1"})
	if sum.Status != RunStatusError || sum.Failed != 1 {
		t.Fatalf("summary: %+v", sum)
	}

	doc, _ := Load(dir, "t1")
	gen := doc.FindNode("g")
	if gen.Data["status"] != NodeStatusError || gen.Data["error"] != "upstream 500" {
		t.Fatalf("gen state: %+v", gen.Data)
	}
	if _, ok := gen.Data["lastErrorParams"].(string); !ok {
		t.Fatal("lastErrorParams should be set after failure")
	}
}

func TestExecutorRejectsCycles(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{}
	dir := t.TempDir()
	writeCanvas(t, dir, &Document{
		Nodes: []*Node{
			{ID: "a", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}},
			{ID: "b", Data: map[string]any{"nodeType": "imageGen", "prompt": "y"}},
		},
		Edges: []*Edge{
			{Source: "a", Target: "b", Type: EdgeFlow},
			{Source: "b", Target: "a", Type: EdgeReference},
		},
	})
	e := newExecutor(rt, dir)
	defer e.Tracker.Stop()

	sum, err := e.RunSync(context.Background(), RunOptions{ThreadID: "t1"})
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if sum.Status != RunStatusError {
		t.Fatalf("status: %+v", sum)
	}
	if len(rt.Tools()) != 0 {
		t.Fatal("no tools should fire when topology is cyclic")
	}
}

func TestExecutorContextEdgesDoNotBlockTopo(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{mediaURL: "/x.png", mediaType: "image"}
	dir := t.TempDir()
	writeCanvas(t, dir, &Document{
		Nodes: []*Node{
			{ID: "a", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}},
			{ID: "b", Data: map[string]any{"nodeType": "imageGen", "prompt": "y"}},
		},
		Edges: []*Edge{
			{Source: "a", Target: "b", Type: EdgeContext},
			{Source: "b", Target: "a", Type: EdgeContext},
		},
	})
	e := newExecutor(rt, dir)
	defer e.Tracker.Stop()

	sum, err := e.RunSync(context.Background(), RunOptions{ThreadID: "t1"})
	if err != nil {
		t.Fatalf("context cycle should be ignored: %v", err)
	}
	if sum.Succeeded != 2 {
		t.Fatalf("expected both to run, got %+v", sum)
	}
}

func TestExecutorSubsetExecution(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{mediaURL: "/a.png", mediaType: "image"}
	dir := t.TempDir()
	writeCanvas(t, dir, &Document{
		Nodes: []*Node{
			{ID: "a", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}},
			{ID: "b", Data: map[string]any{"nodeType": "imageGen", "prompt": "y"}},
			{ID: "c", Data: map[string]any{"nodeType": "imageGen", "prompt": "z"}},
		},
	})
	e := newExecutor(rt, dir)
	defer e.Tracker.Stop()

	sum, err := e.RunSync(context.Background(), RunOptions{ThreadID: "t1", NodeIDs: []string{"b"}})
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	if sum.Total != 1 || sum.Succeeded != 1 {
		t.Fatalf("subset summary: %+v", sum)
	}
}

func TestExecutorTextGenUsesLLMPath(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{llmResponse: "summary text"}
	dir := t.TempDir()
	writeCanvas(t, dir, &Document{
		Nodes: []*Node{{ID: "g", Data: map[string]any{"nodeType": "textGen", "prompt": "summarise"}}},
	})
	e := newExecutor(rt, dir)
	defer e.Tracker.Stop()

	sum, err := e.RunSync(context.Background(), RunOptions{ThreadID: "t1"})
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	if sum.Succeeded != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	if len(rt.toolCalls) != 0 {
		t.Fatalf("textGen should not call ExecuteTool: %+v", rt.toolCalls)
	}
	if len(rt.llmCalls) != 1 || rt.llmCalls[0] != "summarise" {
		t.Fatalf("llm calls: %+v", rt.llmCalls)
	}
}

func TestExecutorVoiceGenDispatchesByEngine(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{mediaURL: "/audio.mp3", mediaType: "audio"}
	dir := t.TempDir()
	writeCanvas(t, dir, &Document{
		Nodes: []*Node{
			{ID: "tts", Data: map[string]any{"nodeType": "voiceGen", "prompt": "hi", "engine": "openai-tts"}},
			{ID: "music", Data: map[string]any{"nodeType": "voiceGen", "prompt": "song", "engine": "suno-music"}},
		},
	})
	e := newExecutor(rt, dir)
	defer e.Tracker.Stop()

	if _, err := e.RunSync(context.Background(), RunOptions{ThreadID: "t1"}); err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	tools := rt.Tools()
	gotNames := []string{tools[0].Name, tools[1].Name}
	wantTTS, wantMusic := false, false
	for _, n := range gotNames {
		if n == "text_to_speech" {
			wantTTS = true
		}
		if n == "generate_music" {
			wantMusic = true
		}
	}
	if !wantTTS || !wantMusic {
		t.Fatalf("expected both tools dispatched, got %v", gotNames)
	}
}

func TestExecutorCancellationStopsMidRun(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{mediaURL: "/x.png", mediaType: "image", delay: 200 * time.Millisecond}
	dir := t.TempDir()
	writeCanvas(t, dir, &Document{
		Nodes: []*Node{
			{ID: "a", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}},
			{ID: "b", Data: map[string]any{"nodeType": "imageGen", "prompt": "y"}},
			{ID: "c", Data: map[string]any{"nodeType": "imageGen", "prompt": "z"}},
		},
		Edges: []*Edge{
			{Source: "a", Target: "b", Type: EdgeFlow},
			{Source: "b", Target: "c", Type: EdgeFlow},
		},
	})
	e := newExecutor(rt, dir)
	defer e.Tracker.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	var sum *RunSummary
	var done int32
	go func() {
		s, _ := e.RunSync(ctx, RunOptions{ThreadID: "t1"})
		sum = s
		atomic.StoreInt32(&done, 1)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	for i := 0; i < 100; i++ {
		if atomic.LoadInt32(&done) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if sum == nil || sum.Status != RunStatusCancelled {
		t.Fatalf("expected cancelled status, got %+v", sum)
	}
	if len(rt.Tools()) >= 3 {
		t.Fatalf("expected fewer than all dispatches before cancel, got %d", len(rt.Tools()))
	}
}

func TestExecutorRunAsyncRegistersWithTracker(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{mediaURL: "/a.png", mediaType: "image"}
	dir := t.TempDir()
	writeCanvas(t, dir, &Document{
		Nodes: []*Node{{ID: "g", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}}},
	})
	e := newExecutor(rt, dir)
	defer e.Tracker.Stop()

	runID, err := e.RunAsync(context.Background(), RunOptions{ThreadID: "t1"})
	if err != nil {
		t.Fatalf("RunAsync: %v", err)
	}
	for i := 0; i < 100; i++ {
		s, ok := e.Tracker.Get(runID)
		if ok && s.Status != RunStatusRunning {
			if s.Status != RunStatusDone || s.Succeeded != 1 {
				t.Fatalf("async finished badly: %+v", s)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("async run never reached terminal status")
}

func TestExecutorNotifiesNodeAndRunStatus(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{mediaURL: "/a.png", mediaType: "image"}
	dir := t.TempDir()
	writeCanvas(t, dir, &Document{
		Nodes: []*Node{{ID: "g", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}}},
	})
	var mu sync.Mutex
	events := []string{}
	e := newExecutor(rt, dir)
	defer e.Tracker.Stop()
	e.Notify = func(_ string, method string, _ map[string]any) {
		mu.Lock()
		events = append(events, method)
		mu.Unlock()
	}

	if _, err := e.RunSync(context.Background(), RunOptions{ThreadID: "t1"}); err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := map[string]bool{"canvas/run-status": false, "canvas/node-status": false}
	for _, ev := range events {
		if _, ok := want[ev]; ok {
			want[ev] = true
		}
	}
	for ev, seen := range want {
		if !seen {
			t.Errorf("missing notify: %q (events: %v)", ev, events)
		}
	}
}

func TestExecutorRequiresThreadID(t *testing.T) {
	t.Parallel()
	e := newExecutor(&fakeRuntime{}, t.TempDir())
	defer e.Tracker.Stop()
	if _, err := e.RunSync(context.Background(), RunOptions{}); err == nil {
		t.Fatal("expected error for empty thread id")
	}
	if _, err := e.RunAsync(context.Background(), RunOptions{}); err == nil {
		t.Fatal("expected error for empty thread id (async)")
	}
}

func TestExecutorEmptyCanvasIsNoOp(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{}
	dir := t.TempDir()
	writeCanvas(t, dir, &Document{})
	e := newExecutor(rt, dir)
	defer e.Tracker.Stop()

	sum, err := e.RunSync(context.Background(), RunOptions{ThreadID: "t1"})
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	if sum.Status != RunStatusDone || sum.Total != 0 {
		t.Fatalf("summary: %+v", sum)
	}
}
