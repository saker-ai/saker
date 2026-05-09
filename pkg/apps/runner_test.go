package apps

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/canvas"
	"github.com/cinience/saker/pkg/tool"
)

// fakeRuntime mirrors the minimal canvas.Runtime surface used in the
// canvas package's executor tests. It records every dispatch so the
// runner test can assert what the executor was asked to do.
type fakeRuntime struct {
	mu        sync.Mutex
	toolCalls []toolCall
	mediaURL  string
	mediaType string
}

type toolCall struct {
	Name   string
	Params map[string]any
}

func (f *fakeRuntime) ExecuteTool(_ context.Context, name string, params map[string]any) (*tool.ToolResult, error) {
	f.mu.Lock()
	cp := make(map[string]any, len(params))
	for k, v := range params {
		cp[k] = v
	}
	f.toolCalls = append(f.toolCalls, toolCall{Name: name, Params: cp})
	f.mu.Unlock()
	return &tool.ToolResult{
		Success: true,
		Output:  "ok",
		Structured: map[string]any{
			"media_url":  f.mediaURL,
			"media_type": f.mediaType,
		},
	}, nil
}

func (f *fakeRuntime) Run(_ context.Context, _ api.Request) (*api.Response, error) {
	return &api.Response{Result: &api.Result{Output: ""}}, nil
}

func (f *fakeRuntime) ProjectRoot() string { return "" }

func (f *fakeRuntime) CacheMedia(_ context.Context, raw, _ string) (string, string, error) {
	return raw, raw, nil
}

func (f *fakeRuntime) calls() []toolCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]toolCall(nil), f.toolCalls...)
}

// newRunner wires Store + Executor + Runner all rooted in a single
// t.TempDir so the temp thread Save lands where the executor's Load
// will look.
func newRunner(t *testing.T, rt canvas.Runtime) *Runner {
	t.Helper()
	dir := t.TempDir()
	store := New(dir)
	exec := &canvas.Executor{
		Runtime:        rt,
		DataDir:        dir,
		Tracker:        canvas.NewRunTracker(),
		PerNodeTimeout: 5 * time.Second,
		SaveInterval:   time.Millisecond,
	}
	t.Cleanup(func() { exec.Tracker.Stop() })
	return NewRunner(store, exec, dir)
}

func TestRunnerNotPublished(t *testing.T) {
	t.Parallel()
	r := newRunner(t, &fakeRuntime{})
	created, err := r.Store.Create(context.Background(), CreateInput{Name: "Unpub"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background(), created.ID, map[string]any{})
	if !errors.Is(err, ErrNotPublished) {
		t.Fatalf("expected ErrNotPublished, got %v", err)
	}
}

func TestRunnerValidateError(t *testing.T) {
	t.Parallel()
	r := newRunner(t, &fakeRuntime{})
	created, err := r.Store.Create(context.Background(), CreateInput{Name: "V"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Store.PublishVersion(context.Background(), created.ID, minimalPublishableDoc(), ""); err != nil {
		t.Fatal(err)
	}

	_, err = r.Run(context.Background(), created.ID, map[string]any{}) // missing required topic
	if err == nil || !strings.Contains(err.Error(), "topic is required") {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestRunnerHappyPath(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{
		mediaURL:  "https://example.com/img.png",
		mediaType: "image",
	}
	r := newRunner(t, rt)

	created, err := r.Store.Create(context.Background(), CreateInput{Name: "Happy"})
	if err != nil {
		t.Fatal(err)
	}

	// Override the imageGen prompt so our injected value can be observed
	// downstream — the runner sets node.Data["prompt"] on the appInput
	// node, but the imageGen node has its own prompt that BuildParams
	// reads. Set it to a known string so we can assert the dispatch.
	doc := minimalPublishableDoc()
	for _, n := range doc.Nodes {
		if n.ID == "gen1" {
			n.Data["prompt"] = "fixed-prompt"
		}
	}

	if _, err := r.Store.PublishVersion(context.Background(), created.ID, doc, ""); err != nil {
		t.Fatal(err)
	}

	runID, err := r.Run(context.Background(), created.ID, map[string]any{
		"topic": "red panda",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if runID == "" {
		t.Fatal("Run returned empty runID")
	}

	// Wait for the async run to finish so we can inspect calls.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if summary, ok := r.Executor.Tracker.Get(runID); ok && summary.Status != canvas.RunStatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	summary, ok := r.Executor.Tracker.Get(runID)
	if !ok {
		t.Fatal("tracker lost runID")
	}
	if summary.Status != canvas.RunStatusDone {
		t.Fatalf("run status=%q error=%q", summary.Status, summary.Error)
	}

	calls := rt.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d: %+v", len(calls), calls)
	}
	if calls[0].Name != "generate_image" {
		t.Fatalf("expected generate_image, got %q", calls[0].Name)
	}

	// The temp thread file written by the runner must be on disk and
	// must contain the injected value on the appInput node.
	threads, err := os.ReadDir(r.DataDir + "/canvas")
	if err != nil {
		t.Fatalf("read canvas dir: %v", err)
	}
	var sawTemp bool
	for _, e := range threads {
		if strings.HasPrefix(e.Name(), "app-run-") {
			sawTemp = true
			doc, err := canvas.Load(r.DataDir, strings.TrimSuffix(e.Name(), ".json"))
			if err != nil {
				t.Fatalf("load temp thread: %v", err)
			}
			var found bool
			for _, n := range doc.Nodes {
				if n.NodeType() != "appInput" {
					continue
				}
				if v, _ := n.Data["value"].(string); v == "red panda" {
					found = true
				}
			}
			if !found {
				t.Errorf("appInput node did not receive injected value: %+v", doc.Nodes)
			}
		}
	}
	if !sawTemp {
		t.Fatal("expected an app-run-* canvas file on disk")
	}
}

// TestRunnerCoercesNumberString feeds a numeric string for a number field
// and asserts the runner stores float64 in value + clean "42" in prompt.
func TestRunnerCoercesNumberString(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{mediaURL: "https://example.com/img.png", mediaType: "image"}
	r := newRunner(t, rt)

	created, err := r.Store.Create(context.Background(), CreateInput{Name: "N"})
	if err != nil {
		t.Fatal(err)
	}

	doc := minimalPublishableDoc()
	for _, n := range doc.Nodes {
		if n.ID == "in1" {
			n.Data["appVariable"] = "count"
			n.Data["appFieldType"] = "number"
		}
		if n.ID == "gen1" {
			n.Data["prompt"] = "p"
		}
	}
	if _, err := r.Store.PublishVersion(context.Background(), created.ID, doc, ""); err != nil {
		t.Fatal(err)
	}

	runID, err := r.Run(context.Background(), created.ID, map[string]any{"count": "42"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	waitForRun(t, r, runID)

	threads, _ := os.ReadDir(r.DataDir + "/canvas")
	var checked bool
	for _, e := range threads {
		if !strings.HasPrefix(e.Name(), "app-run-") {
			continue
		}
		d, err := canvas.Load(r.DataDir, strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range d.Nodes {
			if n.NodeType() != "appInput" {
				continue
			}
			f, ok := n.Data["value"].(float64)
			if !ok || f != 42 {
				t.Fatalf("expected float64(42), got %T(%v)", n.Data["value"], n.Data["value"])
			}
			if s, _ := n.Data["prompt"].(string); s != "42" {
				t.Fatalf("expected prompt=\"42\", got %q", s)
			}
			checked = true
		}
	}
	if !checked {
		t.Fatal("appInput value was never inspected")
	}
}

// TestRunnerRejectsFileNonString verifies that a file field receiving a
// non-string payload (a contract violation from the upload pipeline) is
// rejected before dispatch.
func TestRunnerRejectsFileNonString(t *testing.T) {
	t.Parallel()
	r := newRunner(t, &fakeRuntime{})

	created, err := r.Store.Create(context.Background(), CreateInput{Name: "F"})
	if err != nil {
		t.Fatal(err)
	}
	doc := minimalPublishableDoc()
	for _, n := range doc.Nodes {
		if n.ID == "in1" {
			n.Data["appVariable"] = "asset"
			n.Data["appFieldType"] = "file"
		}
	}
	if _, err := r.Store.PublishVersion(context.Background(), created.ID, doc, ""); err != nil {
		t.Fatal(err)
	}

	_, err = r.Run(context.Background(), created.ID, map[string]any{"asset": 123})
	if err == nil || !strings.Contains(err.Error(), "mediaUrl") {
		t.Fatalf("expected mediaUrl error, got %v", err)
	}
}

// TestRunnerOnTempThreadFires asserts the OnTempThread callback receives
// the (threadID, dataDir) pair that the runner just wrote — this is the
// hook the server relies on to GC temp threads after canvas/run-finished.
func TestRunnerOnTempThreadFires(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{mediaURL: "https://example.com/img.png", mediaType: "image"}
	r := newRunner(t, rt)

	var (
		gotThread, gotDir string
		gotMu             sync.Mutex
	)
	r.OnTempThread = func(threadID, dataDir string) {
		gotMu.Lock()
		defer gotMu.Unlock()
		gotThread = threadID
		gotDir = dataDir
	}

	created, err := r.Store.Create(context.Background(), CreateInput{Name: "Cb"})
	if err != nil {
		t.Fatal(err)
	}
	doc := minimalPublishableDoc()
	for _, n := range doc.Nodes {
		if n.ID == "gen1" {
			n.Data["prompt"] = "p"
		}
	}
	if _, err := r.Store.PublishVersion(context.Background(), created.ID, doc, ""); err != nil {
		t.Fatal(err)
	}
	runID, err := r.Run(context.Background(), created.ID, map[string]any{"topic": "x"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	waitForRun(t, r, runID)

	gotMu.Lock()
	defer gotMu.Unlock()
	if !strings.HasPrefix(gotThread, "app-run-") {
		t.Fatalf("OnTempThread threadID=%q does not start with app-run-", gotThread)
	}
	if gotDir != r.DataDir {
		t.Fatalf("OnTempThread dataDir=%q want %q", gotDir, r.DataDir)
	}
}

// waitForRun blocks until the executor has reached a terminal state for runID
// or the deadline expires. Tests that don't wait can race the executor's
// background save into t.TempDir cleanup, leaving stale files behind.
func waitForRun(t *testing.T, r *Runner, runID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s, ok := r.Executor.Tracker.Get(runID); ok && s.Status != canvas.RunStatusRunning {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waitForRun: runID %s never reached terminal state", runID)
}

func TestRunnerAppliesDefaultWhenInputOmitted(t *testing.T) {
	t.Parallel()
	rt := &fakeRuntime{
		mediaURL:  "https://example.com/img.png",
		mediaType: "image",
	}
	r := newRunner(t, rt)

	created, err := r.Store.Create(context.Background(), CreateInput{Name: "Def"})
	if err != nil {
		t.Fatal(err)
	}

	// Mark topic optional + give it a default so validate passes when
	// the caller omits it; the runner should then inject the default.
	doc := minimalPublishableDoc()
	for _, n := range doc.Nodes {
		if n.ID == "in1" {
			n.Data["appRequired"] = false
			n.Data["appDefault"] = "panda-default"
		}
		if n.ID == "gen1" {
			n.Data["prompt"] = "p"
		}
	}
	if _, err := r.Store.PublishVersion(context.Background(), created.ID, doc, ""); err != nil {
		t.Fatal(err)
	}

	runID, err := r.Run(context.Background(), created.ID, map[string]any{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s, ok := r.Executor.Tracker.Get(runID); ok && s.Status != canvas.RunStatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	threads, _ := os.ReadDir(r.DataDir + "/canvas")
	var sawDefault bool
	for _, e := range threads {
		if !strings.HasPrefix(e.Name(), "app-run-") {
			continue
		}
		d, err := canvas.Load(r.DataDir, strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range d.Nodes {
			if n.NodeType() == "appInput" {
				if v, _ := n.Data["value"].(string); v == "panda-default" {
					sawDefault = true
				}
			}
		}
	}
	if !sawDefault {
		t.Fatal("default value was not injected")
	}
}
