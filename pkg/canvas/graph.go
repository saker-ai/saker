package canvas

import (
	"fmt"
	"sort"
)

// Graph wraps a Document with adjacency indexes used during execution.
// It is read-only after construction; mutations to the underlying Document
// (status flips, result-node appends) are made via the Document directly.
type Graph struct {
	doc      *Document
	byID     map[string]*Node
	outgoing map[string][]*Edge // nodeID → edges where Source == nodeID
	incoming map[string][]*Edge // nodeID → edges where Target == nodeID
}

// NewGraph indexes the document for fast lookups. Edges referencing missing
// nodes are dropped silently (matches the frontend's defensive walks where
// dangling edges are ignored rather than throwing).
func NewGraph(doc *Document) *Graph {
	g := &Graph{
		doc:      doc,
		byID:     make(map[string]*Node, len(doc.Nodes)),
		outgoing: make(map[string][]*Edge, len(doc.Nodes)),
		incoming: make(map[string][]*Edge, len(doc.Nodes)),
	}
	for _, n := range doc.Nodes {
		if n == nil || n.ID == "" {
			continue
		}
		g.byID[n.ID] = n
	}
	for _, e := range doc.Edges {
		if e == nil {
			continue
		}
		if _, ok := g.byID[e.Source]; !ok {
			continue
		}
		if _, ok := g.byID[e.Target]; !ok {
			continue
		}
		g.outgoing[e.Source] = append(g.outgoing[e.Source], e)
		g.incoming[e.Target] = append(g.incoming[e.Target], e)
	}
	return g
}

// Doc returns the underlying document.
func (g *Graph) Doc() *Document { return g.doc }

// Get returns the node with the given ID, or nil.
func (g *Graph) Get(id string) *Node {
	if g == nil {
		return nil
	}
	return g.byID[id]
}

// Incoming returns edges pointing at the given node, filtered to the
// requested edge types. An empty types slice returns all incoming edges.
func (g *Graph) Incoming(nodeID string, types ...string) []*Edge {
	all := g.incoming[nodeID]
	if len(types) == 0 {
		return all
	}
	allow := make(map[string]bool, len(types))
	for _, t := range types {
		allow[t] = true
	}
	out := make([]*Edge, 0, len(all))
	for _, e := range all {
		if allow[e.Type] {
			out = append(out, e)
		}
	}
	return out
}

// IsExecutionEdge reports whether the edge's type participates in
// topological execution ordering. Context edges are LLM-only hints and
// are intentionally ignored so they cannot create false cycles.
func IsExecutionEdge(e *Edge) bool {
	if e == nil {
		return false
	}
	switch e.Type {
	case EdgeFlow, EdgeReference, "":
		// An empty edge type defaults to flow on the frontend; treat it
		// the same way here so older saves still topologically sort.
		return true
	default:
		return false
	}
}

// TopoOrder returns node IDs in execution order using Kahn's algorithm
// over flow+reference edges. Returns an error containing the offending
// node IDs when a cycle is detected. Order between independent nodes is
// stable (sorted by node ID) so test assertions are deterministic.
func (g *Graph) TopoOrder() ([]string, error) {
	indeg := make(map[string]int, len(g.byID))
	ids := make([]string, 0, len(g.byID))
	for id := range g.byID {
		ids = append(ids, id)
		indeg[id] = 0
	}
	for _, e := range g.doc.Edges {
		if !IsExecutionEdge(e) {
			continue
		}
		if _, ok := g.byID[e.Source]; !ok {
			continue
		}
		if _, ok := g.byID[e.Target]; !ok {
			continue
		}
		indeg[e.Target]++
	}

	sort.Strings(ids)
	ready := make([]string, 0, len(ids))
	for _, id := range ids {
		if indeg[id] == 0 {
			ready = append(ready, id)
		}
	}

	out := make([]string, 0, len(ids))
	for len(ready) > 0 {
		// Pop in sorted order to keep results deterministic.
		sort.Strings(ready)
		next := ready[0]
		ready = ready[1:]
		out = append(out, next)
		for _, e := range g.outgoing[next] {
			if !IsExecutionEdge(e) {
				continue
			}
			indeg[e.Target]--
			if indeg[e.Target] == 0 {
				ready = append(ready, e.Target)
			}
		}
	}

	if len(out) != len(ids) {
		stuck := make([]string, 0, len(ids)-len(out))
		for id, d := range indeg {
			if d > 0 {
				stuck = append(stuck, id)
			}
		}
		sort.Strings(stuck)
		return nil, fmt.Errorf("canvas: cycle detected involving nodes %v", stuck)
	}
	return out, nil
}
