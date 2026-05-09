package toolbuiltin

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestCanvasImageBudgetRefusesAttachmentPastCap drives canvas_get_node
// repeatedly with a tiny image but a synthetic 100-byte cap so we don't
// have to materialize 20 MiB of fixtures. After two attachments the third
// must be refused with a clear "[image attachment skipped: ...]" notice.
func TestCanvasImageBudgetRefusesAttachmentPastCap(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	canvasDir := filepath.Join(root, "canvas")
	mediaPath := filepath.Join(root, "canvas-media", "img.png")
	writeFixturePNG(t, mediaPath)

	doc := map[string]any{
		"nodes": []map[string]any{
			{
				"id":   "node_1",
				"type": "image",
				"data": map[string]any{
					"nodeType":  "image",
					"mediaPath": mediaPath,
					"mediaUrl":  "/api/files/canvas-media/img.png",
				},
			},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasGetNodeTool(canvasDir)
	// Replace the tracker with one capped tightly so the test stays cheap.
	// We're testing the wiring + breadcrumb, not the literal 20 MiB.
	tool.budget = newCanvasImageBudget()

	// First call: succeeds, count goes up.
	res, err := tool.Execute(context.Background(), map[string]any{"thread_id": "thread1", "node_id": "node_1"})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(res.ContentBlocks) != 1 {
		t.Fatalf("first call must attach image, got %d blocks", len(res.ContentBlocks))
	}
	used1 := tool.budget.Used("thread1")
	if used1 == 0 {
		t.Fatalf("budget not incremented after success, used=%d", used1)
	}

	// Now poison the tracker so the next reserve trips the cap.
	tool.budget.perThread["thread1"] = canvasImageBudgetPerThread

	res2, err := tool.Execute(context.Background(), map[string]any{"thread_id": "thread1", "node_id": "node_1"})
	if err != nil {
		t.Fatalf("second call should not error, got %v", err)
	}
	if len(res2.ContentBlocks) != 0 {
		t.Fatalf("budget exhaustion must drop attachment, got %d blocks", len(res2.ContentBlocks))
	}
	if !strings.Contains(res2.Output, "image attachment skipped") {
		t.Fatalf("expected skip notice with reason, got: %s", res2.Output)
	}
	if !strings.Contains(res2.Output, "budget exceeded") {
		t.Fatalf("skip notice should explain the cap, got: %s", res2.Output)
	}
}

// TestCanvasImageBudgetIsolatesThreads ensures one thread eating its quota
// does not block a sibling thread — budgets are per-thread, not global.
func TestCanvasImageBudgetIsolatesThreads(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	canvasDir := filepath.Join(root, "canvas")
	mediaPath := filepath.Join(root, "canvas-media", "img.png")
	writeFixturePNG(t, mediaPath)

	doc := map[string]any{
		"nodes": []map[string]any{
			{"id": "node_1", "type": "image", "data": map[string]any{
				"nodeType": "image", "mediaPath": mediaPath,
			}},
		},
	}
	writeCanvasFixture(t, canvasDir, "threadA", doc)
	writeCanvasFixture(t, canvasDir, "threadB", doc)

	tool := NewCanvasGetNodeTool(canvasDir)
	// Exhaust threadA's quota only.
	tool.budget.perThread = map[string]int64{"threadA": canvasImageBudgetPerThread}

	resA, _ := tool.Execute(context.Background(), map[string]any{"thread_id": "threadA", "node_id": "node_1"})
	if len(resA.ContentBlocks) != 0 {
		t.Fatalf("threadA must be over budget, got %d blocks", len(resA.ContentBlocks))
	}
	resB, err := tool.Execute(context.Background(), map[string]any{"thread_id": "threadB", "node_id": "node_1"})
	if err != nil {
		t.Fatalf("threadB call failed: %v", err)
	}
	if len(resB.ContentBlocks) != 1 {
		t.Fatalf("threadB has its own quota, expected 1 block, got %d", len(resB.ContentBlocks))
	}
}

// TestCanvasImageBudgetReserveZeroByteIsNoOp keeps the budgeting code from
// punishing legitimate empty media (corrupt/missing data) — we only count
// real bytes, so a 0-byte attachment doesn't cost anything.
func TestCanvasImageBudgetReserveZeroByteIsNoOp(t *testing.T) {
	t.Parallel()
	b := newCanvasImageBudget()
	if err := b.Reserve("thread1", 0); err != nil {
		t.Fatalf("zero-byte reserve must succeed, got %v", err)
	}
	if got := b.Used("thread1"); got != 0 {
		t.Fatalf("zero-byte reserve must not bump counter, used=%d", got)
	}
}

// TestCanvasImageBudgetReserveFailureLeavesCounterUnchanged ensures a
// rejected reservation does not partially consume the budget — the model
// might pivot to a smaller attachment afterwards and we don't want that
// blocked by ghost accounting.
func TestCanvasImageBudgetReserveFailureLeavesCounterUnchanged(t *testing.T) {
	t.Parallel()
	b := newCanvasImageBudget()
	b.perThread["thread1"] = canvasImageBudgetPerThread - 10
	// Try to reserve more than the remaining 10 bytes.
	if err := b.Reserve("thread1", 1000); err == nil {
		t.Fatalf("expected over-budget error, got nil")
	}
	if got := b.Used("thread1"); got != canvasImageBudgetPerThread-10 {
		t.Fatalf("failed reserve must not bump counter, used=%d want %d",
			got, canvasImageBudgetPerThread-10)
	}
	// The smaller follow-up should still fit.
	if err := b.Reserve("thread1", 5); err != nil {
		t.Fatalf("sub-budget follow-up must succeed, got %v", err)
	}
}

// TestCanvasImageBudgetEmptyThreadIDBypasses keeps non-thread callers
// (e.g. CLI debug) from tripping the per-thread machinery, since there's
// no key to scope the cap to.
func TestCanvasImageBudgetEmptyThreadIDBypasses(t *testing.T) {
	t.Parallel()
	b := newCanvasImageBudget()
	if err := b.Reserve("", 1<<30); err != nil {
		t.Fatalf("empty threadID must bypass budgeting, got %v", err)
	}
}
