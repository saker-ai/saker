// Package canvas implements a server-side executor for stored canvas DAGs.
//
// The browser persists canvas snapshots at <dataDir>/canvas/{threadID}.json
// via Handler.handleCanvasSave. This package loads those snapshots, walks
// them in topological order, and dispatches gen nodes through the runtime's
// existing tool path so external callers can trigger canvases without a
// browser session.
package canvas

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Document is the on-disk shape written by handleCanvasSave.
type Document struct {
	Nodes    []*Node        `json:"nodes"`
	Edges    []*Edge        `json:"edges"`
	Viewport map[string]any `json:"viewport,omitempty"`
}

// Node mirrors the React Flow node shape. Data is intentionally a free-form
// map so we never have to chase frontend schema drift; helpers in this package
// extract typed fields where needed.
type Node struct {
	ID       string         `json:"id"`
	Type     string         `json:"type,omitempty"`
	Position Position       `json:"position"`
	Data     map[string]any `json:"data,omitempty"`
}

// Position matches React Flow's {x, y}.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Edge mirrors the React Flow edge shape.
type Edge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type,omitempty"`
}

// Edge type constants. Mirrors EdgeType in web/src/features/canvas/types.ts.
const (
	EdgeFlow      = "flow"
	EdgeReference = "reference"
	EdgeContext   = "context"
)

// CanvasPath returns the on-disk path for the given thread's canvas JSON.
func CanvasPath(dataDir, threadID string) string {
	return filepath.Join(dataDir, "canvas", threadID+".json")
}

// Load reads and parses a thread's canvas document. Returns an empty document
// (not an error) when the file does not exist, matching handleCanvasLoad's
// behaviour so callers can treat "no canvas yet" as a normal state.
func Load(dataDir, threadID string) (*Document, error) {
	if dataDir == "" {
		return nil, errors.New("canvas: dataDir is empty")
	}
	if strings.TrimSpace(threadID) == "" {
		return nil, errors.New("canvas: threadID is empty")
	}
	if strings.ContainsAny(threadID, "/\\") || strings.Contains(threadID, "..") {
		return nil, fmt.Errorf("canvas: invalid threadID %q", threadID)
	}

	path := CanvasPath(dataDir, threadID)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Document{Nodes: []*Node{}, Edges: []*Edge{}}, nil
		}
		return nil, fmt.Errorf("canvas: read %s: %w", path, err)
	}

	doc := &Document{}
	if err := json.Unmarshal(raw, doc); err != nil {
		return nil, fmt.Errorf("canvas: parse %s: %w", path, err)
	}
	if doc.Nodes == nil {
		doc.Nodes = []*Node{}
	}
	if doc.Edges == nil {
		doc.Edges = []*Edge{}
	}
	return doc, nil
}

// Save atomically writes the document to disk via tmp+rename so that a
// crash mid-write cannot leave a corrupt canvas file. Indentation matches
// handleCanvasSave (two-space) so diffs against browser-saved files are
// readable.
func Save(dataDir, threadID string, doc *Document) error {
	if dataDir == "" {
		return errors.New("canvas: dataDir is empty")
	}
	if strings.TrimSpace(threadID) == "" {
		return errors.New("canvas: threadID is empty")
	}
	if strings.ContainsAny(threadID, "/\\") || strings.Contains(threadID, "..") {
		return fmt.Errorf("canvas: invalid threadID %q", threadID)
	}
	if doc == nil {
		return errors.New("canvas: doc is nil")
	}

	dir := filepath.Join(dataDir, "canvas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("canvas: mkdir %s: %w", dir, err)
	}

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("canvas: marshal: %w", err)
	}

	final := filepath.Join(dir, threadID+".json")
	tmp, err := os.CreateTemp(dir, threadID+".*.tmp")
	if err != nil {
		return fmt.Errorf("canvas: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("canvas: write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("canvas: close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, final); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("canvas: rename tmp→final: %w", err)
	}
	return nil
}

// FindNode returns the node with the given ID, or nil.
func (d *Document) FindNode(id string) *Node {
	if d == nil {
		return nil
	}
	for _, n := range d.Nodes {
		if n != nil && n.ID == id {
			return n
		}
	}
	return nil
}

// NodeType returns the data.nodeType discriminator (the value the frontend
// uses to switch behaviour). Falls back to Node.Type if data.nodeType is
// missing, matching how the browser tolerates older saves.
func (n *Node) NodeType() string {
	if n == nil {
		return ""
	}
	if n.Data != nil {
		if s, ok := n.Data["nodeType"].(string); ok && s != "" {
			return s
		}
	}
	return n.Type
}

// DataString reads a string field from Data, returning "" when missing.
func (n *Node) DataString(key string) string {
	if n == nil || n.Data == nil {
		return ""
	}
	s, _ := n.Data[key].(string)
	return s
}
