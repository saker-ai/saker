package server

import (
	"strings"
	"testing"
)

// TestStripCanvasNodeDataURIsScrubsSourceURL regresses the eddaff17 incident:
// a sketch node with a base64 data: sourceUrl is what poisoned the LLM
// context. The canonical reference (mediaPath) must survive untouched.
func TestStripCanvasNodeDataURIsScrubsSourceURL(t *testing.T) {
	t.Parallel()

	bigPayload := strings.Repeat("A", 4096)
	dataURI := "data:image/png;base64," + bigPayload

	nodes := []any{
		map[string]any{
			"id":   "node_3",
			"type": "sketch",
			"data": map[string]any{
				"nodeType":  "sketch",
				"label":     "画笔涂鸦",
				"mediaPath": "/abs/path/to/canvas-media/sketch.png",
				"mediaUrl":  "/api/files/canvas-media/sketch.png",
				"sourceUrl": dataURI,
			},
		},
	}

	out := stripCanvasNodeDataURIs(nodes)
	got, ok := out.([]any)
	if !ok || len(got) != 1 {
		t.Fatalf("expected 1-elem []any, got %T %#v", out, out)
	}
	data := got[0].(map[string]any)["data"].(map[string]any)

	if got := data["sourceUrl"]; got != "" {
		t.Fatalf("sourceUrl must be scrubbed, got %q", got)
	}
	if got := data["mediaPath"]; got != "/abs/path/to/canvas-media/sketch.png" {
		t.Fatalf("mediaPath must survive untouched, got %q", got)
	}
	// mediaUrl is a normal HTTP path, NOT a data: URI, so it must survive.
	if got := data["mediaUrl"]; got != "/api/files/canvas-media/sketch.png" {
		t.Fatalf("plain http mediaUrl must survive untouched, got %q", got)
	}
	if got := data["label"]; got != "画笔涂鸦" {
		t.Fatalf("label must survive untouched, got %q", got)
	}
}

// TestStripCanvasNodeDataURIsScrubsMediaURL covers the case where the
// frontend mistakenly inlines bytes into mediaUrl (not just sourceUrl).
func TestStripCanvasNodeDataURIsScrubsMediaURL(t *testing.T) {
	t.Parallel()
	nodes := []any{
		map[string]any{
			"id": "n",
			"data": map[string]any{
				"mediaUrl": "data:image/png;base64,QQQQ",
			},
		},
	}
	got := stripCanvasNodeDataURIs(nodes).([]any)
	data := got[0].(map[string]any)["data"].(map[string]any)
	if data["mediaUrl"] != "" {
		t.Fatalf("data:-prefixed mediaUrl must be scrubbed, got %q", data["mediaUrl"])
	}
}

// TestStripCanvasNodeDataURIsLeavesFreeTextAlone ensures we do NOT scrub
// label/prompt/content even if they happen to start with "data:" — those go
// through the LLM-summary scrub instead, and the user might legitimately type
// the word in natural language.
func TestStripCanvasNodeDataURIsLeavesFreeTextAlone(t *testing.T) {
	t.Parallel()
	nodes := []any{
		map[string]any{
			"id": "n",
			"data": map[string]any{
				"label":   "data: how do I parse this URI?",
				"prompt":  "data:image/png;base64,XXX",
				"content": "data:application/json,{}",
			},
		},
	}
	got := stripCanvasNodeDataURIs(nodes).([]any)
	data := got[0].(map[string]any)["data"].(map[string]any)
	for _, k := range []string{"label", "prompt", "content"} {
		v, _ := data[k].(string)
		if !strings.HasPrefix(v, "data:") {
			t.Fatalf("%s must NOT be scrubbed by save-time helper, got %q", k, v)
		}
	}
}

// TestStripCanvasNodeDataURIsNilSafe makes sure missing/nil inputs don't
// panic — handler params can omit the nodes field entirely.
func TestStripCanvasNodeDataURIsNilSafe(t *testing.T) {
	t.Parallel()
	if got := stripCanvasNodeDataURIs(nil); got != nil {
		t.Fatalf("nil in -> nil out, got %#v", got)
	}
}

// TestStripCanvasNodeDataURIsHandlesNonDataURIStrings makes sure normal
// strings survive even on the scrubbed fields.
func TestStripCanvasNodeDataURIsHandlesNonDataURIStrings(t *testing.T) {
	t.Parallel()
	nodes := []any{
		map[string]any{"data": map[string]any{"sourceUrl": "https://example.com/a.png"}},
	}
	got := stripCanvasNodeDataURIs(nodes).([]any)
	data := got[0].(map[string]any)["data"].(map[string]any)
	if data["sourceUrl"] != "https://example.com/a.png" {
		t.Fatalf("https sourceUrl must survive, got %q", data["sourceUrl"])
	}
}
