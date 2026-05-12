package openai

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPrepareSSE_HeadersSet(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	flusher := PrepareSSE(c)
	if flusher == nil {
		t.Fatal("expected non-nil flusher from gin recorder")
	}
	h := rec.Result().Header
	if got := h.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Errorf("Content-Type = %q, want SSE", got)
	}
	if h.Get("Cache-Control") == "" {
		t.Error("Cache-Control should be set")
	}
	if h.Get("X-Accel-Buffering") != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", h.Get("X-Accel-Buffering"))
	}
}

func TestWriteEvent_FullFrame(t *testing.T) {
	var buf bytes.Buffer
	err := WriteEvent(&buf, SSEEvent{
		ID:    "42",
		Event: "chunk",
		Data:  map[string]string{"hello": "world"},
		Retry: 1500,
	})
	if err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"id: 42\n", "event: chunk\n", "retry: 1500\n", `"hello":"world"`, "\n\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestWriteEvent_OmitsEmptyFields(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteEvent(&buf, SSEEvent{Data: 7}); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "id:") || strings.Contains(out, "event:") || strings.Contains(out, "retry:") {
		t.Errorf("expected only data line, got:\n%s", out)
	}
	if !strings.Contains(out, "data: 7\n\n") {
		t.Errorf("missing data line, got:\n%s", out)
	}
}

func TestWriteEvent_MarshalError(t *testing.T) {
	var buf bytes.Buffer
	// channels can't be JSON-marshaled — exercise the error path.
	err := WriteEvent(&buf, SSEEvent{Data: make(chan int)})
	if err == nil {
		t.Fatal("expected marshal error")
	}
	if !strings.Contains(err.Error(), "marshal") {
		t.Errorf("err = %v, want substring marshal", err)
	}
}

func TestWriteDone(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteDone(&buf); err != nil {
		t.Fatalf("WriteDone: %v", err)
	}
	if buf.String() != "data: [DONE]\n\n" {
		t.Errorf("got %q, want OpenAI sentinel", buf.String())
	}
}

func TestWriteComment(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteComment(&buf, "ka"); err != nil {
		t.Fatalf("WriteComment: %v", err)
	}
	if buf.String() != ": ka\n\n" {
		t.Errorf("got %q, want comment frame", buf.String())
	}
}
