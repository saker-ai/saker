package apps

import (
	"strings"
	"testing"

	"github.com/saker-ai/saker/pkg/canvas"
)

func TestExtractInputsEmpty(t *testing.T) {
	t.Parallel()
	if got := ExtractInputs(nil); got != nil {
		t.Fatalf("nil doc: expected nil, got %+v", got)
	}
	if got := ExtractInputs(&canvas.Document{}); len(got) != 0 {
		t.Fatalf("empty doc: expected 0, got %+v", got)
	}
}

func TestExtractInputsHappyPath(t *testing.T) {
	t.Parallel()
	doc := &canvas.Document{Nodes: []*canvas.Node{
		{ID: "i1", Data: map[string]any{
			"nodeType":     "appInput",
			"appVariable":  "topic",
			"label":        "Topic",
			"appFieldType": "text",
			"appRequired":  true,
			"appDefault":   "panda",
		}},
		{ID: "i2", Data: map[string]any{
			"nodeType":     "appInput",
			"appVariable":  "tone",
			"label":        "Tone",
			"appFieldType": "select",
			"appOptions":   []any{"formal", "casual"},
		}},
		{ID: "i3", Data: map[string]any{
			"nodeType":     "appInput",
			"appVariable":  "score",
			"appFieldType": "number",
			"appMin":       0.0,
			"appMax":       100.0,
		}},
		// missing variable: must be skipped
		{ID: "i4", Data: map[string]any{
			"nodeType":     "appInput",
			"appFieldType": "text",
		}},
	}}

	got := ExtractInputs(doc)
	if len(got) != 3 {
		t.Fatalf("expected 3 fields (i4 skipped), got %d: %+v", len(got), got)
	}
	if got[0].Variable != "topic" || got[0].Required != true || got[0].Default != "panda" {
		t.Errorf("i1 mismatch: %+v", got[0])
	}
	if got[1].Type != "select" || len(got[1].Options) != 2 || got[1].Options[1] != "casual" {
		t.Errorf("i2 mismatch: %+v", got[1])
	}
	if got[2].Min == nil || *got[2].Min != 0 || got[2].Max == nil || *got[2].Max != 100 {
		t.Errorf("i3 mismatch: %+v", got[2])
	}
}

func TestExtractInputsDefaultsType(t *testing.T) {
	t.Parallel()
	doc := &canvas.Document{Nodes: []*canvas.Node{
		{ID: "i1", Data: map[string]any{
			"nodeType":    "appInput",
			"appVariable": "x",
		}},
	}}
	got := ExtractInputs(doc)
	if len(got) != 1 || got[0].Type != "text" {
		t.Fatalf("expected default type=text, got %+v", got)
	}
}

func TestExtractOutputsHappyPath(t *testing.T) {
	t.Parallel()
	doc := &canvas.Document{
		Nodes: []*canvas.Node{
			{ID: "gen1", Data: map[string]any{"nodeType": "imageGen"}},
			{ID: "out1", Data: map[string]any{
				"nodeType": "appOutput",
				"label":    "Result",
			}},
			// Output with no upstream: SourceRef empty, Kind defaults text.
			{ID: "outOrphan", Data: map[string]any{
				"nodeType": "appOutput",
			}},
		},
		Edges: []*canvas.Edge{
			{ID: "e1", Source: "gen1", Target: "out1"},
		},
	}
	got := ExtractOutputs(doc)
	if len(got) != 2 {
		t.Fatalf("expected 2 outputs, got %d: %+v", len(got), got)
	}

	// Find each by NodeID; ordering follows doc order.
	var wired, orphan AppOutputField
	for _, o := range got {
		switch o.NodeID {
		case "out1":
			wired = o
		case "outOrphan":
			orphan = o
		}
	}
	if wired.SourceRef != "gen1" {
		t.Errorf("wired SourceRef=%q, want gen1", wired.SourceRef)
	}
	if wired.Kind != "image" {
		t.Errorf("wired Kind=%q, want image (inferred from imageGen)", wired.Kind)
	}
	if orphan.SourceRef != "" {
		t.Errorf("orphan SourceRef=%q, want empty", orphan.SourceRef)
	}
	if orphan.Kind != "text" {
		t.Errorf("orphan Kind=%q, want text (default)", orphan.Kind)
	}
}

func TestExtractOutputsExplicitKindOverridesInference(t *testing.T) {
	t.Parallel()
	doc := &canvas.Document{
		Nodes: []*canvas.Node{
			{ID: "gen1", Data: map[string]any{"nodeType": "imageGen"}},
			{ID: "out1", Data: map[string]any{
				"nodeType":      "appOutput",
				"appOutputKind": "video",
			}},
		},
		Edges: []*canvas.Edge{
			{ID: "e1", Source: "gen1", Target: "out1"},
		},
	}
	got := ExtractOutputs(doc)
	if len(got) != 1 || got[0].Kind != "video" {
		t.Fatalf("explicit appOutputKind should win, got %+v", got)
	}
}

func TestValidateInputsHappyPath(t *testing.T) {
	t.Parallel()
	fields := []AppInputField{
		{Variable: "topic", Type: "text", Required: true},
		{Variable: "tone", Type: "select", Options: []string{"formal", "casual"}},
		{Variable: "score", Type: "number"},
	}
	if err := ValidateInputs(fields, map[string]any{
		"topic": "panda",
		"tone":  "formal",
		"score": 42,
	}); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}

func TestValidateInputsRequiredMissing(t *testing.T) {
	t.Parallel()
	fields := []AppInputField{
		{Variable: "topic", Type: "text", Required: true},
	}
	err := ValidateInputs(fields, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "topic is required") {
		t.Fatalf("expected required error, got %v", err)
	}
}

func TestValidateInputsRequiredEmptyString(t *testing.T) {
	t.Parallel()
	fields := []AppInputField{
		{Variable: "topic", Type: "text", Required: true},
	}
	err := ValidateInputs(fields, map[string]any{"topic": "  "})
	if err == nil || !strings.Contains(err.Error(), "topic is required") {
		t.Fatalf("blank string should fail required: %v", err)
	}
}

func TestValidateInputsSelectInvalid(t *testing.T) {
	t.Parallel()
	fields := []AppInputField{
		{Variable: "tone", Type: "select", Options: []string{"a", "b"}},
	}
	err := ValidateInputs(fields, map[string]any{"tone": "z"})
	if err == nil || !strings.Contains(err.Error(), "tone must be one of") {
		t.Fatalf("select error mismatch: %v", err)
	}
}

func TestValidateInputsNumberInvalid(t *testing.T) {
	t.Parallel()
	fields := []AppInputField{{Variable: "n", Type: "number"}}
	err := ValidateInputs(fields, map[string]any{"n": "not-a-number"})
	if err == nil || !strings.Contains(err.Error(), "n must be a number") {
		t.Fatalf("number error mismatch: %v", err)
	}

	// Numeric-string is OK.
	if err := ValidateInputs(fields, map[string]any{"n": "3.14"}); err != nil {
		t.Fatalf("numeric string should pass: %v", err)
	}
}

func TestValidateInputsAggregatesProblems(t *testing.T) {
	t.Parallel()
	fields := []AppInputField{
		{Variable: "topic", Type: "text", Required: true},
		{Variable: "tone", Type: "select", Options: []string{"formal", "casual"}},
	}
	err := ValidateInputs(fields, map[string]any{"tone": "weird"})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "topic is required") || !strings.Contains(msg, "tone must be one of") {
		t.Fatalf("expected both problems, got %q", msg)
	}
}
