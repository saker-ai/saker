package canvas

import (
	"strings"
	"testing"
)

func docFrom(nodes []*Node, edges []*Edge) *Document {
	return &Document{Nodes: nodes, Edges: edges}
}

func TestTopoLinear(t *testing.T) {
	t.Parallel()
	doc := docFrom(
		[]*Node{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		[]*Edge{
			{Source: "a", Target: "b", Type: EdgeFlow},
			{Source: "b", Target: "c", Type: EdgeFlow},
		},
	)
	got, err := NewGraph(doc).TopoOrder()
	if err != nil {
		t.Fatalf("topo: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTopoIgnoresContextEdges(t *testing.T) {
	t.Parallel()
	// A context-edge cycle a→b→a must not block execution.
	doc := docFrom(
		[]*Node{{ID: "a"}, {ID: "b"}},
		[]*Edge{
			{Source: "a", Target: "b", Type: EdgeContext},
			{Source: "b", Target: "a", Type: EdgeContext},
		},
	)
	if _, err := NewGraph(doc).TopoOrder(); err != nil {
		t.Fatalf("context cycle should be ignored: %v", err)
	}
}

func TestTopoDetectsFlowCycle(t *testing.T) {
	t.Parallel()
	doc := docFrom(
		[]*Node{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		[]*Edge{
			{Source: "a", Target: "b", Type: EdgeFlow},
			{Source: "b", Target: "c", Type: EdgeFlow},
			{Source: "c", Target: "a", Type: EdgeReference},
		},
	)
	_, err := NewGraph(doc).TopoOrder()
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestTopoBranchAndDiamond(t *testing.T) {
	t.Parallel()
	// a → b → d
	//   ↘ c ↗
	doc := docFrom(
		[]*Node{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}},
		[]*Edge{
			{Source: "a", Target: "b", Type: EdgeFlow},
			{Source: "a", Target: "c", Type: EdgeFlow},
			{Source: "b", Target: "d", Type: EdgeFlow},
			{Source: "c", Target: "d", Type: EdgeFlow},
		},
	)
	got, err := NewGraph(doc).TopoOrder()
	if err != nil {
		t.Fatalf("topo: %v", err)
	}
	pos := map[string]int{}
	for i, id := range got {
		pos[id] = i
	}
	if pos["a"] >= pos["b"] || pos["b"] >= pos["d"] || pos["c"] >= pos["d"] {
		t.Fatalf("topo order violated: %v", got)
	}
}

func TestTopoIgnoresDanglingEdges(t *testing.T) {
	t.Parallel()
	// Edge references missing node "ghost" — should be silently dropped.
	doc := docFrom(
		[]*Node{{ID: "a"}, {ID: "b"}},
		[]*Edge{
			{Source: "a", Target: "b", Type: EdgeFlow},
			{Source: "ghost", Target: "b", Type: EdgeFlow},
		},
	)
	got, err := NewGraph(doc).TopoOrder()
	if err != nil {
		t.Fatalf("topo: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 nodes, got %v", got)
	}
}

func TestTopoEmptyDoc(t *testing.T) {
	t.Parallel()
	got, err := NewGraph(docFrom(nil, nil)).TopoOrder()
	if err != nil {
		t.Fatalf("empty topo: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestTopoEmptyEdgeTypeDefaultsToFlow(t *testing.T) {
	t.Parallel()
	// Older saves may have edges with no Type — they're displayed as flow
	// in the UI, so they must order execution too.
	doc := docFrom(
		[]*Node{{ID: "a"}, {ID: "b"}},
		[]*Edge{{Source: "a", Target: "b"}}, // no type
	)
	got, err := NewGraph(doc).TopoOrder()
	if err != nil {
		t.Fatalf("topo: %v", err)
	}
	if got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
}
