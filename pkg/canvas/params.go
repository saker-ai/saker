package canvas

import (
	"fmt"
	"strings"
)

// MaxRefImages mirrors the frontend MAX_REF_IMAGES constant
// (ImageGenNode.tsx:23, VideoGenNode.tsx:24). Generators reject extras.
const MaxRefImages = 3

// MaxGenHistory mirrors useGenerate.ts MAX_GEN_HISTORY (cap at 20 entries).
const MaxGenHistory = 20

// BuildResult tells the executor which tool to dispatch and what params to
// pass. ToolName is empty for nodes the executor handles natively (textGen
// dispatches to runtime.Run instead of ExecuteTool).
type BuildResult struct {
	ToolName string
	Params   map[string]any
	UseLLM   bool // true → executor calls runtime.Run instead of ExecuteTool
}

// BuildParams returns the tool dispatch shape for a single gen node, or an
// error if the node type is not executable. Mirrors the per-node buildParams
// callbacks in web/src/features/canvas/nodes/*GenNode.tsx so prod aigo calls
// see byte-for-byte equivalent params.
func BuildParams(g *Graph, node *Node) (*BuildResult, error) {
	if node == nil {
		return nil, fmt.Errorf("canvas: BuildParams: nil node")
	}
	switch node.NodeType() {
	case "imageGen":
		return buildImageGenParams(g, node)
	case "videoGen":
		return buildVideoGenParams(g, node)
	case "voiceGen":
		return buildVoiceGenParams(g, node)
	case "textGen":
		return buildTextGenParams(node)
	default:
		return nil, fmt.Errorf("canvas: node %s has non-executable type %q", node.ID, node.NodeType())
	}
}

// IsExecutableNodeType reports whether the given nodeType produces a tool
// dispatch. Used by the executor to skip scaffolding nodes (prompt, image,
// reference, …) without raising an error.
func IsExecutableNodeType(nt string) bool {
	switch nt {
	case "imageGen", "videoGen", "voiceGen", "textGen":
		return true
	default:
		return false
	}
}

// buildImageGenParams ports ImageGenNode.tsx:66-97.
func buildImageGenParams(g *Graph, node *Node) (*BuildResult, error) {
	prompt := strings.TrimSpace(node.DataString("prompt"))
	if prompt == "" {
		return nil, fmt.Errorf("canvas: imageGen %s: prompt is empty", node.ID)
	}
	params := map[string]any{
		"prompt": prompt,
	}
	if v := node.DataString("size"); v != "" {
		params["size"] = v
	}
	if v := strings.TrimSpace(node.DataString("negativePrompt")); v != "" {
		params["negative_prompt"] = v
	}
	if v := node.DataString("aspectRatio"); v != "" {
		params["aspect_ratio"] = v
	}
	if v := node.DataString("resolution"); v != "" {
		params["resolution"] = v
	}
	if v := node.DataString("cameraAngle"); v != "" {
		params["camera_angle"] = v
	}
	if v := node.DataString("engine"); v != "" {
		params["engine"] = v
	}

	attachUpstreamImageRefs(g, node.ID, params)
	attachReferenceBundles(g, node.ID, params)

	return &BuildResult{ToolName: "generate_image", Params: params}, nil
}

// buildVideoGenParams ports VideoGenNode.tsx:68-105.
func buildVideoGenParams(g *Graph, node *Node) (*BuildResult, error) {
	prompt := strings.TrimSpace(node.DataString("prompt"))
	if prompt == "" {
		return nil, fmt.Errorf("canvas: videoGen %s: prompt is empty", node.ID)
	}
	params := map[string]any{
		"prompt": prompt,
	}
	if v := node.DataString("size"); v != "" {
		params["size"] = v
	}
	if v := node.DataString("resolution"); v != "" {
		params["resolution"] = v
	}
	if v := node.DataString("aspectRatio"); v != "" {
		params["aspect_ratio"] = v
	}
	if v := dataNumber(node, "duration"); v != 0 {
		params["duration"] = v
	}
	if v := node.DataString("engine"); v != "" {
		params["engine"] = v
	}

	attachUpstreamImageRefs(g, node.ID, params)

	if _, vids := g.CollectVideoReferences(node.ID); len(vids) > 0 {
		params["reference_video"] = vids[0]
	}
	attachReferenceBundles(g, node.ID, params)

	return &BuildResult{ToolName: "generate_video", Params: params}, nil
}

// buildVoiceGenParams ports VoiceGenNode behaviour: dispatches to
// generate_music when the engine signals music generation, otherwise to
// text_to_speech. The params shape is shared.
func buildVoiceGenParams(_ *Graph, node *Node) (*BuildResult, error) {
	text := strings.TrimSpace(firstNonEmpty(node.DataString("prompt"), node.DataString("content")))
	if text == "" {
		return nil, fmt.Errorf("canvas: voiceGen %s: prompt/content is empty", node.ID)
	}
	params := map[string]any{
		"text":   text,
		"prompt": text,
	}
	if v := node.DataString("voice"); v != "" {
		params["voice"] = v
	}
	if v := node.DataString("language"); v != "" {
		params["language"] = v
	}
	if v := node.DataString("instructions"); v != "" {
		params["instructions"] = v
	}
	engine := node.DataString("engine")
	if engine != "" {
		params["engine"] = engine
	}

	tool := "text_to_speech"
	// Music engines are conventionally named with "music" or "song" — match
	// the same heuristic the frontend uses in VoiceGenNode.
	lower := strings.ToLower(engine)
	if strings.Contains(lower, "music") || strings.Contains(lower, "song") {
		tool = "generate_music"
	}
	return &BuildResult{ToolName: tool, Params: params}, nil
}

// buildTextGenParams routes textGen to the LLM via runtime.Run. The tool
// dispatch path is unused; the executor checks UseLLM.
func buildTextGenParams(node *Node) (*BuildResult, error) {
	prompt := strings.TrimSpace(node.DataString("prompt"))
	if prompt == "" {
		return nil, fmt.Errorf("canvas: textGen %s: prompt is empty", node.ID)
	}
	return &BuildResult{
		ToolName: "",
		UseLLM:   true,
		Params:   map[string]any{"prompt": prompt},
	}, nil
}

// attachUpstreamImageRefs writes reference_image / reference_images keys on
// params from upstream image nodes. Caps at MaxRefImages.
func attachUpstreamImageRefs(g *Graph, nodeID string, params map[string]any) {
	imgs := g.CollectLinkedImageNodes(nodeID)
	urls := make([]string, 0, len(imgs))
	for _, n := range imgs {
		if u, ok := n.Data["mediaUrl"].(string); ok && u != "" {
			urls = append(urls, u)
		}
	}
	if len(urls) > MaxRefImages {
		urls = urls[:MaxRefImages]
	}
	switch len(urls) {
	case 0:
		// nothing to attach
	case 1:
		params["reference_image"] = urls[0]
	default:
		params["reference_images"] = urls
	}
}

// attachReferenceBundles writes the typed `references` array used by aigo
// for {style, character, composition, pose} bundles.
func attachReferenceBundles(g *Graph, nodeID string, params map[string]any) {
	bundles := g.CollectReferenceBundles(nodeID)
	if len(bundles) == 0 {
		return
	}
	out := make([]map[string]any, 0, len(bundles))
	for _, b := range bundles {
		if b.MediaURL == "" {
			continue
		}
		out = append(out, map[string]any{
			"type":       b.RefType,
			"strength":   b.Strength,
			"url":        b.MediaURL,
			"media_type": b.MediaType,
		})
	}
	if len(out) > 0 {
		params["references"] = out
	}
}

func dataNumber(n *Node, key string) float64 {
	if n == nil || n.Data == nil {
		return 0
	}
	switch v := n.Data[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
