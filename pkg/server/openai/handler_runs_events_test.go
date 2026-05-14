package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/runhub"
	"github.com/cinience/saker/pkg/runhub/store"
	"github.com/gin-gonic/gin"
)

// newReconnectTestGateway mounts only the reconnect endpoint with a
// caller-provided hub. The auth middleware runs in dev-bypass so every
// request is attributed to the "localhost" identity (APIKeyID="",
// Username="localhost") — tenant scoping tests adjust the run's
// TenantID to exercise the access-control branches.
func newReconnectTestGateway(t *testing.T, hub runhub.Hub) (*Gateway, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	deps := Deps{
		Runtime: stubRunner{},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options: Options{
			Enabled:             true,
			DevBypassAuth:       true,
			MaxRuns:             10,
			MaxRunsPerTenant:    5,
			RingSize:            64,
			ExpiresAfterSeconds: 60,
			MaxRequestBodyBytes: 1024,
			ErrorDetailMode:     "dev",
		},
	}
	if err := deps.Options.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	gw := &Gateway{deps: deps, hub: hub}
	t.Cleanup(gw.Shutdown)

	eng := gin.New()
	v1 := eng.Group("/v1")
	v1.Use(gw.authMiddleware())
	v1.GET("/runs/:id/events", gw.handleRunsEvents)
	return gw, eng
}

func newMemoryReconnectGateway(t *testing.T, ringSize int) (*Gateway, *gin.Engine) {
	t.Helper()
	hub := runhub.NewHub(runhub.Config{
		MaxRuns:  10,
		RingSize: ringSize,
	})
	return newReconnectTestGateway(t, hub)
}

func newPersistentReconnectGateway(t *testing.T, ringSize int) (*Gateway, *gin.Engine) {
	t.Helper()
	s, err := store.Open(store.Config{DSN: filepath.Join(t.TempDir(), "rh.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	hub, err := runhub.NewPersistentHub(runhub.PersistentConfig{
		Config: runhub.Config{MaxRuns: 10, RingSize: ringSize},
		Store:  s,
	})
	if err != nil {
		t.Fatalf("NewPersistentHub: %v", err)
	}
	return newReconnectTestGateway(t, hub)
}

// fetchEvents drives the reconnect endpoint and returns the parsed SSE
// `id:` lines, the body string, and the response status.
//
// lastEventID is a bare seq for callsite ergonomics; this helper appends
// the `<runID>:` prefix the wire protocol now requires. Passing a
// negative value sends no last_event_id at all (replay-everything path).
//
// Parsed ids strip the `<runID>:` prefix so existing assertions that
// compare against bare integers continue to work after the wire-format
// breaking change.
func fetchEvents(t *testing.T, eng *gin.Engine, runID string, lastEventID int) (ids []int, body string, status int) {
	t.Helper()
	url := "/v1/runs/" + runID + "/events"
	if lastEventID >= 0 {
		url += "?last_event_id=" + runID + ":" + strconv.Itoa(lastEventID)
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	eng.ServeHTTP(rec, req)
	body = rec.Body.String()
	status = rec.Code
	prefix := "id: " + runID + ":"
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			n, err := strconv.Atoi(strings.TrimSpace(line[len(prefix):]))
			if err == nil {
				ids = append(ids, n)
			}
		}
	}
	return ids, body, status
}

func TestRunsEvents_UnknownRunReturns404(t *testing.T) {
	t.Parallel()
	_, eng := newMemoryReconnectGateway(t, 64)

	_, body, status := fetchEvents(t, eng, "run_does_not_exist", 0)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", status, body)
	}
}

func TestRunsEvents_BackfillFromRing(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryReconnectGateway(t, 64)

	run, err := gw.hub.Create(runhub.CreateOptions{TenantID: "localhost"})
	if err != nil {
		t.Fatalf("hub Create: %v", err)
	}
	for i := 0; i < 5; i++ {
		run.Publish("chunk", []byte(`{"x":`+strconv.Itoa(i)+`}`))
	}
	gw.hub.Finish(run.ID, runhub.RunStatusCompleted)

	ids, body, status := fetchEvents(t, eng, run.ID, 0)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if len(ids) != 5 {
		t.Fatalf("expected 5 events, got %d (body=%s)", len(ids), body)
	}
	for i, n := range ids {
		if n != i+1 {
			t.Errorf("ids[%d] = %d, want %d", i, n, i+1)
		}
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("expected [DONE] sentinel in body, got: %s", body)
	}
}

func TestRunsEvents_LastEventIDFiltersBackfill(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryReconnectGateway(t, 64)

	run, _ := gw.hub.Create(runhub.CreateOptions{TenantID: "localhost"})
	for i := 0; i < 5; i++ {
		run.Publish("chunk", []byte("x"))
	}
	gw.hub.Finish(run.ID, runhub.RunStatusCompleted)

	ids, _, status := fetchEvents(t, eng, run.ID, 3)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if len(ids) != 2 || ids[0] != 4 || ids[1] != 5 {
		t.Errorf("expected ids [4 5], got %v", ids)
	}
}

func TestRunsEvents_RingMissNoSinkReturns410(t *testing.T) {
	t.Parallel()
	// Tiny ring + many events + MemoryHub (no sink) → aged-out prefix
	// is unrecoverable.
	gw, eng := newMemoryReconnectGateway(t, 4)

	run, _ := gw.hub.Create(runhub.CreateOptions{TenantID: "localhost"})
	for i := 0; i < 20; i++ {
		run.Publish("chunk", []byte("x"))
	}
	gw.hub.Finish(run.ID, runhub.RunStatusCompleted)

	_, body, status := fetchEvents(t, eng, run.ID, 5)
	if status != http.StatusGone {
		t.Fatalf("status = %d, want 410; body=%s", status, body)
	}
	if !strings.Contains(body, "event_replay_unrecoverable") {
		t.Errorf("expected error code in body, got: %s", body)
	}
}

func TestRunsEvents_RingMissWithStoreSinkBackfills(t *testing.T) {
	t.Parallel()
	// PersistentHub with tiny ring should still serve backfill from
	// the sink for seqs that aged out of memory.
	gw, eng := newPersistentReconnectGateway(t, 4)

	run, _ := gw.hub.Create(runhub.CreateOptions{TenantID: "localhost"})
	for i := 0; i < 20; i++ {
		run.Publish("chunk", []byte("x"))
	}
	// Async batch writer — fence so the SSE backfill reads from a fully
	// persisted store.
	gw.hub.Flush()
	gw.hub.Finish(run.ID, runhub.RunStatusCompleted)

	ids, body, status := fetchEvents(t, eng, run.ID, 5)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if len(ids) != 15 {
		t.Fatalf("expected 15 events from sink, got %d", len(ids))
	}
	if ids[0] != 6 || ids[14] != 20 {
		t.Errorf("expected ids 6..20, got %d..%d", ids[0], ids[14])
	}
}

func TestRunsEvents_CrossTenantReturns404(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryReconnectGateway(t, 64)

	// Caller (dev-bypass identity) is "localhost"; create the run as a
	// different tenant so the access-control branch fires.
	run, _ := gw.hub.Create(runhub.CreateOptions{TenantID: "someone-else"})
	run.Publish("chunk", []byte("x"))
	gw.hub.Finish(run.ID, runhub.RunStatusCompleted)

	_, body, status := fetchEvents(t, eng, run.ID, 0)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (existence-leak prevention); body=%s", status, body)
	}
}

func TestRunsEvents_EmptyTenantRejects(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryReconnectGateway(t, 64)

	run, _ := gw.hub.Create(runhub.CreateOptions{})
	run.Publish("chunk", []byte("x"))
	gw.hub.Finish(run.ID, runhub.RunStatusCompleted)

	_, _, status := fetchEvents(t, eng, run.ID, 0)
	if status != http.StatusNotFound {
		t.Errorf("empty-tenant runs must be rejected, got %d", status)
	}
}

func TestRunsEvents_LastEventIDHeaderHonored(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryReconnectGateway(t, 64)

	run, _ := gw.hub.Create(runhub.CreateOptions{TenantID: "localhost"})
	for i := 0; i < 4; i++ {
		run.Publish("chunk", []byte("x"))
	}
	gw.hub.Finish(run.ID, runhub.RunStatusCompleted)

	// Drive the request manually so we can set Last-Event-ID instead
	// of the query param. Header carries the qualified `<runID>:<seq>`
	// cursor — see handler_runs_events.go parseLastEventID.
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+run.ID+"/events", nil)
	req.Header.Set("Last-Event-ID", run.ID+":2")
	rec := httptest.NewRecorder()
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := []int{}
	prefix := "id: " + run.ID + ":"
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(line, prefix) {
			n, _ := strconv.Atoi(strings.TrimSpace(line[len(prefix):]))
			got = append(got, n)
		}
	}
	if len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Errorf("expected ids [3 4] from header, got %v (body=%s)", got, rec.Body.String())
	}
}

func TestRunsEvents_QueryParamWinsOverHeader(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryReconnectGateway(t, 64)

	run, _ := gw.hub.Create(runhub.CreateOptions{TenantID: "localhost"})
	for i := 0; i < 5; i++ {
		run.Publish("chunk", []byte("x"))
	}
	gw.hub.Finish(run.ID, runhub.RunStatusCompleted)

	// Both header and query are qualified `<runID>:<seq>` per the new
	// wire contract. Query (=4) should beat header (=1).
	req := httptest.NewRequest(http.MethodGet,
		"/v1/runs/"+run.ID+"/events?last_event_id="+run.ID+":4", nil)
	req.Header.Set("Last-Event-ID", run.ID+":1")
	rec := httptest.NewRecorder()
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	count := strings.Count(rec.Body.String(), "id: ")
	if count != 1 {
		t.Errorf("query (=4) should win over header (=1), expected 1 event id, got %d (body=%s)",
			count, rec.Body.String())
	}
}

func TestRunsEvents_LiveTailAfterBackfill(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryReconnectGateway(t, 64)

	run, _ := gw.hub.Create(runhub.CreateOptions{TenantID: "localhost"})
	// Pre-publish seq 1; after the GET subscribes we'll publish 2..3
	// and then Finish to close the channel.
	run.Publish("chunk", []byte("a"))

	rec := httptest.NewRecorder()
	// Cursor is `<runID>:0` per the qualified wire format.
	req := httptest.NewRequest(http.MethodGet,
		"/v1/runs/"+run.ID+"/events?last_event_id="+run.ID+":0", nil)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		eng.ServeHTTP(rec, req)
	}()

	// Give the handler a moment to subscribe before publishing more.
	time.Sleep(50 * time.Millisecond)
	run.Publish("chunk", []byte("b"))
	run.Publish("chunk", []byte("c"))
	gw.hub.Finish(run.ID, runhub.RunStatusCompleted)

	// Wait for the handler to drain and write [DONE].
	doneCh := make(chan struct{})
	go func() { wg.Wait(); close(doneCh) }()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after Finish")
	}

	body := rec.Body.String()
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("expected [DONE] sentinel, got: %s", body)
	}
	for i := 1; i <= 3; i++ {
		want := fmt.Sprintf("id: %s:%d", run.ID, i)
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in body, got: %s", want, body)
		}
	}
}

// TestRunsEvents_MalformedLastEventIDReturns400 locks in the strict
// wire-format breaking change: bare integers, non-numeric seqs, missing
// colons, and other garbage are now client bugs (400). The previous
// "treat as 0 and replay from start" fallback is gone — silently doing
// the right thing was masking client cursor-tracking bugs in production.
func TestRunsEvents_MalformedLastEventIDReturns400(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryReconnectGateway(t, 64)

	run, _ := gw.hub.Create(runhub.CreateOptions{TenantID: "localhost"})
	run.Publish("chunk", []byte("x"))
	gw.hub.Finish(run.ID, runhub.RunStatusCompleted)

	cases := []struct {
		name  string
		value string
	}{
		{"bare-int (legacy format)", "5"},
		{"non-numeric seq", run.ID + ":abc"},
		{"empty run portion", ":5"},
		{"empty seq portion", run.ID + ":"},
		{"negative seq", run.ID + ":-1"},
		{"garbage", "not-a-number"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet,
				"/v1/runs/"+run.ID+"/events?last_event_id="+c.value, nil)
			rec := httptest.NewRecorder()
			eng.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "last_event_id") {
				t.Errorf("error body should mention last_event_id field, got: %s",
					rec.Body.String())
			}
		})
	}
}

// TestRunsEvents_CrossRunCursorReturns404 locks in the existence-leak
// guard: a cursor whose run portion doesn't match path :id is treated
// the same as "no such run" (404), so an attacker can't probe whether
// some other run id exists by feeding its cursor through a known run's
// reconnect endpoint.
func TestRunsEvents_CrossRunCursorReturns404(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryReconnectGateway(t, 64)

	run, _ := gw.hub.Create(runhub.CreateOptions{TenantID: "localhost"})
	run.Publish("chunk", []byte("x"))
	gw.hub.Finish(run.ID, runhub.RunStatusCompleted)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/runs/"+run.ID+"/events?last_event_id=run_otherprobe:0", nil)
	rec := httptest.NewRecorder()
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (cross-run cursor existence-leak guard); body=%s",
			rec.Code, rec.Body.String())
	}
}

func TestRunsEvents_RouteIsMounted(t *testing.T) {
	t.Parallel()
	// Smoke test against the real Register flow rather than a hand-built
	// gateway, to make sure register.go did mount the route.
	eng := gin.New()
	gw, err := RegisterOpenAIGateway(eng, Deps{
		Runtime: stubRunner{},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options: Options{
			Enabled:             true,
			MaxRuns:             10,
			MaxRunsPerTenant:    2,
			RingSize:            32,
			ExpiresAfterSeconds: 60,
			MaxRequestBodyBytes: 1024,
			ErrorDetailMode:     "dev",
		},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(gw.Shutdown)

	mounted := false
	for _, r := range eng.Routes() {
		if r.Method == http.MethodGet && r.Path == "/v1/runs/:id/events" {
			mounted = true
			break
		}
	}
	if !mounted {
		t.Errorf("expected GET /v1/runs/:id/events to be mounted")
	}
}

func TestRunsEvents_PersistentHubServesAfterRestart(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "rh.db")

	// Phase 1 — bring up a PersistentHub, create a run, publish events,
	// crash-close the store (skip Shutdown so the DB row stays as
	// in_progress, mimicking a process kill).
	s1, err := store.Open(store.Config{DSN: dbPath})
	if err != nil {
		t.Fatalf("open s1: %v", err)
	}
	hub1, err := runhub.NewPersistentHub(runhub.PersistentConfig{
		Config: runhub.Config{RingSize: 4},
		Store:  s1,
	})
	if err != nil {
		t.Fatalf("hub1: %v", err)
	}

	run, _ := hub1.Create(runhub.CreateOptions{TenantID: "localhost"})
	run.SetStatus(runhub.RunStatusInProgress)
	for i := 0; i < 8; i++ {
		run.Publish("chunk", []byte(`{"i":`+strconv.Itoa(i)+`}`))
	}
	runID := run.ID
	// Async batch writer — flush before "crash" so the persisted state
	// reflects everything we published. A real crash MIGHT lose the last
	// batch; this test asserts the steady-state revival path.
	hub1.Flush()
	if err := s1.Close(); err != nil {
		t.Fatalf("crash-close s1: %v", err)
	}

	// Phase 2 — fresh gateway, fresh hub against the SAME DSN. Reconnect
	// endpoint must serve the historical events from the store.
	s2, err := store.Open(store.Config{DSN: dbPath})
	if err != nil {
		t.Fatalf("reopen s2: %v", err)
	}
	hub2, err := runhub.NewPersistentHub(runhub.PersistentConfig{
		Config: runhub.Config{RingSize: 4},
		Store:  s2,
	})
	if err != nil {
		t.Fatalf("hub2: %v", err)
	}
	_, eng := newReconnectTestGateway(t, hub2)

	// Mark terminal so the channel closes after backfill (otherwise the
	// reconnect handler would tail-wait forever).
	hub2.Finish(runID, runhub.RunStatusCompleted)

	ids, body, status := fetchEvents(t, eng, runID, 0)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if len(ids) != 8 {
		t.Fatalf("expected 8 events from store after restart, got %d", len(ids))
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("expected [DONE] sentinel after Finish, got: %s", body)
	}
}

// Sanity: the response Content-Type for OK paths should be SSE.
func TestRunsEvents_OKContentTypeIsSSE(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryReconnectGateway(t, 64)
	run, _ := gw.hub.Create(runhub.CreateOptions{TenantID: "localhost"})
	run.Publish("chunk", []byte("x"))
	gw.hub.Finish(run.ID, runhub.RunStatusCompleted)

	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+run.ID+"/events", nil)
	rec := httptest.NewRecorder()
	eng.ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}

// Ensure that the JSON we serialize on 410 actually parses as JSON so
// SDKs that decode the error body don't choke.
func TestRunsEvents_410BodyIsJSON(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryReconnectGateway(t, 4)
	run, _ := gw.hub.Create(runhub.CreateOptions{TenantID: "localhost"})
	for i := 0; i < 20; i++ {
		run.Publish("chunk", []byte("x"))
	}
	gw.hub.Finish(run.ID, runhub.RunStatusCompleted)

	_, body, status := fetchEvents(t, eng, run.ID, 5)
	if status != http.StatusGone {
		t.Fatalf("status = %d, want 410", status)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("410 body should be JSON, got err=%v body=%s", err, body)
	}
	errObj, _ := got["error"].(map[string]any)
	if errObj["code"] != "event_replay_unrecoverable" {
		t.Errorf("error.code = %v, want event_replay_unrecoverable", errObj["code"])
	}
}
