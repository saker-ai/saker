package toolbuiltin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readTableDoc loads the persisted canvas back as a generic map so tests can
// poke at tableColumns/tableRows without re-deriving the schema.
func readTableDoc(t *testing.T, canvasDir, threadID string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(canvasDir, threadID+".json"))
	if err != nil {
		t.Fatalf("read canvas: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal canvas: %v", err)
	}
	return doc
}

// findTableNode pulls one node + its data map out of the persisted doc so
// tests can assert on the post-write state.
func findTableNode(t *testing.T, doc map[string]any, nodeID string) map[string]any {
	t.Helper()
	nodes, _ := doc["nodes"].([]any)
	for _, raw := range nodes {
		n, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := n["id"].(string); id == nodeID {
			data, _ := n["data"].(map[string]any)
			return data
		}
	}
	t.Fatalf("node %s not found", nodeID)
	return nil
}

// writeEmptyTableFixture builds a canvas containing one empty table node so
// each test can start from a known baseline and exercise one operation.
func writeEmptyTableFixture(t *testing.T, canvasDir, threadID, nodeID string) {
	t.Helper()
	doc := map[string]any{
		"nodes": []map[string]any{
			{
				"id":   nodeID,
				"type": "table",
				"data": map[string]any{"nodeType": "table", "label": "Test table"},
			},
		},
	}
	writeCanvasFixture(t, canvasDir, threadID, doc)
}

// TestCanvasTableWriteReplaceCreatesColumnsAndRows is the happy path for the
// "replace" operation: a fresh table gets columns + rows in one shot. We
// assert both the in-process tool result and the persisted JSON round-trip.
func TestCanvasTableWriteReplaceCreatesColumnsAndRows(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	writeEmptyTableFixture(t, canvasDir, "thread1", "node_1")

	tool := NewCanvasTableWriteTool(canvasDir)
	res, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_1",
		"operation": "replace",
		"columns": []any{
			map[string]any{"id": "scene", "name": "场号", "type": "number"},
			map[string]any{"id": "setting", "name": "景别", "type": "select", "options": []any{"内", "外"}},
			map[string]any{"id": "line", "name": "台词", "type": "longText"},
		},
		"rows": []any{
			map[string]any{"id": "row_1", "scene": 1, "setting": "内", "line": "你好世界"},
			map[string]any{"id": "row_2", "scene": 2, "setting": "外", "line": "再见"},
		},
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got %+v", res)
	}
	if !strings.Contains(res.Output, "3 columns, 2 rows") {
		t.Fatalf("expected counts in output, got: %s", res.Output)
	}

	data := findTableNode(t, readTableDoc(t, canvasDir, "thread1"), "node_1")
	cols, _ := data["tableColumns"].([]any)
	rows, _ := data["tableRows"].([]any)
	if len(cols) != 3 || len(rows) != 2 {
		t.Fatalf("expected 3 cols / 2 rows persisted, got %d / %d", len(cols), len(rows))
	}
	first, _ := rows[0].(map[string]any)
	if first["line"] != "你好世界" {
		t.Fatalf("row_1.line should round-trip exactly, got %v", first["line"])
	}
}

// TestCanvasTableWriteReplaceRequiresColumns prevents a stray "replace" with
// no columns from blanking the table. Empty columns is almost always a bug
// in the agent prompt; refuse loudly.
func TestCanvasTableWriteReplaceRequiresColumns(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	writeEmptyTableFixture(t, canvasDir, "thread1", "node_1")

	tool := NewCanvasTableWriteTool(canvasDir)
	_, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_1",
		"operation": "replace",
		"columns":   []any{},
	})
	if err == nil || !strings.Contains(err.Error(), "columns must be a non-empty array") {
		t.Fatalf("expected empty-columns error, got %v", err)
	}
}

// TestCanvasTableWriteReplaceRejectsRowOverflow caps blast radius from a
// runaway model; replacing with > canvasTableMaxRows must fail before any
// disk write so the existing data survives.
func TestCanvasTableWriteReplaceRejectsRowOverflow(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	writeEmptyTableFixture(t, canvasDir, "thread1", "node_1")

	rows := make([]any, canvasTableMaxRows+1)
	for i := range rows {
		rows[i] = map[string]any{"a": "x"}
	}
	tool := NewCanvasTableWriteTool(canvasDir)
	_, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_1",
		"operation": "replace",
		"columns":   []any{map[string]any{"id": "a", "name": "A", "type": "text"}},
		"rows":      rows,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds cap") {
		t.Fatalf("expected row-cap error, got %v", err)
	}
}

// TestCanvasTableWriteSetCellUpdatesValue exercises a single-cell mutation
// and verifies that other cells in the same row are untouched.
func TestCanvasTableWriteSetCellUpdatesValue(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	doc := map[string]any{
		"nodes": []map[string]any{
			{
				"id":   "node_1",
				"type": "table",
				"data": map[string]any{
					"nodeType": "table",
					"tableColumns": []any{
						map[string]any{"id": "scene", "name": "场号", "type": "number"},
						map[string]any{"id": "line", "name": "台词", "type": "text"},
					},
					"tableRows": []any{
						map[string]any{"id": "row_1", "scene": 1, "line": "原文"},
					},
				},
			},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasTableWriteTool(canvasDir)
	if _, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_1",
		"operation": "set_cell",
		"row_id":    "row_1",
		"column_id": "line",
		"value":     "改写后的台词",
	}); err != nil {
		t.Fatalf("set_cell: %v", err)
	}

	data := findTableNode(t, readTableDoc(t, canvasDir, "thread1"), "node_1")
	rows, _ := data["tableRows"].([]any)
	row, _ := rows[0].(map[string]any)
	if row["line"] != "改写后的台词" {
		t.Fatalf("line not updated, got %v", row["line"])
	}
	// Number column survived even though we didn't touch it.
	if row["scene"] == nil {
		t.Fatalf("untouched cell should survive, got %+v", row)
	}
}

// TestCanvasTableWriteSetCellRejectsUnknownColumn defends the agent from
// silently dropping cells under a typo'd column id. Better to surface the
// mistake than let the model believe the write happened.
func TestCanvasTableWriteSetCellRejectsUnknownColumn(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	doc := map[string]any{
		"nodes": []map[string]any{
			{
				"id":   "node_1",
				"type": "table",
				"data": map[string]any{
					"nodeType":     "table",
					"tableColumns": []any{map[string]any{"id": "a", "name": "A", "type": "text"}},
					"tableRows":    []any{map[string]any{"id": "row_1", "a": "x"}},
				},
			},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasTableWriteTool(canvasDir)
	_, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_1",
		"operation": "set_cell",
		"row_id":    "row_1",
		"column_id": "nope",
		"value":     "x",
	})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected unknown-column error, got %v", err)
	}
}

// TestCanvasTableWriteAddRowAutoAssignsID verifies that omitting row_id
// auto-generates a non-conflicting id and appends to the bottom by default.
func TestCanvasTableWriteAddRowAutoAssignsID(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	doc := map[string]any{
		"nodes": []map[string]any{
			{
				"id":   "node_1",
				"type": "table",
				"data": map[string]any{
					"nodeType":     "table",
					"tableColumns": []any{map[string]any{"id": "a", "name": "A", "type": "text"}},
					"tableRows":    []any{map[string]any{"id": "row_1", "a": "first"}},
				},
			},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasTableWriteTool(canvasDir)
	if _, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_1",
		"operation": "add_row",
		"values":    map[string]any{"a": "second"},
	}); err != nil {
		t.Fatalf("add_row: %v", err)
	}

	data := findTableNode(t, readTableDoc(t, canvasDir, "thread1"), "node_1")
	rows, _ := data["tableRows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows after add, got %d", len(rows))
	}
	last, _ := rows[1].(map[string]any)
	if last["a"] != "second" {
		t.Fatalf("appended row should hold value, got %+v", last)
	}
	if id, _ := last["id"].(string); id == "" || id == "row_1" {
		t.Fatalf("auto-id must be unique and non-empty, got %q", id)
	}
}

// TestCanvasTableWriteAddRowPositionTop puts the new row first; previous
// rows shift down by one but their ids stay stable.
func TestCanvasTableWriteAddRowPositionTop(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	doc := map[string]any{
		"nodes": []map[string]any{
			{
				"id":   "node_1",
				"type": "table",
				"data": map[string]any{
					"nodeType":     "table",
					"tableColumns": []any{map[string]any{"id": "a", "name": "A", "type": "text"}},
					"tableRows": []any{
						map[string]any{"id": "row_a", "a": "old"},
					},
				},
			},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasTableWriteTool(canvasDir)
	if _, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_1",
		"operation": "add_row",
		"row_id":    "row_new",
		"position":  "top",
		"values":    map[string]any{"a": "new"},
	}); err != nil {
		t.Fatalf("add_row top: %v", err)
	}

	data := findTableNode(t, readTableDoc(t, canvasDir, "thread1"), "node_1")
	rows, _ := data["tableRows"].([]any)
	first, _ := rows[0].(map[string]any)
	if first["id"] != "row_new" {
		t.Fatalf("row_new should be first, got %+v", first)
	}
}

// TestCanvasTableWriteAddRowPositionAfter inserts after an explicit anchor
// and rejects an unknown anchor so the agent can't silently no-op.
func TestCanvasTableWriteAddRowPositionAfter(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	doc := map[string]any{
		"nodes": []map[string]any{
			{
				"id":   "node_1",
				"type": "table",
				"data": map[string]any{
					"nodeType":     "table",
					"tableColumns": []any{map[string]any{"id": "a", "name": "A", "type": "text"}},
					"tableRows": []any{
						map[string]any{"id": "row_a", "a": "1"},
						map[string]any{"id": "row_b", "a": "2"},
					},
				},
			},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasTableWriteTool(canvasDir)
	if _, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_1",
		"operation": "add_row",
		"row_id":    "row_mid",
		"position":  "after:row_a",
		"values":    map[string]any{"a": "1.5"},
	}); err != nil {
		t.Fatalf("add_row after: %v", err)
	}
	data := findTableNode(t, readTableDoc(t, canvasDir, "thread1"), "node_1")
	rows, _ := data["tableRows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	mid, _ := rows[1].(map[string]any)
	if mid["id"] != "row_mid" {
		t.Fatalf("row_mid should be at index 1, got order: %+v", rows)
	}

	// Unknown anchor must error rather than silently fall through.
	_, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_1",
		"operation": "add_row",
		"position":  "after:row_zzz",
		"values":    map[string]any{"a": "x"},
	})
	if err == nil || !strings.Contains(err.Error(), "anchor row") {
		t.Fatalf("expected unknown-anchor error, got %v", err)
	}
}

// TestCanvasTableWriteDeleteRowRemovesByID is the happy path; after delete
// the row id is gone and the remaining rows keep their order.
func TestCanvasTableWriteDeleteRowRemovesByID(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	doc := map[string]any{
		"nodes": []map[string]any{
			{
				"id":   "node_1",
				"type": "table",
				"data": map[string]any{
					"nodeType":     "table",
					"tableColumns": []any{map[string]any{"id": "a", "name": "A", "type": "text"}},
					"tableRows": []any{
						map[string]any{"id": "row_a", "a": "1"},
						map[string]any{"id": "row_b", "a": "2"},
						map[string]any{"id": "row_c", "a": "3"},
					},
				},
			},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasTableWriteTool(canvasDir)
	if _, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_1",
		"operation": "delete_row",
		"row_id":    "row_b",
	}); err != nil {
		t.Fatalf("delete_row: %v", err)
	}

	data := findTableNode(t, readTableDoc(t, canvasDir, "thread1"), "node_1")
	rows, _ := data["tableRows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows after delete, got %d", len(rows))
	}
	for _, raw := range rows {
		r, _ := raw.(map[string]any)
		if r["id"] == "row_b" {
			t.Fatalf("row_b should be gone, got rows: %+v", rows)
		}
	}
}

// TestCanvasTableWriteRefusesNonTableNode keeps the tool from clobbering
// arbitrary node types — only nodeType=="table" is fair game.
func TestCanvasTableWriteRefusesNonTableNode(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	doc := map[string]any{
		"nodes": []map[string]any{
			{"id": "node_1", "type": "prompt", "data": map[string]any{"nodeType": "prompt"}},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasTableWriteTool(canvasDir)
	_, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_1",
		"operation": "replace",
		"columns":   []any{map[string]any{"id": "a", "name": "A", "type": "text"}},
		"rows":      []any{},
	})
	if err == nil || !strings.Contains(err.Error(), "is not a table") {
		t.Fatalf("expected non-table refusal, got %v", err)
	}
}

// TestCanvasTableWriteMissingNode reports the canonical "not found" so the
// agent can pivot to canvas_list_nodes instead of retrying blindly.
func TestCanvasTableWriteMissingNode(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	writeEmptyTableFixture(t, canvasDir, "thread1", "node_1")

	tool := NewCanvasTableWriteTool(canvasDir)
	_, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_999",
		"operation": "replace",
		"columns":   []any{map[string]any{"id": "a", "name": "A", "type": "text"}},
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected node-not-found error, got %v", err)
	}
}

// TestCanvasTableWriteRejectsBadIDs blocks path traversal via thread_id /
// node_id — defense in depth even though the upstream RPC layer also
// validates.
func TestCanvasTableWriteRejectsBadIDs(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	tool := NewCanvasTableWriteTool(canvasDir)
	for _, tc := range []struct{ thread, node string }{
		{"../etc/passwd", "node_1"},
		{"thread1", "../node"},
	} {
		_, err := tool.Execute(context.Background(), map[string]any{
			"thread_id": tc.thread,
			"node_id":   tc.node,
			"operation": "replace",
			"columns":   []any{map[string]any{"id": "a", "name": "A", "type": "text"}},
		})
		if err == nil {
			t.Fatalf("expected rejection for thread=%q node=%q", tc.thread, tc.node)
		}
	}
}

// TestCanvasTableWriteUsesContextThreadID lets the runtime auto-inject the
// thread, mirroring how canvas_get_node and friends work.
func TestCanvasTableWriteUsesContextThreadID(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	writeEmptyTableFixture(t, canvasDir, "ctx_thread", "node_1")

	tool := NewCanvasTableWriteTool(canvasDir)
	ctx := WithThreadID(context.Background(), "ctx_thread")
	if _, err := tool.Execute(ctx, map[string]any{
		"node_id":   "node_1",
		"operation": "replace",
		"columns":   []any{map[string]any{"id": "a", "name": "A", "type": "text"}},
		"rows":      []any{map[string]any{"a": "x"}},
	}); err != nil {
		t.Fatalf("execute with ctx thread_id: %v", err)
	}
}

// TestCanvasTableWriteClampsLongCellValues protects the canvas JSON from a
// runaway model dumping novels into a cell. The clamp is at write time so
// the persisted bytes stay bounded.
func TestCanvasTableWriteClampsLongCellValues(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	writeEmptyTableFixture(t, canvasDir, "thread1", "node_1")

	huge := strings.Repeat("一", 1000) // far above canvasTableMaxCellChars
	tool := NewCanvasTableWriteTool(canvasDir)
	if _, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_1",
		"operation": "replace",
		"columns":   []any{map[string]any{"id": "body", "name": "Body", "type": "longText"}},
		"rows":      []any{map[string]any{"id": "row_1", "body": huge}},
	}); err != nil {
		t.Fatalf("replace huge: %v", err)
	}
	data := findTableNode(t, readTableDoc(t, canvasDir, "thread1"), "node_1")
	rows, _ := data["tableRows"].([]any)
	row, _ := rows[0].(map[string]any)
	got, _ := row["body"].(string)
	if len(got) >= len(huge) {
		t.Fatalf("expected clamp; got %d bytes, original %d", len(got), len(huge))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("clamped value should end with ellipsis, got %q", got)
	}
}

// TestCanvasTableWritePreservesUnknownNodeFields catches the regression
// where switching to a typed canvas struct would silently drop non-table
// fields. The tool must round-trip e.g. sketchData / mediaUrl untouched.
func TestCanvasTableWritePreservesUnknownNodeFields(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	doc := map[string]any{
		"nodes": []map[string]any{
			{"id": "node_sketch", "type": "sketch", "data": map[string]any{
				"nodeType": "sketch", "sketchData": "STROKES_BLOB", "mediaUrl": "/x.png",
			}},
			{"id": "node_1", "type": "table", "data": map[string]any{"nodeType": "table"}},
		},
		"viewport": map[string]any{"x": 100, "y": 50, "zoom": 0.8},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasTableWriteTool(canvasDir)
	if _, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_1",
		"operation": "replace",
		"columns":   []any{map[string]any{"id": "a", "name": "A", "type": "text"}},
		"rows":      []any{map[string]any{"a": "1"}},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	doc2 := readTableDoc(t, canvasDir, "thread1")
	sketchData := findTableNode(t, doc2, "node_sketch")
	if sketchData["sketchData"] != "STROKES_BLOB" {
		t.Fatalf("sketch payload was clobbered, got %+v", sketchData)
	}
	if sketchData["mediaUrl"] != "/x.png" {
		t.Fatalf("sketch mediaUrl was clobbered, got %+v", sketchData)
	}
	if vp, _ := doc2["viewport"].(map[string]any); vp == nil || vp["zoom"] == nil {
		t.Fatalf("viewport was dropped, got %+v", doc2["viewport"])
	}
}

// TestCanvasTableWriteDisabledWhenCanvasDirEmpty mirrors canvas_get_node:
// an unconfigured canvasDir must surface a clear "not available" error.
func TestCanvasTableWriteDisabledWhenCanvasDirEmpty(t *testing.T) {
	t.Parallel()
	tool := NewCanvasTableWriteTool("")
	_, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_1",
		"operation": "replace",
		"columns":   []any{map[string]any{"id": "a", "name": "A", "type": "text"}},
	})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected 'not available' error, got %v", err)
	}
}

// TestCanvasTableWriteRaceWithLateAutosave is the regression guard for the
// thread-bc72d587 incident: the agent fired canvas_table_write before the
// frontend's autosave landed the canvas file, and we surfaced a misleading
// "no canvas data" error. The fix in readCanvasWithMissingFileGrace polls
// for canvasTableWriteMissingFileWait (currently 6s); here we sleep < that
// budget then drop the file in and confirm the call succeeds end-to-end.
func TestCanvasTableWriteRaceWithLateAutosave(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	if err := os.MkdirAll(canvasDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	go func() {
		// 400ms is well under the 6s grace window but well past the 150ms
		// poll interval, so we exercise at least 1-2 retry rounds.
		// time.Sleep is fine here because t.Parallel + the sleep is the
		// whole point of the regression — we are simulating the autosave.
		time.Sleep(400 * time.Millisecond)
		writeEmptyTableFixture(t, canvasDir, "thread_race", "node_1")
	}()

	tool := NewCanvasTableWriteTool(canvasDir)
	res, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread_race",
		"node_id":   "node_1",
		"operation": "replace",
		"columns":   []any{map[string]any{"id": "c1", "name": "Title", "type": "text"}},
		"rows":      []any{map[string]any{"id": "r1", "c1": "hello"}},
	})
	if err != nil {
		t.Fatalf("expected the late-autosave race to be absorbed, got error: %v", err)
	}
	if res == nil || !res.Success {
		t.Fatalf("expected Success result, got %+v", res)
	}
}

// TestCanvasTableWriteRaceCtxCancelStopsPolling proves the missing-file grace
// loop honours ctx cancellation — a cancelled turn must not block the full
// canvasTableWriteMissingFileWait (6s) on a dead-end retry.
func TestCanvasTableWriteRaceCtxCancelStopsPolling(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	if err := os.MkdirAll(canvasDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	tool := NewCanvasTableWriteTool(canvasDir)
	start := time.Now()
	_, err := tool.Execute(ctx, map[string]any{
		"thread_id": "thread_cancel",
		"node_id":   "node_1",
		"operation": "replace",
		"columns":   []any{map[string]any{"id": "c1", "name": "Title", "type": "text"}},
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected error from ctx-cancelled grace loop")
	}
	// Grace window is 6s; ctx times out at 250ms. Allow generous slack
	// (1s) for slow CI but anything close to 6s means cancellation
	// wasn't honoured.
	if elapsed > time.Second {
		t.Fatalf("ctx cancel didn't shortcut the grace loop, elapsed=%v", elapsed)
	}
}

// TestCanvasTableWriteMissingFileErrorIsActionable locks in the new error
// message: the previous "no canvas data for thread X" was misread by the
// model as "thread isn't bound to a canvas". The new wording names the
// race and the user-facing remediation.
func TestCanvasTableWriteMissingFileErrorIsActionable(t *testing.T) {
	t.Parallel()
	canvasDir := filepath.Join(t.TempDir(), "canvas")
	if err := os.MkdirAll(canvasDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tool := NewCanvasTableWriteTool(canvasDir)
	_, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread_never",
		"node_id":   "node_1",
		"operation": "replace",
		"columns":   []any{map[string]any{"id": "c1", "name": "Title", "type": "text"}},
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	for _, want := range []string{"no canvas data", "frontend", "wait", "retry"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q hint: %q", want, err.Error())
		}
	}
}
