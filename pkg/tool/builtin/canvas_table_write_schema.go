package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/tool"
)

// canvas_table_write_schema.go owns the public tool surface (struct, schema,
// description, Name/Description/Schema, Execute) plus canvas-file
// IO/lookup helpers. The mutation routines live in
// canvas_table_write_apply.go and the validation/normalisation/persistence
// helpers in canvas_table_write_helpers.go.

// canvasTableWriteMissingFileWait bounds how long we wait for the frontend's
// autosave / pre-flush to land a brand-new canvas before erroring out. The
// frontend's saveToServer debounce is 1500ms (useCanvasBridge.ts); 6s gives
// roughly four debounce windows of headroom to absorb network/disk jitter.
const (
	canvasTableWriteMissingFileWait = 6 * time.Second
	canvasTableWriteMissingFilePoll = 150 * time.Millisecond
)

// canvasTableWriteDescription is shown to the LLM when picking the tool.
const canvasTableWriteDescription = `Writes structured rows and columns into an existing table node on the canvas.

Use this when the user asks to populate a table-shaped artifact (e.g. split a
script into beats with columns scene/setting/action/line/shot, break an article
into paragraphs with title/body, build a checklist). The target node MUST exist
on the canvas and have nodeType == "table" — create it manually first, or ask
the user to add one via the QuickAdd menu, then call this tool with the node's
id (visible in the <canvas_state> summary or via canvas_list_nodes).

Operations:
- replace      Overwrite the table's columns and rows wholesale. Pass "columns"
               and "rows". Use for first-time fill or when the structure changes.
- set_cell     Change one cell. Pass "row_id", "column_id", "value".
- add_row      Append a row at the bottom by default. Pass "values" (a map keyed
               by column id). Optional "position" ("top" / "bottom" /
               "after:<row_id>") and "row_id" (auto-generated when omitted).
- delete_row   Remove one row. Pass "row_id".

Cell text is truncated to 200 characters at write time to keep the canvas JSON
small. Column ids and row ids must match ^[A-Za-z0-9_-]+$. After a successful
call the browser auto-loads the change on the next canvas/load (or live, if the
user is already on that thread).

IMPORTANT: When the user wants tabular output ON THE CANVAS, you MUST use this
tool. Do NOT fall back to bash heredoc, file_write, or printing markdown table
syntax in chat — those will not populate the table node. If no table node
exists yet, ask the user to add one (or, in agent flows where a parent step
already created one, the new node id is named in the prompt).`

var canvasTableWriteSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"thread_id": map[string]any{
			"type":        "string",
			"description": "Optional. Defaults to the current chat thread when the server has injected it.",
		},
		"node_id": map[string]any{
			"type":        "string",
			"description": "ID of the target table node, e.g. node_3.",
		},
		"operation": map[string]any{
			"type":        "string",
			"enum":        []string{"replace", "set_cell", "add_row", "delete_row"},
			"description": "Which mutation to apply.",
		},
		"columns": map[string]any{
			"type":        "array",
			"description": "For operation=replace. Ordered list of column definitions.",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":      map[string]any{"type": "string"},
					"name":    map[string]any{"type": "string"},
					"type":    map[string]any{"type": "string", "enum": []string{"text", "longText", "number", "select"}},
					"options": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []string{"name"},
			},
		},
		"rows": map[string]any{
			"type":        "array",
			"description": "For operation=replace. Each row is a map keyed by column id, with an optional 'id' field.",
			"items":       map[string]any{"type": "object"},
		},
		"row_id": map[string]any{
			"type":        "string",
			"description": "For set_cell / add_row / delete_row. For add_row this is optional and auto-generated when omitted.",
		},
		"column_id": map[string]any{
			"type":        "string",
			"description": "For set_cell. Must match an existing column id.",
		},
		"value": map[string]any{
			"description": "For set_cell. New cell value (string / number / null).",
		},
		"values": map[string]any{
			"type":        "object",
			"description": "For add_row. Map keyed by column id with the initial cell values.",
		},
		"position": map[string]any{
			"type":        "string",
			"description": "For add_row. 'bottom' (default), 'top', or 'after:<row_id>'.",
		},
	},
	Required: []string{"node_id", "operation"},
}

// canvasTableMaxCellChars caps a single cell's textual representation so a
// runaway agent can't bloat the canvas JSON. Long-form columns still fit a
// paragraph; numeric columns are unaffected.
const canvasTableMaxCellChars = 200

// canvasTableMaxRows caps the row count after a write to keep the canvas
// document compact. ~100 rows × N columns is the design target; we allow a
// little headroom but reject pathological cases.
const canvasTableMaxRows = 200

// CanvasTableWriteTool exposes structured table mutations on canvas table
// nodes. Bound to the same per-thread directory as CanvasGetNodeTool.
type CanvasTableWriteTool struct {
	canvasDir string
}

// NewCanvasTableWriteTool builds a tool bound to the per-thread canvas JSON
// directory. Pass an empty canvasDir to disable the tool.
func NewCanvasTableWriteTool(canvasDir string) *CanvasTableWriteTool {
	return &CanvasTableWriteTool{canvasDir: canvasDir}
}

// Name returns the tool identifier.
func (t *CanvasTableWriteTool) Name() string { return "canvas_table_write" }

// Description returns the LLM-facing description.
func (t *CanvasTableWriteTool) Description() string { return canvasTableWriteDescription }

// Schema returns the parameter schema.
func (t *CanvasTableWriteTool) Schema() *tool.JSONSchema { return canvasTableWriteSchema }

// Execute performs the table mutation against the on-disk canvas snapshot.
// The browser auto-saves the canvas after every edit, so we always work
// against the most recent state.
func (t *CanvasTableWriteTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if t == nil || strings.TrimSpace(t.canvasDir) == "" {
		return nil, errors.New("canvas_table_write: not available (no canvas directory configured)")
	}

	threadID, _ := params["thread_id"].(string)
	nodeID, _ := params["node_id"].(string)
	operation, _ := params["operation"].(string)
	threadID = strings.TrimSpace(threadID)
	nodeID = strings.TrimSpace(nodeID)
	operation = strings.TrimSpace(operation)
	if threadID == "" {
		threadID = ThreadIDFromContext(ctx)
	}
	if threadID == "" {
		return nil, errors.New("canvas_table_write: thread_id is required (no thread injected via context)")
	}
	if nodeID == "" {
		return nil, errors.New("canvas_table_write: node_id is required")
	}
	if !canvasIDPattern.MatchString(threadID) {
		return nil, fmt.Errorf("canvas_table_write: invalid thread_id %q", threadID)
	}
	if !canvasIDPattern.MatchString(nodeID) {
		return nil, fmt.Errorf("canvas_table_write: invalid node_id %q", nodeID)
	}
	if operation == "" {
		return nil, errors.New("canvas_table_write: operation is required")
	}

	canvasPath := filepath.Join(t.canvasDir, threadID+".json")
	raw, err := readCanvasWithMissingFileGrace(ctx, canvasPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Pre-flush in ChatApp.tsx is supposed to land before turn/send,
			// but a brand-new thread that types its first message before any
			// canvas autosave fires can still race past this tool. The error
			// message names the symptom + the user-actionable next step so
			// the model doesn't make up "thread not bound to canvas" prose.
			return nil, fmt.Errorf("canvas_table_write: no canvas data for thread %s yet — the frontend has not persisted any canvas state. Either the table node was never created, or the autosave has not landed. Ask the user to wait a moment and retry, or to confirm the table node exists on the canvas", threadID)
		}
		return nil, fmt.Errorf("canvas_table_write: read canvas: %w", err)
	}

	// Use map[string]any to preserve unknown fields (sketch data, viewport,
	// other node types). The narrow canvasNodeData struct in canvas.go would
	// silently drop tableColumns/tableRows on round-trip.
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("canvas_table_write: parse canvas: %w", err)
	}

	nodes, _ := doc["nodes"].([]any)
	target, targetIdx := findCanvasNodeByID(nodes, nodeID)
	if target == nil {
		return nil, fmt.Errorf("canvas_table_write: node %s not found in thread %s", nodeID, threadID)
	}
	data, _ := target["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
		target["data"] = data
	}
	if nt, _ := data["nodeType"].(string); nt != "table" {
		return nil, fmt.Errorf("canvas_table_write: node %s is not a table (nodeType=%q)", nodeID, nt)
	}

	summary, applyErr := applyTableOperation(data, operation, params)
	if applyErr != nil {
		return nil, fmt.Errorf("canvas_table_write: %w", applyErr)
	}

	// Stitch the (possibly modified) target back in. Map mutations propagate,
	// but slices may have been reassigned by the apply step.
	if targetIdx >= 0 {
		nodes[targetIdx] = target
	}
	doc["nodes"] = nodes

	if err := atomicWriteCanvasJSON(canvasPath, doc); err != nil {
		return nil, fmt.Errorf("canvas_table_write: persist canvas: %w", err)
	}

	rowsAfter, _ := data["tableRows"].([]any)
	colsAfter, _ := data["tableColumns"].([]any)
	return &tool.ToolResult{
		Success: true,
		Output: fmt.Sprintf("canvas_table_write %s on node %s OK — %d columns, %d rows now.\n%s",
			operation, nodeID, len(colsAfter), len(rowsAfter), summary),
		Data: map[string]any{
			"thread_id": threadID,
			"node_id":   nodeID,
			"operation": operation,
			"row_count": len(rowsAfter),
			"col_count": len(colsAfter),
		},
	}, nil
}

// readCanvasWithMissingFileGrace wraps os.ReadFile so a brand-new thread that
// hasn't received its first canvas/save yet gets a brief polling window before
// the tool errors out. The browser's saveToServer debounce is ~500ms; a single
// hard miss is almost always a race against the autosave, not a true absence.
//
// Honours ctx cancellation so a cancelled turn doesn't block here for 2s.
func readCanvasWithMissingFileGrace(ctx context.Context, canvasPath string) ([]byte, error) {
	raw, err := os.ReadFile(canvasPath)
	if err == nil || !os.IsNotExist(err) {
		return raw, err
	}
	deadline := time.Now().Add(canvasTableWriteMissingFileWait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(canvasTableWriteMissingFilePoll):
		}
		raw, err = os.ReadFile(canvasPath)
		if err == nil {
			return raw, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return nil, err
}

// findCanvasNodeByID returns the target node map and its index in the slice,
// or (nil, -1) when the id doesn't appear. Unknown shapes (non-map entries)
// are skipped — defensive against future schema drift.
func findCanvasNodeByID(nodes []any, nodeID string) (map[string]any, int) {
	for i, raw := range nodes {
		n, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := n["id"].(string); id == nodeID {
			return n, i
		}
	}
	return nil, -1
}
