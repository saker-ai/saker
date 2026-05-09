package artifact

import (
	"strings"
	"testing"
)

func TestLineageGraphRecordsDerivedArtifacts(t *testing.T) {
	src := NewLocalFileRef("/tmp/source.png", ArtifactKindImage)
	derived := NewGeneratedRef("art_thumb", ArtifactKindImage)

	var graph LineageGraph
	graph.AddEdge(src, derived, "thumbnail")

	if len(graph.Edges) != 1 {
		t.Fatalf("expected one lineage edge, got %+v", graph.Edges)
	}
	if graph.Edges[0].Operation != "thumbnail" {
		t.Fatalf("expected operation to be preserved, got %+v", graph.Edges[0])
	}
	if got := graph.ChildrenOf(src); len(got) != 1 || got[0] != derived {
		t.Fatalf("expected derived child to be discoverable, got %+v", got)
	}
}

func TestLineageGraphPreservesProvenanceAcrossMultiStepPipeline(t *testing.T) {
	raw := NewLocalFileRef("/tmp/raw.mov", ArtifactKindVideo)
	audio := NewGeneratedRef("art_audio", ArtifactKindAudio)
	transcript := NewGeneratedRef("art_text", ArtifactKindText)

	var graph LineageGraph
	graph.AddEdge(raw, audio, "extract-audio")
	graph.AddEdge(audio, transcript, "transcribe")

	ancestors := graph.AncestorsOf(transcript)
	if len(ancestors) != 2 {
		t.Fatalf("expected full provenance chain, got %+v", ancestors)
	}
	if ancestors[0] != audio || ancestors[1] != raw {
		t.Fatalf("expected nearest-to-root ancestry ordering, got %+v", ancestors)
	}
}

func TestToDOTEmpty(t *testing.T) {
	var g LineageGraph
	dot := g.ToDOT()
	if !strings.Contains(dot, "digraph lineage") {
		t.Fatalf("expected valid DOT even for empty graph, got: %s", dot)
	}
}

func TestToDOTWithEdges(t *testing.T) {
	var g LineageGraph
	src := NewLocalFileRef("/tmp/video.mp4", ArtifactKindVideo)
	frame := NewGeneratedRef("frame_0", ArtifactKindImage)
	styled := NewGeneratedRef("styled_0", ArtifactKindImage)
	g.AddEdge(src, frame, "extract")
	g.AddEdge(frame, styled, "stylize")

	dot := g.ToDOT()
	if !strings.Contains(dot, "digraph lineage") {
		t.Fatalf("missing digraph header: %s", dot)
	}
	if !strings.Contains(dot, `[label="extract"]`) {
		t.Fatalf("missing extract edge label: %s", dot)
	}
	if !strings.Contains(dot, `[label="stylize"]`) {
		t.Fatalf("missing stylize edge label: %s", dot)
	}
	if !strings.Contains(dot, `"frame_0"`) {
		t.Fatalf("missing frame_0 node: %s", dot)
	}
}

func TestToDOTEdgeWithoutOperation(t *testing.T) {
	var g LineageGraph
	a := NewGeneratedRef("a", ArtifactKindText)
	b := NewGeneratedRef("b", ArtifactKindText)
	g.AddEdge(a, b, "")

	dot := g.ToDOT()
	if strings.Contains(dot, "label") {
		t.Fatalf("expected no label for empty operation, got: %s", dot)
	}
	if !strings.Contains(dot, `"a" -> "b"`) {
		t.Fatalf("expected edge a->b, got: %s", dot)
	}
}
