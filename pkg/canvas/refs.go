package canvas

// Ports of the upstream-edge resolution helpers in
// web/src/features/canvas/videoReferences.ts. Function names mirror the
// TypeScript exports so cross-checks against the frontend stay obvious.

// ReferenceBundle mirrors the TypeScript ReferenceBundle shape.
type ReferenceBundle struct {
	NodeID    string  `json:"nodeId"`
	RefType   string  `json:"refType"`
	Strength  float64 `json:"strength"`
	MediaURL  string  `json:"mediaUrl,omitempty"`
	MediaType string  `json:"mediaType,omitempty"`
}

// CollectLinkedImageNodes returns direct upstream nodes whose
// data.mediaType == "image". Mirrors collectLinkedImageNodes
// (videoReferences.ts:70-92).
func (g *Graph) CollectLinkedImageNodes(targetID string) []*Node {
	seen := map[string]bool{}
	var out []*Node
	for _, e := range g.incoming[targetID] {
		if seen[e.Source] {
			continue
		}
		src := g.Get(e.Source)
		if src == nil {
			continue
		}
		mt, _ := src.Data["mediaType"].(string)
		if mt != "image" {
			continue
		}
		seen[e.Source] = true
		out = append(out, src)
	}
	return out
}

// CollectVideoReferences returns image and video URLs from direct upstream
// nodes, splitting by data.mediaType. Mirrors collectVideoReferences
// (videoReferences.ts:38-68).
func (g *Graph) CollectVideoReferences(targetID string) (imageURLs, videoURLs []string) {
	seenImg := map[string]bool{}
	seenVid := map[string]bool{}
	for _, e := range g.incoming[targetID] {
		src := g.Get(e.Source)
		if src == nil || src.Data == nil {
			continue
		}
		mt, _ := src.Data["mediaType"].(string)
		url, _ := src.Data["mediaUrl"].(string)
		if url == "" {
			continue
		}
		switch mt {
		case "image":
			if !seenImg[url] {
				seenImg[url] = true
				imageURLs = append(imageURLs, url)
			}
		case "video":
			if !seenVid[url] {
				seenVid[url] = true
				videoURLs = append(videoURLs, url)
			}
		}
	}
	return imageURLs, videoURLs
}

// CollectReferenceBundles walks upstream `reference` nodes and returns their
// {refType, strength, mediaUrl, mediaType} bundles. If the reference node
// has no media of its own, walks one hop further upstream to find the
// attached media node — mirrors collectReferenceNodes
// (videoReferences.ts:97-136).
func (g *Graph) CollectReferenceBundles(targetID string) []ReferenceBundle {
	var out []ReferenceBundle
	for _, e := range g.incoming[targetID] {
		src := g.Get(e.Source)
		if src == nil || src.Data == nil {
			continue
		}
		nodeType, _ := src.Data["nodeType"].(string)
		if nodeType != "reference" {
			continue
		}

		mediaURL, _ := src.Data["mediaUrl"].(string)
		mediaType, _ := src.Data["mediaType"].(string)
		if mediaType == "" {
			mediaType = "image"
		}
		if mediaURL == "" {
			// One-hop upstream walk: find any incoming edge into the
			// reference node whose source has a mediaUrl.
			for _, up := range g.incoming[src.ID] {
				upNode := g.Get(up.Source)
				if upNode == nil || upNode.Data == nil {
					continue
				}
				if u, ok := upNode.Data["mediaUrl"].(string); ok && u != "" {
					mediaURL = u
					if t, ok := upNode.Data["mediaType"].(string); ok && t != "" {
						mediaType = t
					}
					break
				}
			}
		}

		refType, _ := src.Data["refType"].(string)
		if refType == "" {
			refType = "style"
		}
		strength := 1.0
		if v, ok := src.Data["refStrength"].(float64); ok {
			strength = v
		}

		out = append(out, ReferenceBundle{
			NodeID:    src.ID,
			RefType:   refType,
			Strength:  strength,
			MediaURL:  mediaURL,
			MediaType: mediaType,
		})
	}
	return out
}
