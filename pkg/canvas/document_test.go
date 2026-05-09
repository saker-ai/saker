package canvas

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRawCanvas(t *testing.T, dataDir, threadID string, body string) string {
	t.Helper()
	dir := filepath.Join(dataDir, "canvas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, threadID+".json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoadReturnsEmptyWhenFileMissing(t *testing.T) {
	t.Parallel()
	doc, err := Load(t.TempDir(), "missing")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if doc == nil || len(doc.Nodes) != 0 || len(doc.Edges) != 0 {
		t.Fatalf("expected empty doc, got %+v", doc)
	}
}

func TestLoadParsesNodesAndEdges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRawCanvas(t, dir, "t1", `{
		"nodes": [
			{"id":"n1","type":"prompt","position":{"x":1,"y":2},"data":{"nodeType":"prompt","label":"hi"}},
			{"id":"n2","type":"imageGen","position":{"x":3,"y":4},"data":{"nodeType":"imageGen","prompt":"a cat"}}
		],
		"edges": [
			{"id":"e1","source":"n1","target":"n2","type":"flow"}
		],
		"viewport": {"x": 10, "y": 20, "zoom": 1}
	}`)

	doc, err := Load(dir, "t1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(doc.Nodes) != 2 || len(doc.Edges) != 1 {
		t.Fatalf("unexpected counts: %+v", doc)
	}
	if doc.Nodes[1].NodeType() != "imageGen" {
		t.Fatalf("expected imageGen, got %q", doc.Nodes[1].NodeType())
	}
	if doc.Viewport["zoom"].(float64) != 1 {
		t.Fatalf("viewport not preserved: %+v", doc.Viewport)
	}
}

func TestLoadRejectsBadThreadIDs(t *testing.T) {
	t.Parallel()
	for _, tid := range []string{"", "../etc/passwd", "a/b", "..\\b"} {
		if _, err := Load(t.TempDir(), tid); err == nil {
			t.Errorf("expected error for threadID=%q", tid)
		}
	}
}

func TestSaveIsAtomicAndReadable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	doc := &Document{
		Nodes: []*Node{{ID: "n1", Type: "prompt", Data: map[string]any{"label": "hi"}}},
		Edges: []*Edge{},
	}
	if err := Save(dir, "t1", doc); err != nil {
		t.Fatalf("save: %v", err)
	}

	// File should exist at the canonical path with 2-space indent.
	raw, err := os.ReadFile(CanvasPath(dir, "t1"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(raw), "\n  \"nodes\"") {
		t.Fatalf("expected indented JSON, got %s", raw)
	}
	// No leftover .tmp files.
	entries, _ := os.ReadDir(filepath.Join(dir, "canvas"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover tmp: %s", e.Name())
		}
	}

	// Round-trip equality.
	var back Document
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if len(back.Nodes) != 1 || back.Nodes[0].ID != "n1" {
		t.Fatalf("round-trip mismatch: %+v", back)
	}
}

func TestSaveRejectsNilDocAndBadID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := Save(dir, "t1", nil); err == nil {
		t.Fatal("expected error for nil doc")
	}
	if err := Save(dir, "../oops", &Document{}); err == nil {
		t.Fatal("expected error for bad threadID")
	}
}

func TestNodeHelpers(t *testing.T) {
	t.Parallel()
	n := &Node{Data: map[string]any{"nodeType": "imageGen", "label": "go"}}
	if n.NodeType() != "imageGen" {
		t.Fatal("NodeType")
	}
	if n.DataString("label") != "go" {
		t.Fatal("DataString")
	}
	if (*Node)(nil).NodeType() != "" || (*Node)(nil).DataString("x") != "" {
		t.Fatal("nil-safe accessors")
	}
}
