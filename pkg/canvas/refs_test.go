package canvas

import (
	"reflect"
	"sort"
	"testing"
)

func TestCollectLinkedImageNodes(t *testing.T) {
	t.Parallel()
	doc := docFrom(
		[]*Node{
			{ID: "img1", Data: map[string]any{"mediaType": "image", "mediaUrl": "/a.png"}},
			{ID: "img2", Data: map[string]any{"mediaType": "image", "mediaUrl": "/b.png"}},
			{ID: "vid1", Data: map[string]any{"mediaType": "video", "mediaUrl": "/v.mp4"}},
			{ID: "gen", Data: map[string]any{"nodeType": "imageGen"}},
		},
		[]*Edge{
			{Source: "img1", Target: "gen", Type: EdgeFlow},
			{Source: "img2", Target: "gen", Type: EdgeReference},
			{Source: "vid1", Target: "gen", Type: EdgeFlow}, // wrong type, should not appear
			{Source: "img1", Target: "gen", Type: EdgeFlow}, // duplicate, dedup by source
		},
	)
	got := NewGraph(doc).CollectLinkedImageNodes("gen")
	if len(got) != 2 {
		t.Fatalf("want 2 image nodes, got %d", len(got))
	}
	ids := []string{got[0].ID, got[1].ID}
	sort.Strings(ids)
	if !reflect.DeepEqual(ids, []string{"img1", "img2"}) {
		t.Fatalf("got %v", ids)
	}
}

func TestCollectVideoReferences(t *testing.T) {
	t.Parallel()
	doc := docFrom(
		[]*Node{
			{ID: "img", Data: map[string]any{"mediaType": "image", "mediaUrl": "/a.png"}},
			{ID: "vid1", Data: map[string]any{"mediaType": "video", "mediaUrl": "/v1.mp4"}},
			{ID: "vid2", Data: map[string]any{"mediaType": "video", "mediaUrl": "/v2.mp4"}},
			{ID: "gen", Data: map[string]any{"nodeType": "videoGen"}},
		},
		[]*Edge{
			{Source: "img", Target: "gen", Type: EdgeFlow},
			{Source: "vid1", Target: "gen", Type: EdgeFlow},
			{Source: "vid2", Target: "gen", Type: EdgeFlow},
		},
	)
	imgs, vids := NewGraph(doc).CollectVideoReferences("gen")
	if len(imgs) != 1 || imgs[0] != "/a.png" {
		t.Fatalf("imgs: %v", imgs)
	}
	if len(vids) != 2 {
		t.Fatalf("vids: %v", vids)
	}
}

func TestCollectReferenceBundlesDirectMedia(t *testing.T) {
	t.Parallel()
	doc := docFrom(
		[]*Node{
			{ID: "ref", Data: map[string]any{
				"nodeType":    "reference",
				"refType":     "pose",
				"refStrength": 0.6,
				"mediaUrl":    "/p.png",
				"mediaType":   "image",
			}},
			{ID: "gen", Data: map[string]any{"nodeType": "imageGen"}},
		},
		[]*Edge{{Source: "ref", Target: "gen", Type: EdgeReference}},
	)
	got := NewGraph(doc).CollectReferenceBundles("gen")
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	want := ReferenceBundle{NodeID: "ref", RefType: "pose", Strength: 0.6, MediaURL: "/p.png", MediaType: "image"}
	if got[0] != want {
		t.Fatalf("got %+v want %+v", got[0], want)
	}
}

func TestCollectReferenceBundlesOneHopWalk(t *testing.T) {
	t.Parallel()
	// A reference node with no media of its own should pull media from its
	// upstream image node.
	doc := docFrom(
		[]*Node{
			{ID: "img", Data: map[string]any{"mediaType": "image", "mediaUrl": "/u.png"}},
			{ID: "ref", Data: map[string]any{"nodeType": "reference", "refType": "style"}},
			{ID: "gen", Data: map[string]any{"nodeType": "imageGen"}},
		},
		[]*Edge{
			{Source: "img", Target: "ref", Type: EdgeFlow},
			{Source: "ref", Target: "gen", Type: EdgeReference},
		},
	)
	got := NewGraph(doc).CollectReferenceBundles("gen")
	if len(got) != 1 || got[0].MediaURL != "/u.png" {
		t.Fatalf("upstream walk failed: %+v", got)
	}
	if got[0].Strength != 1.0 {
		t.Fatalf("default strength should be 1.0, got %v", got[0].Strength)
	}
	if got[0].RefType != "style" {
		t.Fatalf("refType: %v", got[0].RefType)
	}
}

func TestCollectReferenceBundlesIgnoresNonReferenceNodes(t *testing.T) {
	t.Parallel()
	doc := docFrom(
		[]*Node{
			{ID: "img", Data: map[string]any{"mediaType": "image", "mediaUrl": "/a.png"}},
			{ID: "gen", Data: map[string]any{"nodeType": "imageGen"}},
		},
		[]*Edge{{Source: "img", Target: "gen", Type: EdgeReference}},
	)
	got := NewGraph(doc).CollectReferenceBundles("gen")
	if len(got) != 0 {
		t.Fatalf("plain image upstream is not a reference bundle: %+v", got)
	}
}
