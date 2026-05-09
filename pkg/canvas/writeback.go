package canvas

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// looksLikeMediaURL is a deliberately-loose shape check. The bug it guards
// against is engines returning opaque task-id UUIDs (e.g. "c424ec41-...") and
// having those silently written into <video src=>. Anything with a scheme or
// a leading slash is plausibly a fetchable URL and is allowed through; the
// stricter validation lives one layer up in pkg/tool/builtin/aigo where we
// know the engine should always produce http(s)/data URIs.
func looksLikeMediaURL(v string) bool {
	if v == "" {
		return false
	}
	if strings.HasPrefix(v, "/") {
		return true
	}
	switch {
	case strings.HasPrefix(v, "http://"),
		strings.HasPrefix(v, "https://"),
		strings.HasPrefix(v, "data:"),
		strings.HasPrefix(v, "blob:"),
		strings.HasPrefix(v, "file://"):
		return true
	}
	return false
}

// GenHistoryEntry mirrors the TypeScript GenHistoryEntry pushed onto a gen
// node's data.generationHistory by useGenerate.ts. Field names match the
// frontend exactly so the UI can rehydrate without a translation step.
type GenHistoryEntry struct {
	ID            string         `json:"id"`
	Prompt        string         `json:"prompt"`
	MediaURL      string         `json:"mediaUrl"`
	MediaPath     string         `json:"mediaPath,omitempty"`
	Params        map[string]any `json:"params,omitempty"`
	CreatedAt     int64          `json:"createdAt"`
	Status        string         `json:"status"`
	Error         string         `json:"error,omitempty"`
	ResultNodeIDs []string       `json:"resultNodeIds,omitempty"`
}

// nowMillis returns the current time in milliseconds since epoch — the
// frontend uses Date.now(), so we emit the same shape.
func nowMillis() int64 {
	return time.Now().UnixMilli()
}

// shortToken returns a 4-byte random hex string used to suffix generated
// IDs. Mirrors the `Math.random().toString(36).slice(2,6)` token in
// useGenerate.ts. Falls back to a timestamp when /dev/urandom is unhappy.
func shortToken() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())[:8]
	}
	return hex.EncodeToString(b[:])
}

// MarkRunning flips a gen node into the "running" state and records the
// start time, mirroring useGenerate.ts:46-49.
func MarkRunning(node *Node) {
	if node == nil {
		return
	}
	if node.Data == nil {
		node.Data = map[string]any{}
	}
	node.Data["status"] = NodeStatusRunning
	node.Data["generating"] = true
	node.Data["startTime"] = nowMillis()
	delete(node.Data, "error")
	delete(node.Data, "lastErrorParams")
}

// MarkPending flips a gen node into the post-success "pending" state used
// by the UI (matches useGenerate.ts:122-129).
func MarkPending(node *Node) {
	if node == nil {
		return
	}
	if node.Data == nil {
		node.Data = map[string]any{}
	}
	node.Data["status"] = NodeStatusPending
	node.Data["generating"] = false
	node.Data["endTime"] = nowMillis()
	delete(node.Data, "error")
	delete(node.Data, "genProgress")
}

// MarkError records a failure on a gen node, including a JSON-stringified
// snapshot of the params that were dispatched (matches useGenerate.ts:146-154
// — the frontend uses `lastErrorParams` to drive its retry button).
func MarkError(node *Node, msg string, params map[string]any) {
	if node == nil {
		return
	}
	if node.Data == nil {
		node.Data = map[string]any{}
	}
	node.Data["status"] = NodeStatusError
	node.Data["generating"] = false
	node.Data["error"] = msg
	node.Data["endTime"] = nowMillis()
	if params != nil {
		if raw, err := json.Marshal(params); err == nil {
			node.Data["lastErrorParams"] = string(raw)
		}
	}
}

// AppendResultNode adds a result media node to the document and returns
// its generated ID. Layout offsets mirror useGenerate.ts:80-95 so
// browser-saved canvases and executor-produced canvases position result
// nodes identically.
func AppendResultNode(doc *Document, gen *Node, mediaType, mediaURL, mediaPath, sourceURL, label string) string {
	if doc == nil || gen == nil {
		return ""
	}
	id := fmt.Sprintf("node_%d_%s", nowMillis(), shortToken())
	pos := Position{X: gen.Position.X + 350, Y: gen.Position.Y}

	// Defensive guard: if mediaURL doesn't look like a fetchable URL, refuse to
	// store it on a "done" node — that's how we ended up writing async task
	// UUIDs into <video src=> and silently breaking the canvas. Emit an error
	// node instead so the failure is visible to the user and the agent loop.
	if mediaURL != "" && !looksLikeMediaURL(mediaURL) {
		slog.Default().Warn("canvas: refusing to write non-URL mediaUrl",
			"mediaUrl", mediaURL,
			"mediaType", mediaType,
			"genNode", gen.ID,
		)
		errMsg := fmt.Sprintf("invalid media url %q (likely an unresumed async task id)", mediaURL)
		data := map[string]any{
			"nodeType": mediaType,
			"label":    label,
			"status":   NodeStatusError,
			"error":    errMsg,
		}
		doc.Nodes = append(doc.Nodes, &Node{
			ID:       id,
			Type:     mediaType,
			Position: pos,
			Data:     data,
		})
		return id
	}

	data := map[string]any{
		"nodeType":  mediaType,
		"label":     label,
		"mediaUrl":  mediaURL,
		"mediaType": mediaType,
		"status":    NodeStatusDone,
	}
	if mediaPath != "" {
		data["mediaPath"] = mediaPath
	}
	if sourceURL != "" {
		data["sourceUrl"] = sourceURL
	}
	doc.Nodes = append(doc.Nodes, &Node{
		ID:       id,
		Type:     mediaType,
		Position: pos,
		Data:     data,
	})
	return id
}

// AppendFlowEdge adds a flow edge between two nodes and returns its ID.
// Edge IDs follow the same `edge_<source>_<target>` shape used by
// useGenerate.ts:96 so the React Flow renderer treats them as stable.
func AppendFlowEdge(doc *Document, sourceID, targetID string) string {
	if doc == nil || sourceID == "" || targetID == "" {
		return ""
	}
	id := fmt.Sprintf("edge_%s_%s", sourceID, targetID)
	doc.Edges = append(doc.Edges, &Edge{
		ID:     id,
		Source: sourceID,
		Target: targetID,
		Type:   EdgeFlow,
	})
	return id
}

// AppendGenHistory pushes an entry onto a gen node's generationHistory,
// preserving order (newest first) and capping the slice at MaxGenHistory.
// The frontend stores history as an array of objects, so we emit the same
// shape via the json package's struct tags.
func AppendGenHistory(node *Node, entry GenHistoryEntry) {
	if node == nil {
		return
	}
	if node.Data == nil {
		node.Data = map[string]any{}
	}
	hist := loadHistory(node.Data["generationHistory"])
	hist = append([]map[string]any{historyToMap(entry)}, hist...)
	if len(hist) > MaxGenHistory {
		hist = hist[:MaxGenHistory]
	}
	node.Data["generationHistory"] = hist
	node.Data["activeHistoryIndex"] = 0
}

// NewHistoryEntryID returns a fresh `gh_<ms>_<token>` ID matching
// useGenerate.ts.
func NewHistoryEntryID() string {
	return fmt.Sprintf("gh_%d_%s", nowMillis(), shortToken()[:4])
}

func loadHistory(v any) []map[string]any {
	if v == nil {
		return nil
	}
	if arr, ok := v.([]map[string]any); ok {
		return arr
	}
	if arr, ok := v.([]any); ok {
		out := make([]map[string]any, 0, len(arr))
		for _, e := range arr {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func historyToMap(e GenHistoryEntry) map[string]any {
	m := map[string]any{
		"id":        e.ID,
		"prompt":    e.Prompt,
		"mediaUrl":  e.MediaURL,
		"createdAt": e.CreatedAt,
		"status":    e.Status,
	}
	if e.MediaPath != "" {
		m["mediaPath"] = e.MediaPath
	}
	if e.Params != nil {
		m["params"] = e.Params
	}
	if e.Error != "" {
		m["error"] = e.Error
	}
	if len(e.ResultNodeIDs) > 0 {
		m["resultNodeIds"] = e.ResultNodeIDs
	}
	return m
}
