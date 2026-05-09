package canvas

import (
	"strings"
	"testing"
)

func TestMarkRunningAndPending(t *testing.T) {
	t.Parallel()
	n := &Node{ID: "g", Data: map[string]any{"status": NodeStatusError, "error": "old"}}
	MarkRunning(n)
	if n.Data["status"] != NodeStatusRunning || n.Data["generating"] != true {
		t.Fatalf("after MarkRunning: %+v", n.Data)
	}
	if _, hasErr := n.Data["error"]; hasErr {
		t.Fatal("MarkRunning should clear error")
	}
	if _, hasStart := n.Data["startTime"]; !hasStart {
		t.Fatal("MarkRunning should set startTime")
	}

	MarkPending(n)
	if n.Data["status"] != NodeStatusPending || n.Data["generating"] != false {
		t.Fatalf("after MarkPending: %+v", n.Data)
	}
	if _, hasEnd := n.Data["endTime"]; !hasEnd {
		t.Fatal("MarkPending should set endTime")
	}
}

func TestMarkErrorRecordsLastErrorParams(t *testing.T) {
	t.Parallel()
	n := &Node{ID: "g", Data: map[string]any{}}
	MarkError(n, "boom", map[string]any{"prompt": "x"})
	if n.Data["status"] != NodeStatusError || n.Data["error"] != "boom" {
		t.Fatalf("after MarkError: %+v", n.Data)
	}
	raw, ok := n.Data["lastErrorParams"].(string)
	if !ok || !strings.Contains(raw, `"prompt":"x"`) {
		t.Fatalf("lastErrorParams: %v", n.Data["lastErrorParams"])
	}
}

func TestMarkHelpersAreNilSafe(t *testing.T) {
	t.Parallel()
	MarkRunning(nil)
	MarkPending(nil)
	MarkError(nil, "x", nil)

	n := &Node{ID: "g"} // nil data
	MarkRunning(n)
	if n.Data == nil {
		t.Fatal("Mark should initialise nil data map")
	}
}

func TestAppendResultNodeAndEdge(t *testing.T) {
	t.Parallel()
	doc := &Document{}
	gen := &Node{ID: "g", Position: Position{X: 100, Y: 50}, Data: map[string]any{"prompt": "a cat"}}
	doc.Nodes = append(doc.Nodes, gen)

	id := AppendResultNode(doc, gen, "image", "/cached/a.png", "/disk/a.png", "https://orig/x.png", "a cat")
	if id == "" || len(doc.Nodes) != 2 {
		t.Fatalf("AppendResultNode: id=%q nodes=%d", id, len(doc.Nodes))
	}
	res := doc.Nodes[1]
	if res.Position.X != 450 || res.Position.Y != 50 {
		t.Fatalf("position offset wrong: %+v", res.Position)
	}
	if res.Data["mediaUrl"] != "/cached/a.png" || res.Data["mediaPath"] != "/disk/a.png" {
		t.Fatalf("data: %+v", res.Data)
	}
	if res.Data["sourceUrl"] != "https://orig/x.png" {
		t.Fatalf("sourceUrl missing: %+v", res.Data)
	}

	edgeID := AppendFlowEdge(doc, gen.ID, id)
	if edgeID == "" || len(doc.Edges) != 1 {
		t.Fatalf("AppendFlowEdge: id=%q edges=%d", edgeID, len(doc.Edges))
	}
	if doc.Edges[0].Type != EdgeFlow {
		t.Fatalf("expected flow edge, got %q", doc.Edges[0].Type)
	}
}

func TestAppendResultNodeOmitsEmptyOptionalFields(t *testing.T) {
	t.Parallel()
	doc := &Document{}
	gen := &Node{ID: "g", Data: map[string]any{}}
	doc.Nodes = append(doc.Nodes, gen)
	id := AppendResultNode(doc, gen, "image", "/a.png", "", "", "")
	res := doc.FindNode(id)
	if _, ok := res.Data["mediaPath"]; ok {
		t.Fatal("mediaPath should be omitted when empty")
	}
	if _, ok := res.Data["sourceUrl"]; ok {
		t.Fatal("sourceUrl should be omitted when empty")
	}
}

func TestAppendGenHistoryCapsAtMaxAndPrependsNewest(t *testing.T) {
	t.Parallel()
	n := &Node{ID: "g", Data: map[string]any{}}
	for i := 0; i < MaxGenHistory+5; i++ {
		AppendGenHistory(n, GenHistoryEntry{
			ID:        NewHistoryEntryID(),
			Prompt:    "p",
			MediaURL:  "/u",
			Status:    NodeStatusDone,
			CreatedAt: int64(i),
		})
	}
	hist, ok := n.Data["generationHistory"].([]map[string]any)
	if !ok {
		t.Fatalf("history type: %T", n.Data["generationHistory"])
	}
	if len(hist) != MaxGenHistory {
		t.Fatalf("want %d entries, got %d", MaxGenHistory, len(hist))
	}
	// Newest (highest createdAt) should be first.
	first := hist[0]["createdAt"].(int64)
	last := hist[len(hist)-1]["createdAt"].(int64)
	if first <= last {
		t.Fatalf("expected newest-first ordering: first=%d last=%d", first, last)
	}
	if n.Data["activeHistoryIndex"].(int) != 0 {
		t.Fatalf("activeHistoryIndex should be 0, got %v", n.Data["activeHistoryIndex"])
	}
}

func TestAppendGenHistoryAcceptsLegacyAnyArrays(t *testing.T) {
	t.Parallel()
	// A canvas saved by an older browser session can deserialise the
	// history slice as []any rather than []map[string]any. The helper
	// must coerce it back so we don't drop history on first run.
	n := &Node{ID: "g", Data: map[string]any{
		"generationHistory": []any{
			map[string]any{"id": "old", "createdAt": int64(1), "status": "done", "prompt": "p", "mediaUrl": "/o"},
		},
	}}
	AppendGenHistory(n, GenHistoryEntry{ID: "new", Prompt: "p2", Status: "done", CreatedAt: 2})
	hist, _ := n.Data["generationHistory"].([]map[string]any)
	if len(hist) != 2 || hist[0]["id"] != "new" || hist[1]["id"] != "old" {
		t.Fatalf("legacy history merge failed: %+v", hist)
	}
}

func TestAppendResultNodeRefusesUUIDMediaURL(t *testing.T) {
	t.Parallel()
	doc := &Document{}
	gen := &Node{ID: "g", Position: Position{X: 0, Y: 0}, Data: map[string]any{}}
	doc.Nodes = append(doc.Nodes, gen)

	// This is the exact bug we're guarding against — DashScope returned a
	// task UUID and it got written into mediaUrl as if it were a video URL.
	const badURL = "c424ec41-2880-4f9d-b09e-362cefa3e047"
	id := AppendResultNode(doc, gen, "video", badURL, "", "", "label")
	if id == "" {
		t.Fatal("expected an error node id even for invalid URL")
	}
	res := doc.FindNode(id)
	if res.Data["status"] != NodeStatusError {
		t.Fatalf("status = %v, want error", res.Data["status"])
	}
	if _, ok := res.Data["mediaUrl"]; ok {
		t.Fatalf("mediaUrl should not be persisted on error node, got %v", res.Data["mediaUrl"])
	}
	errMsg, _ := res.Data["error"].(string)
	if !strings.Contains(errMsg, badURL) {
		t.Fatalf("error message should reference the bad URL, got %q", errMsg)
	}
}

func TestLooksLikeMediaURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"https://example.com/x.mp4", true},
		{"http://example.com/x.png", true},
		{"data:image/png;base64,xxx", true},
		{"blob:https://x/y", true},
		{"file:///tmp/x.png", true},
		{"/api/files/abc.png", true},
		{"/cached/a.png", true},
		// Bug pattern: opaque task id with no scheme/path.
		{"c424ec41-2880-4f9d-b09e-362cefa3e047", false},
		{"task_12345", false},
		{"", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			if got := looksLikeMediaURL(c.in); got != c.want {
				t.Fatalf("looksLikeMediaURL(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestNewHistoryEntryIDIsUnique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool, 50)
	for i := 0; i < 50; i++ {
		id := NewHistoryEntryID()
		if !strings.HasPrefix(id, "gh_") {
			t.Fatalf("bad prefix: %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id: %q", id)
		}
		seen[id] = true
	}
}
