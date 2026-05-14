package openai

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/runhub"
	"github.com/gin-gonic/gin"
)

// newCancelTestGateway mounts only the cancel endpoint with a
// caller-provided hub. Auth runs in dev-bypass so the request identity
// is "localhost" — tenant tests vary the run's TenantID to exercise
// the access-control branches.
func newCancelTestGateway(t *testing.T, hub runhub.Hub) (*Gateway, *gin.Engine) {
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
	v1.DELETE("/runs/:id", gw.handleRunsCancel)
	return gw, eng
}

func newMemoryCancelGateway(t *testing.T) (*Gateway, *gin.Engine) {
	t.Helper()
	hub := runhub.NewHub(runhub.Config{
		MaxRuns:  10,
		RingSize: 64,
	})
	return newCancelTestGateway(t, hub)
}

// doCancel issues DELETE /v1/runs/:id and returns the parsed status +
// body for assertions.
func doCancel(t *testing.T, eng *gin.Engine, runID string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/v1/runs/"+runID, nil)
	rec := httptest.NewRecorder()
	eng.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// TestRunsCancel_HappyPath drives the success case:
//   - run exists, tenant matches, hub.Cancel succeeds → 204 No Content,
//     no body, run status flips to cancelling.
func TestRunsCancel_HappyPath(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryCancelGateway(t)

	// Wire a sentinel cancel func to confirm hub.Cancel propagates to
	// the producer goroutine. CreateOptions.Cancel is the runtime's
	// context cancel; in production the chat-completions handler
	// supplies it, here we observe it firing.
	var cancelled bool
	run, err := gw.hub.Create(runhub.CreateOptions{
		TenantID: "localhost",
		Cancel:   func() { cancelled = true },
	})
	if err != nil {
		t.Fatalf("hub Create: %v", err)
	}
	run.SetStatus(runhub.RunStatusInProgress)

	status, body := doCancel(t, eng, run.ID)
	if status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", status, body)
	}
	if body != "" {
		t.Errorf("expected empty body for 204, got: %q", body)
	}
	if !cancelled {
		t.Errorf("expected hub.Cancel to invoke run's CancelFunc")
	}
	// The run row itself must remain in the hub so a follow-up
	// reconnect can read the cancelled status. GC sweeps it later.
	if _, err := gw.hub.Get(run.ID); err != nil {
		t.Errorf("run should still be in hub after cancel (for terminal status read), got err=%v", err)
	}
	if got := run.Status(); got != runhub.RunStatusCancelling {
		t.Errorf("run status = %v, want cancelling", got)
	}
}

// TestRunsCancel_UnknownIDReturns404 — basic existence check; an
// unknown id returns the standard 404 envelope.
func TestRunsCancel_UnknownIDReturns404(t *testing.T) {
	t.Parallel()
	_, eng := newMemoryCancelGateway(t)

	status, body := doCancel(t, eng, "run_does_not_exist")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", status, body)
	}
	if !strings.Contains(body, "not_found_error") {
		t.Errorf("expected error.type=not_found_error in body, got: %s", body)
	}
}

// TestRunsCancel_CrossTenantReturns404 locks in the existence-leak
// guard: a run owned by tenant X must look identical to a missing run
// when probed by tenant Y. (404, never 403.)
func TestRunsCancel_CrossTenantReturns404(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryCancelGateway(t)

	// Caller (dev-bypass) is "localhost"; create run under a different
	// tenant so the access-control branch fires.
	run, _ := gw.hub.Create(runhub.CreateOptions{TenantID: "someone-else"})

	status, body := doCancel(t, eng, run.ID)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (existence-leak prevention); body=%s", status, body)
	}
	// Run should be untouched — the cancel was rejected, not silently
	// applied.
	if got := run.Status(); got == runhub.RunStatusCancelling {
		t.Errorf("cross-tenant cancel must not mutate run status, got: %v", got)
	}
}

// TestRunsCancel_AlreadyTerminalIsIdempotent — cancelling a finished
// run still returns 204. The semantic intent ("the run is no longer
// running for the caller") is satisfied either way; this matches the
// idempotent-DELETE contract REST clients expect.
func TestRunsCancel_AlreadyTerminalIsIdempotent(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryCancelGateway(t)

	run, _ := gw.hub.Create(runhub.CreateOptions{TenantID: "localhost"})
	gw.hub.Finish(run.ID, runhub.RunStatusCompleted)

	status, body := doCancel(t, eng, run.ID)
	if status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (idempotent); body=%s", status, body)
	}
}

func TestRunsCancel_EmptyTenantRejects(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryCancelGateway(t)

	run, _ := gw.hub.Create(runhub.CreateOptions{})

	status, _ := doCancel(t, eng, run.ID)
	if status != http.StatusNotFound {
		t.Errorf("empty-tenant runs must be rejected, got %d", status)
	}
}

// TestRunsCancel_RouteIsMounted is a smoke test against the real
// Register flow to confirm the DELETE route was wired.
func TestRunsCancel_RouteIsMounted(t *testing.T) {
	t.Parallel()
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
		if r.Method == http.MethodDelete && r.Path == "/v1/runs/:id" {
			mounted = true
			break
		}
	}
	if !mounted {
		t.Errorf("expected DELETE /v1/runs/:id to be mounted")
	}
}

// TestRunsCancel_TerminalStatusFlowsToReconnect end-to-end check:
// after DELETE, a follow-up GET /v1/runs/:id/events must serve the
// cancelled state through to the client. This is the *reason* the
// handler doesn't immediately Remove — clients need to observe the
// terminal event.
func TestRunsCancel_TerminalStatusFlowsToReconnect(t *testing.T) {
	t.Parallel()
	gw, eng := newMemoryCancelGateway(t)
	// Mount the reconnect endpoint on the same engine for end-to-end.
	// gin.RouterGroup.Group() inherits middleware, but calling eng.Group()
	// here creates a *fresh* group — so we re-attach authMiddleware to
	// keep tenant scoping working on the GET path.
	v1 := eng.Group("/v1")
	v1.Use(gw.authMiddleware())
	v1.GET("/runs/:id/events", gw.handleRunsEvents)

	run, _ := gw.hub.Create(runhub.CreateOptions{TenantID: "localhost"})
	run.SetStatus(runhub.RunStatusInProgress)
	run.Publish("chunk", []byte(`{"x":1}`))

	// DELETE first.
	status, body := doCancel(t, eng, run.ID)
	if status != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204; body=%s", status, body)
	}

	// Cancel doesn't close subscribers on its own (it just signals the
	// producer to stop). For the test we follow the production pattern
	// where the producer wraps up by calling Finish on its way out.
	gw.hub.Finish(run.ID, runhub.RunStatusCancelled)

	// Follow-up GET — must succeed and replay the buffered event with
	// a [DONE] sentinel.
	req := httptest.NewRequest(http.MethodGet,
		"/v1/runs/"+run.ID+"/events?last_event_id="+run.ID+":0", nil)
	rec := httptest.NewRecorder()
	// Route the GET through gin via a deadline so a hung handler
	// doesn't wedge the test forever.
	doneCh := make(chan struct{})
	go func() {
		eng.ServeHTTP(rec, req)
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect GET did not return after cancel+finish")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("reconnect status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Errorf("expected [DONE] sentinel after cancel, got: %s", rec.Body.String())
	}
}
