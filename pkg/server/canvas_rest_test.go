package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/saker-ai/saker/pkg/canvas"
	"github.com/gin-gonic/gin"
)

// newCanvasTestServer wires the four REST endpoints onto an httptest.Server
// via a minimal gin engine that registers the canvas routes the same way
// production does (registerCanvasRoutes). No auth or other handler
// dependencies are wired in.
func newCanvasTestServer(t *testing.T) (*httptest.Server, *Server, *fakeCanvasRuntime) {
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

	s := &Server{handler: h, opts: Options{DataDir: dir}}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.HandleMethodNotAllowed = true
	authed := engine.Group("")
	s.registerCanvasRoutes(authed)
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)
	return srv, s, rt
}

func decodeJSON(t *testing.T, r io.Reader, into any) {
	t.Helper()
	if err := json.NewDecoder(r).Decode(into); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

// drainRun waits until the tracker reports the run as terminal. Required
// before tests return because the executor's background goroutine writes
// to the canvas JSON file and racing TempDir cleanup wins otherwise.
func drainRun(t *testing.T, exec *canvas.Executor, runID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s, ok := exec.Tracker.Get(runID); ok && s.Status != canvas.RunStatusRunning {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s never finished", runID)
}

func TestCanvasRESTExecuteRejectsBadMethod(t *testing.T) {
	t.Parallel()
	srv, _, _ := newCanvasTestServer(t)
	resp, err := http.Get(srv.URL + "/api/canvas/t1/execute")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestCanvasRESTExecuteHappyPathReturns202(t *testing.T) {
	t.Parallel()
	srv, s, _ := newCanvasTestServer(t)
	writeCanvasDoc(t, s.handler.dataDir, "t1", &canvas.Document{
		Nodes: []*canvas.Node{{ID: "g", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}}},
	})

	body := bytes.NewBufferString(`{"skipDone":true}`)
	resp, err := http.Post(srv.URL+"/api/canvas/t1/execute", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d, body: %s", resp.StatusCode, buf)
	}
	var out map[string]any
	decodeJSON(t, resp.Body, &out)
	runID, ok := out["runId"].(string)
	if !ok {
		t.Fatalf("missing runId: %+v", out)
	}
	if out["status"] != canvas.RunStatusRunning {
		t.Fatalf("status: %+v", out)
	}
	drainRun(t, s.handler.canvasExecutor, runID)
}

func TestCanvasRESTExecuteAcceptsEmptyBody(t *testing.T) {
	t.Parallel()
	srv, s, _ := newCanvasTestServer(t)
	writeCanvasDoc(t, s.handler.dataDir, "t1", &canvas.Document{
		Nodes: []*canvas.Node{{ID: "g", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}}},
	})
	resp, err := http.Post(srv.URL+"/api/canvas/t1/execute", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out map[string]any
	decodeJSON(t, resp.Body, &out)
	runID, _ := out["runId"].(string)
	drainRun(t, s.handler.canvasExecutor, runID)
}

func TestCanvasRESTExecuteRejectsBadJSON(t *testing.T) {
	t.Parallel()
	srv, _, _ := newCanvasTestServer(t)
	resp, err := http.Post(srv.URL+"/api/canvas/t1/execute", "application/json", bytes.NewBufferString("{broken"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestCanvasRESTRunStatusRoundtrip(t *testing.T) {
	t.Parallel()
	srv, s, _ := newCanvasTestServer(t)
	writeCanvasDoc(t, s.handler.dataDir, "t1", &canvas.Document{
		Nodes: []*canvas.Node{{ID: "g", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}}},
	})

	// Start a run.
	resp, err := http.Post(srv.URL+"/api/canvas/t1/execute", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var startResp map[string]any
	decodeJSON(t, resp.Body, &startResp)
	resp.Body.Close()
	runID := startResp["runId"].(string)

	// Poll status to terminal state.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, err := http.Get(srv.URL + "/api/canvas/runs/" + runID)
		if err != nil {
			t.Fatalf("GET status: %v", err)
		}
		if r.StatusCode != http.StatusOK {
			r.Body.Close()
			t.Fatalf("status: %d", r.StatusCode)
		}
		var sum canvas.RunSummary
		decodeJSON(t, r.Body, &sum)
		r.Body.Close()
		if sum.Status != canvas.RunStatusRunning {
			if sum.Status != canvas.RunStatusDone || sum.Succeeded != 1 {
				t.Fatalf("final summary: %+v", sum)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run never finished")
}

func TestCanvasRESTRunStatusUnknownID(t *testing.T) {
	t.Parallel()
	srv, _, _ := newCanvasTestServer(t)
	resp, err := http.Get(srv.URL + "/api/canvas/runs/no-such")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestCanvasRESTRunCancelMissingID(t *testing.T) {
	t.Parallel()
	srv, _, _ := newCanvasTestServer(t)
	resp, err := http.Post(srv.URL+"/api/canvas/runs/no-such/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestCanvasRESTRunCancelRejectsBadMethod(t *testing.T) {
	t.Parallel()
	srv, _, _ := newCanvasTestServer(t)
	resp, err := http.Get(srv.URL + "/api/canvas/runs/whatever/cancel")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestCanvasRESTDocumentReturnsSavedJSON(t *testing.T) {
	t.Parallel()
	srv, s, _ := newCanvasTestServer(t)
	writeCanvasDoc(t, s.handler.dataDir, "t1", &canvas.Document{
		Nodes: []*canvas.Node{{ID: "n1", Data: map[string]any{"nodeType": "prompt"}}},
	})
	resp, err := http.Get(srv.URL + "/api/canvas/t1/document")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var doc canvas.Document
	decodeJSON(t, resp.Body, &doc)
	if len(doc.Nodes) != 1 || doc.Nodes[0].ID != "n1" {
		t.Fatalf("doc: %+v", doc)
	}
}

func TestCanvasRESTRejectsUnknownAction(t *testing.T) {
	t.Parallel()
	srv, _, _ := newCanvasTestServer(t)
	resp, err := http.Get(srv.URL + "/api/canvas/t1/bogus")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestCanvasRESTRejectsEmptyPath(t *testing.T) {
	t.Parallel()
	srv, _, _ := newCanvasTestServer(t)
	resp, err := http.Get(srv.URL + "/api/canvas/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	// Native gin routes return 404 for the empty resource case (no
	// matching route) instead of the dispatcher's bespoke 400.
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}
