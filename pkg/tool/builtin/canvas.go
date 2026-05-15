package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/tool"
)

// canvasGetNodeDescription is shown to the LLM when it picks a tool.
const canvasGetNodeDescription = `Reads a single canvas node by ID from the current thread's canvas state.

Returns the node's metadata (type, label, prompt, status, connected edges, etc.).
For image / sketch / imageGen / video nodes that have a saved mediaPath, ALSO
returns the binary content as an image ContentBlock so the model can see the
image directly.

Use this BEFORE generating new media when the user asks to reference, edit,
build on, or describe something already on the canvas. Available node IDs come
from canvas_list_nodes or the <canvas_state> block (when present).

Parameters:
- node_id: Node ID, e.g. "node_2".
- thread_id (optional): Thread ID. The server normally injects the current
  thread automatically; only pass this if you need to read a different thread's
  canvas.`

var canvasGetNodeSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"thread_id": map[string]any{
			"type":        "string",
			"description": "Optional. Defaults to the current chat thread when the server has injected it.",
		},
		"node_id": map[string]any{
			"type":        "string",
			"description": "Node ID from canvas_list_nodes or the <canvas_state> summary, e.g. node_2.",
		},
	},
	Required: []string{"node_id"},
}

// threadIDKey carries the canvas thread/session ID through context. The HTTP
// handler injects this before invoking the agent so canvas tools can default
// to the current chat thread without the LLM having to thread it through.
type threadIDKey struct{}

// WithThreadID returns a context carrying the given thread ID for canvas tools.
func WithThreadID(ctx context.Context, threadID string) context.Context {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ctx
	}
	return context.WithValue(ctx, threadIDKey{}, threadID)
}

// ThreadIDFromContext retrieves the thread ID from context, or "" if absent.
func ThreadIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(threadIDKey{}).(string)
	return id
}

// canvasIDPattern restricts thread/node IDs to filename-safe characters to
// prevent path traversal.
var canvasIDPattern = regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)

// canvasMediaSegment is the directory name canvas server writes media files
// under (e.g. <project>/.saker/canvas-media/<sha>.png). Resolved mediaPath
// must contain this segment as a defense against arbitrary file reads.
const canvasMediaSegment = "canvas-media"

// canvasMaxImageBytes caps the size of an image returned via ContentBlock to
// avoid blowing up the model context with multi-MB attachments.
const canvasMaxImageBytes = 5 << 20 // 5 MiB

// CanvasGetNodeTool exposes a per-thread canvas node lookup to the LLM.
// canvasDir is the directory holding "{threadID}.json" files (server side
// stores them at "<dataDir>/canvas"). When canvasDir is empty the tool is
// effectively disabled and Execute returns a clear error — this matches CLI
// mode where there is no web canvas.
//
// budget bounds how many image bytes a single thread can attach across
// repeated calls — see canvasImageBudget for the rationale.
type CanvasGetNodeTool struct {
	canvasDir string
	budget    *canvasImageBudget
}

// NewCanvasGetNodeTool builds a tool bound to the per-thread canvas JSON
// directory. Pass an empty canvasDir to disable the tool (CLI mode).
func NewCanvasGetNodeTool(canvasDir string) *CanvasGetNodeTool {
	return &CanvasGetNodeTool{canvasDir: canvasDir, budget: newCanvasImageBudget()}
}

// Name returns the tool identifier exposed to the model.
func (t *CanvasGetNodeTool) Name() string { return "canvas_get_node" }

// Description returns the human-readable description shown to the model.
func (t *CanvasGetNodeTool) Description() string { return canvasGetNodeDescription }

// Schema returns the JSON schema describing the tool's parameters.
func (t *CanvasGetNodeTool) Schema() *tool.JSONSchema { return canvasGetNodeSchema }

// Execute looks up a single canvas node by ID and returns its metadata plus
// (when available and safe) the bound image as an image ContentBlock.
func (t *CanvasGetNodeTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if t == nil || strings.TrimSpace(t.canvasDir) == "" {
		return nil, errors.New("canvas_get_node: not available (no canvas directory configured)")
	}

	threadID, _ := params["thread_id"].(string)
	nodeID, _ := params["node_id"].(string)
	threadID = strings.TrimSpace(threadID)
	nodeID = strings.TrimSpace(nodeID)
	if threadID == "" {
		threadID = ThreadIDFromContext(ctx)
	}
	if threadID == "" {
		return nil, errors.New("canvas_get_node: thread_id is required (no thread injected via context)")
	}
	if nodeID == "" {
		return nil, errors.New("canvas_get_node: node_id is required")
	}
	if !canvasIDPattern.MatchString(threadID) {
		return nil, fmt.Errorf("canvas_get_node: invalid thread_id %q", threadID)
	}
	if !canvasIDPattern.MatchString(nodeID) {
		return nil, fmt.Errorf("canvas_get_node: invalid node_id %q", nodeID)
	}

	canvasPath := filepath.Join(t.canvasDir, threadID+".json")
	raw, err := os.ReadFile(canvasPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("canvas_get_node: no canvas data for thread %s", threadID)
		}
		return nil, fmt.Errorf("canvas_get_node: read canvas: %w", err)
	}

	var doc canvasDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("canvas_get_node: parse canvas: %w", err)
	}

	var found *canvasNode
	for i := range doc.Nodes {
		if doc.Nodes[i].ID == nodeID {
			found = &doc.Nodes[i]
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("canvas_get_node: node %s not found in thread %s", nodeID, threadID)
	}

	summary := buildCanvasNodeSummary(found, doc.Edges)

	result := &tool.ToolResult{
		Success: true,
		Output:  summary,
		Data: map[string]any{
			"thread_id": threadID,
			"node_id":   nodeID,
			"node_type": found.Data.NodeType,
			"label":     found.Data.Label,
			"media_url": found.Data.MediaURL,
		},
	}

	if mediaPath := strings.TrimSpace(found.Data.MediaPath); mediaPath != "" {
		block, addErr := loadCanvasImageBlock(mediaPath, t.canvasDir)
		if addErr != nil {
			result.Output += "\n\n[image attachment skipped: " + addErr.Error() + "]"
		} else if budgetErr := t.budget.Reserve(threadID, int64(len(block.Data))); budgetErr != nil {
			// Refuse the attachment when the per-thread budget is exhausted.
			// The model still gets the textual summary, plus a clear breadcrumb
			// explaining why the image is missing — that beats silently failing
			// (the model would otherwise re-fetch in a tight loop).
			result.Output += "\n\n[image attachment skipped: " + budgetErr.Error() + "]"
		} else {
			result.ContentBlocks = append(result.ContentBlocks, *block)
		}
	}

	return result, nil
}

// canvasDoc / canvasNode mirror the subset of fields persisted at
// pkg/server/handler.go:handleCanvasSave. We deliberately use json.RawMessage
// for unknown fields so format drift in the frontend does not break the tool.
type canvasDoc struct {
	Nodes []canvasNode `json:"nodes"`
	Edges []canvasEdge `json:"edges"`
}

type canvasNode struct {
	ID   string         `json:"id"`
	Type string         `json:"type"`
	Data canvasNodeData `json:"data"`
}

type canvasNodeData struct {
	NodeType      string            `json:"nodeType"`
	Label         string            `json:"label"`
	Status        string            `json:"status"`
	Content       string            `json:"content"`
	Prompt        string            `json:"prompt"`
	MediaPath     string            `json:"mediaPath"`
	MediaURL      string            `json:"mediaUrl"`
	SourceURL     string            `json:"sourceUrl"`
	SketchData    string            `json:"sketchData"`
	SketchBgImage string            `json:"sketchBgImage"`
	Engine        string            `json:"engine"`
	AspectRatio   string            `json:"aspectRatio"`
	Resolution    string            `json:"resolution"`
	Poses         []json.RawMessage `json:"poses"`
}

type canvasEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

// buildCanvasNodeSummary renders a compact, human-readable description of a
// node and the edges touching it.
func buildCanvasNodeSummary(n *canvasNode, edges []canvasEdge) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Canvas node %s\n", n.ID)
	fmt.Fprintf(&b, "  type: %s\n", coalesce(n.Data.NodeType, n.Type))
	if n.Data.Label != "" {
		fmt.Fprintf(&b, "  label: %s\n", n.Data.Label)
	}
	if n.Data.Status != "" {
		fmt.Fprintf(&b, "  status: %s\n", n.Data.Status)
	}
	if n.Data.Prompt != "" {
		fmt.Fprintf(&b, "  prompt: %s\n", canvasSafeText("prompt", n.Data.Prompt, 400))
	}
	if n.Data.Content != "" && n.Data.Content != n.Data.Prompt {
		fmt.Fprintf(&b, "  content: %s\n", canvasSafeText("content", n.Data.Content, 400))
	}
	if n.Data.Engine != "" {
		fmt.Fprintf(&b, "  engine: %s\n", n.Data.Engine)
	}
	if n.Data.AspectRatio != "" || n.Data.Resolution != "" {
		fmt.Fprintf(&b, "  aspect_ratio=%s resolution=%s\n", n.Data.AspectRatio, n.Data.Resolution)
	}
	if v := canvasSafeURL("mediaUrl", n.Data.MediaURL); v != "" {
		fmt.Fprintf(&b, "  mediaUrl: %s\n", v)
	}
	if v := canvasSafeURL("sourceUrl", n.Data.SourceURL); v != "" {
		fmt.Fprintf(&b, "  sourceUrl: %s\n", v)
	}
	if n.Data.MediaPath != "" {
		fmt.Fprintf(&b, "  hasLocalImage: true (returned as image ContentBlock)\n")
	}
	if n.Data.SketchData != "" || n.Data.SketchBgImage != "" {
		fmt.Fprintf(&b, "  sketch: true (poses=%d)\n", len(n.Data.Poses))
	}

	var inbound, outbound []string
	for _, e := range edges {
		if e.Source == n.ID {
			outbound = append(outbound, fmt.Sprintf("%s --%s--> %s", e.Source, defaultEdgeType(e.Type), e.Target))
		}
		if e.Target == n.ID {
			inbound = append(inbound, fmt.Sprintf("%s --%s--> %s", e.Source, defaultEdgeType(e.Type), e.Target))
		}
	}
	if len(inbound) > 0 {
		fmt.Fprintf(&b, "  inbound_edges:\n")
		for _, e := range inbound {
			fmt.Fprintf(&b, "    - %s\n", e)
		}
	}
	if len(outbound) > 0 {
		fmt.Fprintf(&b, "  outbound_edges:\n")
		for _, e := range outbound {
			fmt.Fprintf(&b, "    - %s\n", e)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// loadCanvasImageBlock reads the image at mediaPath and returns it as an
// image ContentBlock. The resolved path must live under the canvas-media
// directory paired with canvasDir (i.e. <canvasDir>/../canvas-media/). We
// also reject oversized files and unknown media types.
func loadCanvasImageBlock(mediaPath, canvasDir string) (*model.ContentBlock, error) {
	resolved, err := filepath.EvalSymlinks(mediaPath)
	if err != nil {
		// EvalSymlinks failed (e.g. missing file) — fall back to an absolute
		// form of the raw path so the shared loader surfaces the real error.
		if abs, aerr := filepath.Abs(mediaPath); aerr == nil {
			resolved = abs
		} else {
			resolved = mediaPath
		}
	} else if abs, aerr := filepath.Abs(resolved); aerr == nil {
		resolved = abs
	}

	expectedDir := filepath.Join(canvasDir, "..", canvasMediaSegment)
	if resolvedDir, rerr := filepath.EvalSymlinks(expectedDir); rerr == nil {
		expectedDir = resolvedDir
	}
	if abs, aerr := filepath.Abs(expectedDir); aerr == nil {
		expectedDir = abs
	}
	prefix := expectedDir + string(filepath.Separator)
	if !strings.HasPrefix(resolved, prefix) {
		return nil, fmt.Errorf("path is outside %s/", canvasMediaSegment)
	}

	block, _, _, err := LoadImageBlockFromFile(resolved, canvasMaxImageBytes)
	if err != nil {
		return nil, err
	}
	return block, nil
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func defaultEdgeType(t string) string {
	if t == "" {
		return "flow"
	}
	return t
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// canvasListNodesDescription explains the on-demand canvas summary tool.
const canvasListNodesDescription = `Lists all nodes (and edges) currently on the user's canvas for the active thread.

Returns a compact text digest with each node's ID, type, label, status, and
whether it has bound media. Use this when the user references "the image",
"my sketch", "node X", or otherwise needs you to know what's on the canvas
before generating new media or calling canvas_get_node.

Cheaper than prepending the full canvas state to every prompt — call this
on demand only when the conversation is canvas-related.

Parameters:
- thread_id (optional): Thread ID. Defaults to the injected current thread.`

var canvasListNodesSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]any{
		"thread_id": map[string]any{
			"type":        "string",
			"description": "Optional. Defaults to the current chat thread when the server has injected it.",
		},
	},
}

// CanvasListNodesTool returns the canvas summary for a thread on demand. It
// shares the same JSON storage as CanvasGetNodeTool.
type CanvasListNodesTool struct {
	canvasDir string
}

// NewCanvasListNodesTool builds the on-demand canvas summary tool.
func NewCanvasListNodesTool(canvasDir string) *CanvasListNodesTool {
	return &CanvasListNodesTool{canvasDir: canvasDir}
}

func (t *CanvasListNodesTool) Name() string             { return "canvas_list_nodes" }
func (t *CanvasListNodesTool) Description() string      { return canvasListNodesDescription }
func (t *CanvasListNodesTool) Schema() *tool.JSONSchema { return canvasListNodesSchema }

// Execute reads the canvas JSON for the resolved thread and returns a digest.
func (t *CanvasListNodesTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if t == nil || strings.TrimSpace(t.canvasDir) == "" {
		return nil, errors.New("canvas_list_nodes: not available (no canvas directory configured)")
	}

	threadID, _ := params["thread_id"].(string)
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		threadID = ThreadIDFromContext(ctx)
	}
	if threadID == "" {
		return nil, errors.New("canvas_list_nodes: thread_id is required (no thread injected via context)")
	}
	if !canvasIDPattern.MatchString(threadID) {
		return nil, fmt.Errorf("canvas_list_nodes: invalid thread_id %q", threadID)
	}

	canvasPath := filepath.Join(t.canvasDir, threadID+".json")
	raw, err := os.ReadFile(canvasPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &tool.ToolResult{
				Success: true,
				Output:  fmt.Sprintf("thread_id: %s\n(canvas is empty — no nodes have been created yet)", threadID),
				Data: map[string]any{
					"thread_id":  threadID,
					"node_count": 0,
				},
			}, nil
		}
		return nil, fmt.Errorf("canvas_list_nodes: read canvas: %w", err)
	}

	var doc canvasDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("canvas_list_nodes: parse canvas: %w", err)
	}

	summary := buildCanvasListSummary(threadID, &doc)
	return &tool.ToolResult{
		Success: true,
		Output:  summary,
		Data: map[string]any{
			"thread_id":  threadID,
			"node_count": len(doc.Nodes),
			"edge_count": len(doc.Edges),
		},
	}, nil
}

// buildCanvasListSummary mirrors handler.loadCanvasSummary but operates on the
// types declared here so the LLM-facing tool stays self-contained. Output is
// safe to embed inside <canvas_state>…</canvas_state> (angle brackets in
// user-controlled fields are substituted with guillemets).
func buildCanvasListSummary(threadID string, doc *canvasDoc) string {
	const maxDetailedNodes = 50
	const maxEdges = 100

	var b strings.Builder
	fmt.Fprintf(&b, "thread_id: %s\n", threadID)
	fmt.Fprintf(&b, "nodes (%d):\n", len(doc.Nodes))

	if len(doc.Nodes) > maxDetailedNodes {
		for _, n := range doc.Nodes {
			fmt.Fprintf(&b, "  - %s [%s] %s\n",
				n.ID,
				coalesce(n.Data.NodeType, n.Type),
				canvasSafeListField(n.Data.Label, 60),
			)
		}
		fmt.Fprintf(&b, "(canvas has %d nodes; use canvas_get_node for details)\n", len(doc.Nodes))
	} else {
		for _, n := range doc.Nodes {
			fmt.Fprintf(&b, "  - %s [%s]", n.ID, coalesce(n.Data.NodeType, n.Type))
			if n.Data.Label != "" {
				fmt.Fprintf(&b, " label=%q", canvasSafeListField(n.Data.Label, 60))
			}
			if n.Data.Status != "" {
				fmt.Fprintf(&b, " status=%s", n.Data.Status)
			}
			hasMedia := n.Data.MediaPath != "" || n.Data.MediaURL != "" || n.Data.SourceURL != ""
			if hasMedia {
				b.WriteString(" hasMedia=true")
			}
			if n.Data.SketchData != "" || len(n.Data.Poses) > 0 {
				fmt.Fprintf(&b, " sketch=true poses=%d", len(n.Data.Poses))
			}
			if n.Data.Prompt != "" {
				fmt.Fprintf(&b, " prompt=%q", canvasSafeListField(n.Data.Prompt, 80))
			}
			b.WriteString("\n")
		}
	}

	if len(doc.Edges) > 0 {
		fmt.Fprintf(&b, "edges (%d):\n", len(doc.Edges))
		if len(doc.Edges) > maxEdges {
			fmt.Fprintf(&b, "  (omitted; canvas has %d edges)\n", len(doc.Edges))
		} else {
			for _, e := range doc.Edges {
				fmt.Fprintf(&b, "  - %s --%s--> %s\n", e.Source, defaultEdgeType(e.Type), e.Target)
			}
		}
	}
	b.WriteString("Use canvas_get_node(node_id) to fetch a node's full contents (image attachments included where available).")
	return b.String()
}

// canvasTruncate collapses newlines and clips overly long values.
func canvasTruncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// canvasSanitize swaps angle brackets for visually similar guillemets so user
// labels/prompts cannot close the enclosing <canvas_state> wrapper.
func canvasSanitize(s string) string {
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, "<", "‹")
	s = strings.ReplaceAll(s, ">", "›")
	return s
}

// canvasSafeURL returns a model-safe representation of a canvas URL field.
// Inlining a data: URI bloats the model context (a single sketch is often
// 10k+ tokens of base64) and confused the model into emitting empty tool
// calls — see the eddaff17 thread incident. The image, when present, is
// already attached as an image ContentBlock by loadCanvasImageBlock, so the
// raw bytes are redundant here. Plain http(s) / API paths pass through.
func canvasSafeURL(label, u string) string {
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "data:") {
		return fmt.Sprintf("<%s data URI omitted; %d bytes; image attached as content block when available>", label, len(u))
	}
	return u
}

// canvasSafeText is the defense-in-depth twin of canvasSafeURL for free-text
// fields (prompt / content). The frontend should never put a data: URI here,
// but if a future bug routes one through (e.g. drag-drop accidentally pastes
// the encoded blob), the same eddaff17 failure mode would recur — a 400-char
// truncated base64 prefix is still pure garbage to the model. Detect it and
// replace with the same placeholder, otherwise truncate normally.
func canvasSafeText(label, s string, maxLen int) string {
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "data:") {
		return fmt.Sprintf("<%s data URI omitted; %d bytes; image attached as content block when available>", label, len(s))
	}
	return truncate(s, maxLen)
}

// canvasSafeListField is the list-summary variant: same data: URI defense as
// canvasSafeText, but the placeholder uses guillemets and the surviving prose
// is run through canvasSanitize so user-controlled content cannot close the
// wrapping <canvas_state> block. Used by buildCanvasListSummary for label /
// prompt fields.
func canvasSafeListField(s string, maxLen int) string {
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "data:") {
		return fmt.Sprintf("‹data URI omitted; %d bytes›", len(s))
	}
	return canvasSanitize(canvasTruncate(s, maxLen))
}
