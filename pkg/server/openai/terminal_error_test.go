package openai

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/runhub"
)

// TestWriteTerminalErrorIfNeeded_Mapping is the unit-level lock for the
// (status → error code) mapping spelled out in the plan §E.4. The full
// SSE wire format and the [DONE] sentinel ordering is covered separately
// by the reconnect-handler integration tests below.
func TestWriteTerminalErrorIfNeeded_Mapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status   runhub.RunStatus
		wantCode string // "" means: no frame should be emitted
	}{
		{runhub.RunStatusCancelled, "run_cancelled"},
		{runhub.RunStatusExpired, "run_expired"},
		{runhub.RunStatusFailed, "run_failed"},
		{runhub.RunStatusCompleted, ""},
		{runhub.RunStatusInProgress, ""}, // defensive: shouldn't happen on chan close
		{runhub.RunStatusQueued, ""},     // defensive: shouldn't happen on chan close
	}
	for _, c := range cases {
		t.Run(string(c.status), func(t *testing.T) {
			hub := runhub.NewHub(runhub.Config{RingSize: 8})
			t.Cleanup(hub.Shutdown)
			run, err := hub.Create(runhub.CreateOptions{TenantID: "t1"})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			run.SetStatus(c.status)

			var buf bytes.Buffer
			if err := writeTerminalErrorIfNeeded(&buf, run); err != nil {
				t.Fatalf("writeTerminalErrorIfNeeded: %v", err)
			}
			out := buf.String()

			if c.wantCode == "" {
				if out != "" {
					t.Fatalf("status=%s wrote %q, want empty (clean termination)", c.status, out)
				}
				return
			}
			if !strings.HasPrefix(out, "event: error\ndata: ") {
				t.Errorf("status=%s wrong frame prefix: %q", c.status, out)
			}
			if !strings.HasSuffix(out, "\n\n") {
				t.Errorf("status=%s frame missing terminator: %q", c.status, out)
			}
			payload := strings.TrimSuffix(strings.TrimPrefix(out, "event: error\ndata: "), "\n\n")
			var env ErrorEnvelope
			if err := json.Unmarshal([]byte(payload), &env); err != nil {
				t.Fatalf("status=%s JSON decode failed: %v (payload=%s)", c.status, err, payload)
			}
			if env.Error.Code != c.wantCode {
				t.Errorf("status=%s code=%q, want %q", c.status, env.Error.Code, c.wantCode)
			}
			if env.Error.Type != ErrTypeAPI {
				t.Errorf("status=%s type=%q, want %q", c.status, env.Error.Type, ErrTypeAPI)
			}
			if env.Error.Message == "" {
				t.Errorf("status=%s empty error message", c.status)
			}
		})
	}
}

// TestWriteTerminalErrorIfNeeded_NilRun is the defensive-nil branch —
// callers shouldn't pass nil but we don't want a panic if they do.
func TestWriteTerminalErrorIfNeeded_NilRun(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := writeTerminalErrorIfNeeded(&buf, nil); err != nil {
		t.Fatalf("nil run should be no-op, got err=%v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("nil run should write nothing, got %q", buf.String())
	}
}

// TestRunsEvents_TerminalErrorFrames covers the three non-clean terminal
// states end-to-end through the reconnect handler: each must emit the
// `event: error\ndata: {ErrorEnvelope}\n\n` frame BEFORE `data: [DONE]`,
// and the error code must match the plan-defined mapping. The completed
// path is the negative case — no error frame should appear.
func TestRunsEvents_TerminalErrorFrames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		status   runhub.RunStatus
		wantCode string // "" means: assert NO error frame
	}{
		{"cancelled", runhub.RunStatusCancelled, "run_cancelled"},
		{"expired", runhub.RunStatusExpired, "run_expired"},
		{"failed", runhub.RunStatusFailed, "run_failed"},
		{"completed", runhub.RunStatusCompleted, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gw, eng := newMemoryReconnectGateway(t, 64)

			run, err := gw.hub.Create(runhub.CreateOptions{TenantID: "localhost"})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			run.Publish("chunk", []byte(`{"x":1}`))
			gw.hub.Finish(run.ID, c.status)

			req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+run.ID+"/events", nil)
			rec := httptest.NewRecorder()
			eng.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()

			doneIdx := strings.Index(body, "data: [DONE]")
			if doneIdx < 0 {
				t.Fatalf("missing [DONE] sentinel; body=%s", body)
			}

			if c.wantCode == "" {
				if strings.Contains(body, "event: error") {
					t.Errorf("clean termination should NOT emit error frame; body=%s", body)
				}
				return
			}

			errIdx := strings.Index(body, "event: error")
			if errIdx < 0 {
				t.Fatalf("missing event:error frame for status=%s; body=%s", c.status, body)
			}
			if errIdx >= doneIdx {
				t.Errorf("event:error must appear BEFORE [DONE]; errIdx=%d doneIdx=%d body=%s",
					errIdx, doneIdx, body)
			}
			if !strings.Contains(body, `"code":"`+c.wantCode+`"`) {
				t.Errorf("expected error.code=%q in body; body=%s", c.wantCode, body)
			}
			if !strings.Contains(body, `"type":"`+ErrTypeAPI+`"`) {
				t.Errorf("expected error.type=%q in body; body=%s", ErrTypeAPI, body)
			}
		})
	}
}
