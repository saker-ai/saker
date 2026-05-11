package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// canvasDir returns the legacy single-project canvas directory. New call
// sites must go through pathsFor(ctx).CanvasDir so multi-tenant requests
// land in the per-project directory; this helper stays for the few legacy
// places that still build paths without ctx (loadCanvasSummary fallback).
func (h *Handler) canvasDir() string {
	return filepath.Join(h.dataDir, "canvas")
}

func (h *Handler) handleCanvasSave(ctx context.Context, req Request) Response {
	threadID, _ := req.Params["threadId"].(string)
	if threadID == "" {
		return h.invalidParams(req.ID, "threadId is required")
	}

	data := map[string]any{
		"nodes":    stripCanvasNodeDataURIs(req.Params["nodes"]),
		"edges":    req.Params["edges"],
		"viewport": req.Params["viewport"],
	}

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return h.internalError(req.ID, "marshal canvas: "+err.Error())
	}

	dir := h.pathsFor(ctx).CanvasDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return h.internalError(req.ID, "create canvas dir: "+err.Error())
	}

	path := filepath.Join(dir, threadID+".json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return h.internalError(req.ID, "write canvas: "+err.Error())
	}

	return h.success(req.ID, map[string]any{"ok": true})
}

func (h *Handler) handleCanvasLoad(ctx context.Context, req Request) Response {
	threadID, _ := req.Params["threadId"].(string)
	if threadID == "" {
		return h.invalidParams(req.ID, "threadId is required")
	}

	path := filepath.Join(h.pathsFor(ctx).CanvasDir, threadID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return h.success(req.ID, map[string]any{"nodes": []any{}, "edges": []any{}, "viewport": nil})
		}
		return h.internalError(req.ID, "read canvas: "+err.Error())
	}

	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return h.internalError(req.ID, "parse canvas: "+err.Error())
	}

	return h.success(req.ID, data)
}

// loadCanvasSummary returns a compact human-readable digest of the saved
// canvas for threadID, or "" when no canvas data exists or the file cannot be
// parsed. The returned string is meant to be wrapped in <canvas_state>…</canvas_state>
// and prepended to the user's prompt so the agent can reference existing nodes
// (and use canvas_get_node to fetch their contents).
func (h *Handler) loadCanvasSummary(ctx context.Context, threadID string) string {
	if h == nil || strings.TrimSpace(threadID) == "" || h.dataDir == "" {
		return ""
	}
	path := filepath.Join(h.pathsFor(ctx).CanvasDir, threadID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	var doc struct {
		Nodes []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Data struct {
				NodeType    string            `json:"nodeType"`
				Label       string            `json:"label"`
				Status      string            `json:"status"`
				MediaURL    string            `json:"mediaUrl"`
				MediaPath   string            `json:"mediaPath"`
				SourceURL   string            `json:"sourceUrl"`
				SketchData  string            `json:"sketchData"`
				Poses       []json.RawMessage `json:"poses"`
				Prompt      string            `json:"prompt"`
				AspectRatio string            `json:"aspectRatio"`
			} `json:"data"`
		} `json:"nodes"`
		Edges []struct {
			Source string `json:"source"`
			Target string `json:"target"`
			Type   string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	if len(doc.Nodes) == 0 && len(doc.Edges) == 0 {
		return ""
	}

	const maxDetailedNodes = 50
	var b strings.Builder
	fmt.Fprintf(&b, "thread_id: %s\n", threadID)
	fmt.Fprintf(&b, "nodes (%d):\n", len(doc.Nodes))
	if len(doc.Nodes) > maxDetailedNodes {
		// Degrade to ID-only listing when canvas is huge.
		for _, n := range doc.Nodes {
			fmt.Fprintf(&b, "  - %s [%s] %s\n", n.ID, firstNonEmpty(n.Data.NodeType, n.Type), sanitizeCanvasText(truncateLine(n.Data.Label, 60)))
		}
		fmt.Fprintf(&b, "(canvas has %d nodes; use canvas_get_node for details)\n", len(doc.Nodes))
	} else {
		for _, n := range doc.Nodes {
			nodeType := firstNonEmpty(n.Data.NodeType, n.Type)
			fmt.Fprintf(&b, "  - %s [%s]", n.ID, nodeType)
			if n.Data.Label != "" {
				fmt.Fprintf(&b, " label=%q", sanitizeCanvasText(truncateLine(n.Data.Label, 60)))
			}
			if n.Data.Status != "" {
				fmt.Fprintf(&b, " status=%s", n.Data.Status)
			}
			hasMedia := n.Data.MediaPath != "" || n.Data.MediaURL != "" || n.Data.SourceURL != ""
			if hasMedia {
				fmt.Fprintf(&b, " hasMedia=true")
			}
			if n.Data.SketchData != "" || len(n.Data.Poses) > 0 {
				fmt.Fprintf(&b, " sketch=true poses=%d", len(n.Data.Poses))
			}
			if n.Data.Prompt != "" {
				fmt.Fprintf(&b, " prompt=%q", sanitizeCanvasText(truncateLine(n.Data.Prompt, 80)))
			}
			b.WriteString("\n")
		}
	}

	if len(doc.Edges) > 0 {
		const maxEdges = 100
		fmt.Fprintf(&b, "edges (%d):\n", len(doc.Edges))
		if len(doc.Edges) > maxEdges {
			fmt.Fprintf(&b, "  (omitted; canvas has %d edges)\n", len(doc.Edges))
		} else {
			for _, e := range doc.Edges {
				edgeType := e.Type
				if edgeType == "" {
					edgeType = "flow"
				}
				fmt.Fprintf(&b, "  - %s --%s--> %s\n", e.Source, edgeType, e.Target)
			}
		}
	}
	b.WriteString("Use canvas_get_node(thread_id, node_id) to fetch a node's full contents (image attachments included where available). For canvasTable nodes, use canvas_table_write(thread_id, node_id, operation, ...) to populate columns/rows directly — never fall back to bash heredoc or file_write for table content.")
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncateLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// canvasKeywords are the substrings whose presence in the user's prompt
// trigger eager loading of the <canvas_state> block. Matching is
// case-insensitive on the lower-cased prompt for ASCII; CJK keywords match
// directly. The list intentionally errs on the side of inclusion — false
// positives only cost a few hundred tokens, but a false negative means the
// agent has to spend a tool call to fetch context.
var canvasKeywords = []string{
	"canvas",
	"node_",
	"sketch",
	"image",
	"video",
	"thumbnail",
	"reference",
	"pose",
	"画布",
	"节点",
	"图片",
	"图像",
	"草图",
	"视频",
	"参考图",
}

// promptMentionsCanvas reports whether prompt contains any cue that the user
// is referring to canvas state. Used to decide whether to prepend the
// <canvas_state> block (eager) or rely on canvas_list_nodes (lazy).
func promptMentionsCanvas(prompt string) bool {
	if strings.TrimSpace(prompt) == "" {
		return false
	}
	lower := strings.ToLower(prompt)
	for _, kw := range canvasKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// sanitizeCanvasText substitutes angle brackets with visually similar Unicode
// guillemets so user-controlled node labels/prompts cannot close the enclosing
// <canvas_state>…</canvas_state> wrapper with a tag of their own. The
// substitution is reversible for humans but opaque to tag matchers.
func sanitizeCanvasText(s string) string {
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, "<", "‹")
	s = strings.ReplaceAll(s, ">", "›")
	return s
}
