package openai

import (
	"bytes"
	"strings"
	"testing"

	"github.com/saker-ai/saker/pkg/runhub"
)

func TestParseIncludeUsage(t *testing.T) {
	cases := []struct {
		name string
		opts map[string]any
		want bool
	}{
		{"nil opts", nil, false},
		{"empty opts", map[string]any{}, false},
		{"missing key", map[string]any{"other": true}, false},
		{"explicit true", map[string]any{"include_usage": true}, true},
		{"explicit false", map[string]any{"include_usage": false}, false},
		{"non-bool value", map[string]any{"include_usage": "yes"}, false},
		{"numeric value", map[string]any{"include_usage": 1}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseIncludeUsage(c.opts); got != c.want {
				t.Errorf("parseIncludeUsage(%v) = %v, want %v", c.opts, got, c.want)
			}
		})
	}
}

func TestMakeChatChunkID(t *testing.T) {
	cases := []struct {
		runID string
		want  string
	}{
		{"run_abc123", "chatcmpl-abc123"},
		{"abc123", "chatcmpl-abc123"}, // strips only when prefix matches
		{"run_run_xx", "chatcmpl-run_xx"},
		{"", "chatcmpl-"},
	}
	for _, c := range cases {
		t.Run(c.runID, func(t *testing.T) {
			if got := makeChatChunkID(c.runID); got != c.want {
				t.Errorf("makeChatChunkID(%q) = %q, want %q", c.runID, got, c.want)
			}
		})
	}
}

func TestWriteChunkSSE(t *testing.T) {
	var buf bytes.Buffer
	evt := runhub.Event{Seq: 7, Type: "chunk", Data: []byte(`{"hello":"world"}`)}
	const runID = "run_abc123"
	if err := writeChunkSSE(&buf, runID, evt); err != nil {
		t.Fatalf("writeChunkSSE: %v", err)
	}
	out := buf.String()
	// Wire format is the qualified `<run_id>:<seq>` cursor — see
	// writeChunkSSE doc for why. The legacy bare-int format is gone.
	if !strings.Contains(out, "id: run_abc123:7\n") {
		t.Errorf("missing qualified id line: %q", out)
	}
	if !strings.Contains(out, `data: {"hello":"world"}`) {
		t.Errorf("missing data line: %q", out)
	}
	if !strings.HasSuffix(out, "\n\n") {
		t.Errorf("expected SSE frame to end with double newline: %q", out)
	}
}
