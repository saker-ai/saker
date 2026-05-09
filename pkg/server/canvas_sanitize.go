package server

import "strings"

// stripCanvasNodeDataURIs removes "data:" URIs from canvas node fields before
// the document is persisted to disk and later loaded back into the LLM context.
//
// Why: a sketch node in the eddaff17 thread carried a 26 KiB base64 data: URI
// in data.sourceUrl. Each subsequent canvas_get_node / canvas_list_nodes call
// then read that document and (before the data-URI scrub) bloated the model
// prompt enough to push it into the runaway-generation failure mode.
//
// We only drop fields that are *redundant* — sourceUrl/mediaUrl have a sibling
// mediaPath that points at the on-disk PNG, so the canonical reference
// survives. The frontend can re-render the inline preview from mediaUrl
// (which is the /api/files/canvas-media/* path, not a base64 blob).
//
// The input shape comes from JSON-RPC params: typically
//
//	[]interface{} of map[string]interface{}
//
// but we walk arbitrarily-nested maps/slices defensively in case the schema
// gains other data: carriers later.
func stripCanvasNodeDataURIs(raw any) any {
	return walkAndStripDataURIs(raw)
}

// canvasDataURIBearingFields enumerates the keys on a node's `data` map that
// are known to be safe to scrub — strings starting with "data:" carry inline
// base64 with no information beyond what the persisted PNG already holds.
//
// Keep this list narrow. Free-text fields (label, prompt, content) must NOT
// be scrubbed here because users may legitimately type "data:" as part of a
// natural-language sentence; the LLM-summary scrub in pkg/tool/builtin/canvas
// handles those separately.
var canvasDataURIBearingFields = map[string]struct{}{
	"sourceUrl": {},
	"mediaUrl":  {},
}

func walkAndStripDataURIs(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if _, drop := canvasDataURIBearingFields[k]; drop {
				if s, ok := val.(string); ok && strings.HasPrefix(s, "data:") {
					x[k] = ""
					continue
				}
			}
			x[k] = walkAndStripDataURIs(val)
		}
		return x
	case []any:
		for i, item := range x {
			x[i] = walkAndStripDataURIs(item)
		}
		return x
	default:
		return v
	}
}
