package toolbuiltin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/model"
)

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jkO8AAAAASUVORK5CYII="

func writeCanvasFixture(t *testing.T, dir, threadID string, doc map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir canvas dir: %v", err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal canvas doc: %v", err)
	}
	path := filepath.Join(dir, threadID+".json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write canvas fixture: %v", err)
	}
	return path
}

func writeFixturePNG(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir image dir: %v", err)
	}
	data, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatalf("decode tiny png: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write png: %v", err)
	}
}

func TestCanvasGetNodeReturnsImageContentBlock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	canvasDir := filepath.Join(root, "canvas")
	mediaPath := filepath.Join(root, "canvas-media", "abc.png")
	writeFixturePNG(t, mediaPath)

	doc := map[string]any{
		"nodes": []map[string]any{
			{
				"id":   "node_2",
				"type": "sketch",
				"data": map[string]any{
					"nodeType":  "sketch",
					"label":     "Sketch reference",
					"status":    "done",
					"mediaPath": mediaPath,
					"mediaUrl":  "/api/files/canvas-media/abc.png",
				},
			},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "node_2", "target": "node_3", "type": "reference"},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasGetNodeTool(canvasDir)
	res, err := tool.Execute(context.Background(), map[string]any{
		"thread_id": "thread1",
		"node_id":   "node_2",
	})
	if err != nil {
		t.Fatalf("execute canvas_get_node: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got %+v", res)
	}
	if !strings.Contains(res.Output, "node_2") {
		t.Fatalf("output should mention node_2, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "Sketch reference") {
		t.Fatalf("output should mention label, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "node_2 --reference--> node_3") {
		t.Fatalf("output should describe outbound edge, got: %s", res.Output)
	}
	if len(res.ContentBlocks) != 1 {
		t.Fatalf("expected one image content block, got %d", len(res.ContentBlocks))
	}
	if res.ContentBlocks[0].Type != model.ContentBlockImage {
		t.Fatalf("expected image block, got %v", res.ContentBlocks[0].Type)
	}
	if res.ContentBlocks[0].MediaType != "image/png" {
		t.Fatalf("expected image/png, got %q", res.ContentBlocks[0].MediaType)
	}
	if res.ContentBlocks[0].Data == "" {
		t.Fatalf("expected base64 data, got empty")
	}
}

func TestCanvasGetNodeWithoutMediaReturnsNoBlock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	canvasDir := filepath.Join(root, "canvas")
	doc := map[string]any{
		"nodes": []map[string]any{
			{"id": "node_0", "type": "prompt", "data": map[string]any{"nodeType": "prompt", "label": "Hello"}},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasGetNodeTool(canvasDir)
	res, err := tool.Execute(context.Background(), map[string]any{"thread_id": "thread1", "node_id": "node_0"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.ContentBlocks) != 0 {
		t.Fatalf("expected no content blocks for prompt node, got %d", len(res.ContentBlocks))
	}
}

func TestCanvasGetNodeRejectsMediaOutsideCanvasMedia(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	canvasDir := filepath.Join(root, "canvas")
	// mediaPath is a real PNG but lives in a directory NOT named canvas-media —
	// the tool must refuse to leak it.
	mediaPath := filepath.Join(root, "secrets", "private.png")
	writeFixturePNG(t, mediaPath)

	doc := map[string]any{
		"nodes": []map[string]any{
			{
				"id":   "node_evil",
				"type": "image",
				"data": map[string]any{"nodeType": "image", "mediaPath": mediaPath},
			},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasGetNodeTool(canvasDir)
	res, err := tool.Execute(context.Background(), map[string]any{"thread_id": "thread1", "node_id": "node_evil"})
	if err != nil {
		t.Fatalf("execute should not error on metadata path, got %v", err)
	}
	if len(res.ContentBlocks) != 0 {
		t.Fatalf("must NOT attach image outside canvas-media/, got %d blocks", len(res.ContentBlocks))
	}
	if !strings.Contains(res.Output, "image attachment skipped") {
		t.Fatalf("expected skip notice in output, got: %s", res.Output)
	}
}

// TestCanvasGetNodeRejectsDecoyCanvasMediaDir guards against the old weaker
// check that only looked for the string "/canvas-media/" in the resolved path.
// Here the media file is inside a directory literally named "canvas-media" but
// NOT paired with canvasDir, so the tool must still reject it.
func TestCanvasGetNodeRejectsDecoyCanvasMediaDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	canvasDir := filepath.Join(root, "real", "canvas")
	// Decoy directory: name contains canvas-media but lives under an unrelated path.
	mediaPath := filepath.Join(root, "evil", "canvas-media", "leak.png")
	writeFixturePNG(t, mediaPath)

	doc := map[string]any{
		"nodes": []map[string]any{
			{"id": "node_evil", "type": "image", "data": map[string]any{"nodeType": "image", "mediaPath": mediaPath}},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasGetNodeTool(canvasDir)
	res, err := tool.Execute(context.Background(), map[string]any{"thread_id": "thread1", "node_id": "node_evil"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.ContentBlocks) != 0 {
		t.Fatalf("decoy canvas-media must be rejected, got %d blocks", len(res.ContentBlocks))
	}
	if !strings.Contains(res.Output, "image attachment skipped") {
		t.Fatalf("expected skip notice, got: %s", res.Output)
	}
}

func TestCanvasGetNodeMissingNode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	canvasDir := filepath.Join(root, "canvas")
	writeCanvasFixture(t, canvasDir, "thread1", map[string]any{"nodes": []map[string]any{{"id": "node_0", "type": "prompt"}}})

	tool := NewCanvasGetNodeTool(canvasDir)
	if _, err := tool.Execute(context.Background(), map[string]any{"thread_id": "thread1", "node_id": "node_999"}); err == nil {
		t.Fatal("expected error for missing node")
	}
}

func TestCanvasGetNodeMissingCanvasFile(t *testing.T) {
	t.Parallel()
	tool := NewCanvasGetNodeTool(t.TempDir())
	if _, err := tool.Execute(context.Background(), map[string]any{"thread_id": "thread1", "node_id": "node_0"}); err == nil {
		t.Fatal("expected error when canvas file is missing")
	}
}

func TestCanvasGetNodeRejectsBadIDs(t *testing.T) {
	t.Parallel()
	tool := NewCanvasGetNodeTool(t.TempDir())
	for _, tc := range []struct{ thread, node string }{
		{"../etc/passwd", "node_0"},
		{"thread1", "../node"},
		{"", "node_0"},
		{"thread1", ""},
	} {
		if _, err := tool.Execute(context.Background(), map[string]any{"thread_id": tc.thread, "node_id": tc.node}); err == nil {
			t.Fatalf("expected rejection for thread=%q node=%q", tc.thread, tc.node)
		}
	}
}

func TestCanvasGetNodeDisabledWhenCanvasDirEmpty(t *testing.T) {
	t.Parallel()
	tool := NewCanvasGetNodeTool("")
	_, err := tool.Execute(context.Background(), map[string]any{"thread_id": "thread1", "node_id": "node_0"})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected 'not available' error, got %v", err)
	}
}

// TestCanvasGetNodeUsesContextThreadID confirms that when thread_id is omitted
// from params, the tool falls back to the value injected via WithThreadID.
func TestCanvasGetNodeUsesContextThreadID(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	canvasDir := filepath.Join(root, "canvas")
	doc := map[string]any{
		"nodes": []map[string]any{
			{"id": "node_0", "type": "prompt", "data": map[string]any{"nodeType": "prompt", "label": "Hi"}},
		},
	}
	writeCanvasFixture(t, canvasDir, "ctx_thread", doc)

	tool := NewCanvasGetNodeTool(canvasDir)
	ctx := WithThreadID(context.Background(), "ctx_thread")
	res, err := tool.Execute(ctx, map[string]any{"node_id": "node_0"})
	if err != nil {
		t.Fatalf("execute with ctx-injected thread_id: %v", err)
	}
	if !strings.Contains(res.Output, "node_0") {
		t.Fatalf("expected output to mention node_0, got: %s", res.Output)
	}
}

// TestCanvasGetNodeRequiresThreadIDFromCtxOrParams ensures the tool refuses
// when neither params nor context supply a thread ID.
func TestCanvasGetNodeRequiresThreadIDFromCtxOrParams(t *testing.T) {
	t.Parallel()
	tool := NewCanvasGetNodeTool(t.TempDir())
	if _, err := tool.Execute(context.Background(), map[string]any{"node_id": "node_0"}); err == nil ||
		!strings.Contains(err.Error(), "thread_id is required") {
		t.Fatalf("expected thread_id required error, got %v", err)
	}
}

// TestCanvasListNodesReturnsSummary confirms the new on-demand summary tool
// surfaces nodes/edges in the same digest format the prepended <canvas_state>
// block uses.
func TestCanvasListNodesReturnsSummary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	canvasDir := filepath.Join(root, "canvas")
	doc := map[string]any{
		"nodes": []map[string]any{
			{"id": "node_0", "type": "prompt", "data": map[string]any{"nodeType": "prompt", "label": "Ask"}},
			{"id": "node_2", "type": "image", "data": map[string]any{"nodeType": "image", "mediaUrl": "/x.png"}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "node_0", "target": "node_2", "type": "reference"},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasListNodesTool(canvasDir)
	ctx := WithThreadID(context.Background(), "thread1")
	res, err := tool.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	for _, want := range []string{"thread_id: thread1", "node_0", "node_2", "hasMedia=true", "node_0 --reference--> node_2"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("summary missing %q\nfull:\n%s", want, res.Output)
		}
	}
}

// TestCanvasGetNodeOmitsDataURIFromSummary regresses the eddaff17 incident:
// a sketch node's sourceUrl was a base64 data: URI inlined into the LLM
// summary, bloating context to 26k tokens and causing the model to emit a
// generate_image call with no prompt. The summary must omit the raw bytes;
// the actual image is delivered via the image ContentBlock instead.
func TestCanvasGetNodeOmitsDataURIFromSummary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	canvasDir := filepath.Join(root, "canvas")
	mediaPath := filepath.Join(root, "canvas-media", "sketch.png")
	writeFixturePNG(t, mediaPath)

	// Construct a long base64 sourceUrl to make leakage easy to detect.
	bigPayload := strings.Repeat("A", 4096)
	dataURI := "data:image/png;base64," + bigPayload

	doc := map[string]any{
		"nodes": []map[string]any{
			{
				"id":   "node_3",
				"type": "sketch",
				"data": map[string]any{
					"nodeType":  "sketch",
					"label":     "画笔涂鸦",
					"status":    "done",
					"mediaPath": mediaPath,
					"mediaUrl":  "/api/files/canvas-media/sketch.png",
					"sourceUrl": dataURI,
				},
			},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasGetNodeTool(canvasDir)
	res, err := tool.Execute(context.Background(), map[string]any{"thread_id": "thread1", "node_id": "node_3"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(res.Output, bigPayload) {
		t.Fatalf("summary leaked %d bytes of base64 payload; full output:\n%s", len(bigPayload), res.Output)
	}
	if strings.Contains(res.Output, "data:image/png;base64,") {
		t.Fatalf("summary still contains a data: URI prefix; full output:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "data URI omitted") {
		t.Fatalf("summary should announce the omitted data URI; full output:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "mediaUrl: /api/files/canvas-media/sketch.png") {
		t.Fatalf("plain mediaUrl must still pass through; full output:\n%s", res.Output)
	}
	if len(res.ContentBlocks) != 1 || res.ContentBlocks[0].Type != model.ContentBlockImage {
		t.Fatalf("image must still be delivered as a ContentBlock; got %+v", res.ContentBlocks)
	}
}

// TestCanvasGetNodeOmitsDataURIFromPromptAndContent extends the eddaff17
// regression to defense-in-depth on the free-text fields. If the frontend
// ever routes a data: URI into prompt/content (e.g. drag-drop accident),
// the summary must scrub it the same way it scrubs sourceUrl/mediaUrl —
// a 400-char truncated base64 prefix is still pure garbage to the model.
func TestCanvasGetNodeOmitsDataURIFromPromptAndContent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	canvasDir := filepath.Join(root, "canvas")

	bigPayload := strings.Repeat("Z", 4096)
	dataURI := "data:image/png;base64," + bigPayload

	doc := map[string]any{
		"nodes": []map[string]any{
			{
				"id":   "node_p",
				"type": "prompt",
				"data": map[string]any{
					"nodeType": "prompt",
					"label":    "leaked prompt",
					"prompt":   dataURI,
					"content":  dataURI,
				},
			},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasGetNodeTool(canvasDir)
	res, err := tool.Execute(context.Background(), map[string]any{"thread_id": "thread1", "node_id": "node_p"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(res.Output, bigPayload) {
		t.Fatalf("summary leaked %d bytes of base64 payload from prompt/content; output:\n%s", len(bigPayload), res.Output)
	}
	if strings.Contains(res.Output, "data:image/png;base64,") {
		t.Fatalf("summary still contains a data: URI prefix; output:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "<prompt data URI omitted") {
		t.Fatalf("expected scrubbed prompt placeholder; output:\n%s", res.Output)
	}
}

// TestCanvasListNodesScrubsDataURIFromLabelAndPrompt extends the eddaff17
// data-URI defense to canvas_list_nodes. Even though list-summary fields are
// hard-truncated to 60-80 chars, a leaked "data:image/png;base64,…" prefix
// still poisons the model context with bytes it cannot use. Scrub them with
// the same placeholder treatment as canvas_get_node.
func TestCanvasListNodesScrubsDataURIFromLabelAndPrompt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	canvasDir := filepath.Join(root, "canvas")

	bigPayload := strings.Repeat("Q", 4096)
	dataURI := "data:image/png;base64," + bigPayload
	doc := map[string]any{
		"nodes": []map[string]any{
			{
				"id":   "node_p",
				"type": "prompt",
				"data": map[string]any{
					"nodeType": "prompt",
					"label":    dataURI,
					"prompt":   dataURI,
				},
			},
		},
	}
	writeCanvasFixture(t, canvasDir, "thread1", doc)

	tool := NewCanvasListNodesTool(canvasDir)
	ctx := WithThreadID(context.Background(), "thread1")
	res, err := tool.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(res.Output, bigPayload) {
		t.Fatalf("list summary leaked %d bytes of base64 from label/prompt; output:\n%s", len(bigPayload), res.Output)
	}
	if strings.Contains(res.Output, "data:image/png;base64,") {
		t.Fatalf("list summary still contains a data: URI prefix; output:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "data URI omitted") {
		t.Fatalf("list summary should announce the omitted data URI; output:\n%s", res.Output)
	}
}

// TestCanvasListNodesEmptyCanvasIsOK ensures requesting a missing canvas
// returns success with a friendly empty-state message instead of an error.
func TestCanvasListNodesEmptyCanvasIsOK(t *testing.T) {
	t.Parallel()
	tool := NewCanvasListNodesTool(t.TempDir())
	ctx := WithThreadID(context.Background(), "fresh_thread")
	res, err := tool.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("expected success on missing canvas, got %v", err)
	}
	if !strings.Contains(res.Output, "canvas is empty") {
		t.Fatalf("expected empty-state message, got: %s", res.Output)
	}
}
