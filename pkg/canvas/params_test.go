package canvas

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestBuildParamsRejectsNilAndUnknown(t *testing.T) {
	t.Parallel()
	g := NewGraph(docFrom(nil, nil))
	if _, err := BuildParams(g, nil); err == nil {
		t.Fatal("expected error for nil node")
	}
	n := &Node{ID: "x", Data: map[string]any{"nodeType": "prompt"}}
	if _, err := BuildParams(g, n); err == nil {
		t.Fatal("expected error for non-executable node type")
	}
}

func TestIsExecutableNodeType(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		nt   string
		want bool
	}{
		{"imageGen", true},
		{"videoGen", true},
		{"voiceGen", true},
		{"textGen", true},
		{"prompt", false},
		{"reference", false},
		{"image", false},
		{"", false},
	} {
		if got := IsExecutableNodeType(tt.nt); got != tt.want {
			t.Errorf("IsExecutableNodeType(%q) = %v, want %v", tt.nt, got, tt.want)
		}
	}
}

func TestBuildImageGenParamsBasic(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "g", Data: map[string]any{
		"nodeType":       "imageGen",
		"prompt":         "  a cat  ",
		"size":           "1024x1024",
		"negativePrompt": "blurry",
		"aspectRatio":    "1:1",
		"resolution":     "1k",
		"cameraAngle":    "front",
		"engine":         "qwen-image",
	}}
	g := NewGraph(docFrom([]*Node{gen}, nil))

	res, err := BuildParams(g, gen)
	if err != nil {
		t.Fatalf("BuildParams: %v", err)
	}
	if res.ToolName != "generate_image" || res.UseLLM {
		t.Fatalf("unexpected dispatch: %+v", res)
	}
	want := map[string]any{
		"prompt":          "a cat",
		"size":            "1024x1024",
		"negative_prompt": "blurry",
		"aspect_ratio":    "1:1",
		"resolution":      "1k",
		"camera_angle":    "front",
		"engine":          "qwen-image",
	}
	if !reflect.DeepEqual(res.Params, want) {
		t.Fatalf("params mismatch:\ngot  %v\nwant %v", res.Params, want)
	}
}

func TestBuildImageGenParamsRequiresPrompt(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "g", Data: map[string]any{"nodeType": "imageGen", "prompt": "   "}}
	g := NewGraph(docFrom([]*Node{gen}, nil))
	if _, err := BuildParams(g, gen); err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestBuildImageGenParamsAttachesSingleImageRef(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "g", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}}
	img := &Node{ID: "img", Data: map[string]any{"mediaType": "image", "mediaUrl": "/a.png"}}
	doc := docFrom([]*Node{gen, img}, []*Edge{{Source: "img", Target: "g", Type: EdgeFlow}})
	g := NewGraph(doc)

	res, err := BuildParams(g, gen)
	if err != nil {
		t.Fatalf("BuildParams: %v", err)
	}
	if res.Params["reference_image"] != "/a.png" {
		t.Fatalf("reference_image: %v", res.Params["reference_image"])
	}
	if _, ok := res.Params["reference_images"]; ok {
		t.Fatal("reference_images should not be set when only one image")
	}
}

func TestBuildImageGenParamsCapsRefImages(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "g", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}}
	nodes := []*Node{gen}
	edges := []*Edge{}
	for i, url := range []string{"/a.png", "/b.png", "/c.png", "/d.png"} {
		id := []string{"img1", "img2", "img3", "img4"}[i]
		nodes = append(nodes, &Node{ID: id, Data: map[string]any{"mediaType": "image", "mediaUrl": url}})
		edges = append(edges, &Edge{Source: id, Target: "g", Type: EdgeFlow})
	}
	g := NewGraph(docFrom(nodes, edges))

	res, err := BuildParams(g, gen)
	if err != nil {
		t.Fatalf("BuildParams: %v", err)
	}
	urls, ok := res.Params["reference_images"].([]string)
	if !ok {
		t.Fatalf("reference_images type: %T", res.Params["reference_images"])
	}
	if len(urls) != MaxRefImages {
		t.Fatalf("want %d images, got %d", MaxRefImages, len(urls))
	}
}

func TestBuildImageGenParamsAttachesReferenceBundles(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "g", Data: map[string]any{"nodeType": "imageGen", "prompt": "x"}}
	ref := &Node{ID: "r", Data: map[string]any{
		"nodeType":    "reference",
		"refType":     "style",
		"refStrength": 0.7,
		"mediaUrl":    "/s.png",
		"mediaType":   "image",
	}}
	doc := docFrom([]*Node{gen, ref}, []*Edge{{Source: "r", Target: "g", Type: EdgeReference}})
	g := NewGraph(doc)

	res, err := BuildParams(g, gen)
	if err != nil {
		t.Fatalf("BuildParams: %v", err)
	}
	bundles, ok := res.Params["references"].([]map[string]any)
	if !ok {
		t.Fatalf("references type: %T", res.Params["references"])
	}
	if len(bundles) != 1 {
		t.Fatalf("want 1 bundle, got %d", len(bundles))
	}
	b := bundles[0]
	if b["type"] != "style" || b["url"] != "/s.png" || b["strength"] != 0.7 || b["media_type"] != "image" {
		t.Fatalf("bundle mismatch: %+v", b)
	}
}

func TestBuildVideoGenParamsBasic(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "g", Data: map[string]any{
		"nodeType":    "videoGen",
		"prompt":      "a cat dancing",
		"size":        "720x1280",
		"resolution":  "720p",
		"aspectRatio": "9:16",
		"duration":    float64(5),
		"engine":      "wan2.5",
	}}
	g := NewGraph(docFrom([]*Node{gen}, nil))

	res, err := BuildParams(g, gen)
	if err != nil {
		t.Fatalf("BuildParams: %v", err)
	}
	if res.ToolName != "generate_video" {
		t.Fatalf("toolName: %q", res.ToolName)
	}
	want := map[string]any{
		"prompt":       "a cat dancing",
		"size":         "720x1280",
		"resolution":   "720p",
		"aspect_ratio": "9:16",
		"duration":     float64(5),
		"engine":       "wan2.5",
	}
	if !reflect.DeepEqual(res.Params, want) {
		t.Fatalf("params mismatch:\ngot  %v\nwant %v", res.Params, want)
	}
}

func TestBuildVideoGenParamsAttachesVideoReference(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "g", Data: map[string]any{"nodeType": "videoGen", "prompt": "x"}}
	vid := &Node{ID: "v", Data: map[string]any{"mediaType": "video", "mediaUrl": "/clip.mp4"}}
	img := &Node{ID: "i", Data: map[string]any{"mediaType": "image", "mediaUrl": "/seed.png"}}
	doc := docFrom([]*Node{gen, vid, img}, []*Edge{
		{Source: "v", Target: "g", Type: EdgeFlow},
		{Source: "i", Target: "g", Type: EdgeFlow},
	})
	g := NewGraph(doc)

	res, err := BuildParams(g, gen)
	if err != nil {
		t.Fatalf("BuildParams: %v", err)
	}
	if res.Params["reference_video"] != "/clip.mp4" {
		t.Fatalf("reference_video: %v", res.Params["reference_video"])
	}
	if res.Params["reference_image"] != "/seed.png" {
		t.Fatalf("reference_image: %v", res.Params["reference_image"])
	}
}

func TestBuildVoiceGenParamsTextToSpeech(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "g", Data: map[string]any{
		"nodeType":     "voiceGen",
		"prompt":       "hello world",
		"voice":        "alloy",
		"language":     "en",
		"instructions": "speak slowly",
		"engine":       "openai-tts",
	}}
	g := NewGraph(docFrom([]*Node{gen}, nil))

	res, err := BuildParams(g, gen)
	if err != nil {
		t.Fatalf("BuildParams: %v", err)
	}
	if res.ToolName != "text_to_speech" {
		t.Fatalf("toolName: %q", res.ToolName)
	}
	if res.Params["text"] != "hello world" || res.Params["prompt"] != "hello world" {
		t.Fatalf("text/prompt: %+v", res.Params)
	}
	if res.Params["voice"] != "alloy" || res.Params["language"] != "en" {
		t.Fatalf("voice/language: %+v", res.Params)
	}
}

func TestBuildVoiceGenParamsMusicEngineDispatchesToGenerateMusic(t *testing.T) {
	t.Parallel()
	for _, engine := range []string{"suno-music", "ace-step-song", "Music-XL"} {
		gen := &Node{ID: "g", Data: map[string]any{
			"nodeType": "voiceGen",
			"prompt":   "a song",
			"engine":   engine,
		}}
		g := NewGraph(docFrom([]*Node{gen}, nil))
		res, err := BuildParams(g, gen)
		if err != nil {
			t.Fatalf("engine=%q: %v", engine, err)
		}
		if res.ToolName != "generate_music" {
			t.Fatalf("engine=%q: toolName=%q, want generate_music", engine, res.ToolName)
		}
	}
}

func TestBuildVoiceGenParamsAcceptsContentField(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "g", Data: map[string]any{
		"nodeType": "voiceGen",
		"content":  "fallback text",
	}}
	g := NewGraph(docFrom([]*Node{gen}, nil))
	res, err := BuildParams(g, gen)
	if err != nil {
		t.Fatalf("BuildParams: %v", err)
	}
	if res.Params["text"] != "fallback text" {
		t.Fatalf("text: %v", res.Params["text"])
	}
}

func TestBuildVoiceGenParamsRequiresText(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "g", Data: map[string]any{"nodeType": "voiceGen"}}
	g := NewGraph(docFrom([]*Node{gen}, nil))
	if _, err := BuildParams(g, gen); err == nil {
		t.Fatal("expected error when neither prompt nor content")
	}
}

func TestBuildTextGenParamsUsesLLMPath(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "g", Data: map[string]any{
		"nodeType": "textGen",
		"prompt":   "summarize the previous output",
	}}
	g := NewGraph(docFrom([]*Node{gen}, nil))
	res, err := BuildParams(g, gen)
	if err != nil {
		t.Fatalf("BuildParams: %v", err)
	}
	if !res.UseLLM {
		t.Fatal("expected UseLLM=true for textGen")
	}
	if res.ToolName != "" {
		t.Fatalf("textGen should not set ToolName, got %q", res.ToolName)
	}
	if res.Params["prompt"] != "summarize the previous output" {
		t.Fatalf("prompt: %v", res.Params["prompt"])
	}
}

func TestBuildTextGenParamsRequiresPrompt(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "g", Data: map[string]any{"nodeType": "textGen"}}
	g := NewGraph(docFrom([]*Node{gen}, nil))
	if _, err := BuildParams(g, gen); err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestBuildImageGenParamsOmitsEmptyOptionalFields(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "g", Data: map[string]any{
		"nodeType": "imageGen",
		"prompt":   "x",
	}}
	g := NewGraph(docFrom([]*Node{gen}, nil))
	res, err := BuildParams(g, gen)
	if err != nil {
		t.Fatalf("BuildParams: %v", err)
	}
	keys := make([]string, 0, len(res.Params))
	for k := range res.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"prompt"}) {
		t.Fatalf("expected only prompt key, got %v", keys)
	}
}

func TestBuildVideoGenParamsOmitsZeroDuration(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "g", Data: map[string]any{"nodeType": "videoGen", "prompt": "x"}}
	g := NewGraph(docFrom([]*Node{gen}, nil))
	res, err := BuildParams(g, gen)
	if err != nil {
		t.Fatalf("BuildParams: %v", err)
	}
	if _, ok := res.Params["duration"]; ok {
		t.Fatalf("duration should be omitted when zero, got %v", res.Params["duration"])
	}
}

func TestDataNumberAcceptsIntAndFloat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		val  any
		want float64
	}{
		{float64(3.5), 3.5},
		{int(7), 7},
		{int64(9), 9},
		{"not a number", 0},
		{nil, 0},
	}
	for _, tc := range cases {
		n := &Node{Data: map[string]any{"k": tc.val}}
		if got := dataNumber(n, "k"); got != tc.want {
			t.Errorf("dataNumber(%v) = %v, want %v", tc.val, got, tc.want)
		}
	}
	if got := dataNumber(nil, "k"); got != 0 {
		t.Errorf("nil node: got %v", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	t.Parallel()
	if got := firstNonEmpty("", "", "x", "y"); got != "x" {
		t.Errorf("got %q", got)
	}
	if got := firstNonEmpty("", "  ", ""); got != "  " {
		t.Errorf("non-blank-but-non-empty wins: got %q", got)
	}
	if got := firstNonEmpty(); got != "" {
		t.Errorf("empty: got %q", got)
	}
}

func TestBuildImageGenParamsErrorMentionsNodeID(t *testing.T) {
	t.Parallel()
	gen := &Node{ID: "node-42", Data: map[string]any{"nodeType": "imageGen"}}
	g := NewGraph(docFrom([]*Node{gen}, nil))
	_, err := BuildParams(g, gen)
	if err == nil || !strings.Contains(err.Error(), "node-42") {
		t.Fatalf("expected error mentioning node-42, got %v", err)
	}
}
