package apps

import (
	"fmt"
	"strings"

	"github.com/cinience/saker/pkg/canvas"
)

// ExtractInputs walks doc.Nodes and returns one AppInputField for every
// node with NodeType()=="appInput" that has a non-empty appVariable.
// Nodes missing the variable name are skipped silently — they are
// half-configured scaffolding, not validation errors.
func ExtractInputs(doc *canvas.Document) []AppInputField {
	if doc == nil {
		return nil
	}
	out := make([]AppInputField, 0, len(doc.Nodes))
	for _, n := range doc.Nodes {
		if n == nil || n.NodeType() != "appInput" {
			continue
		}
		variable := strings.TrimSpace(n.DataString("appVariable"))
		if variable == "" {
			continue
		}
		field := AppInputField{
			NodeID:   n.ID,
			Variable: variable,
			Label:    n.DataString("label"),
			Type:     n.DataString("appFieldType"),
			Required: dataBool(n, "appRequired"),
		}
		if field.Type == "" {
			field.Type = "text"
		}
		if v, ok := n.Data["appDefault"]; ok && v != nil {
			field.Default = v
		}
		if opts := dataStringSlice(n, "appOptions"); len(opts) > 0 {
			field.Options = opts
		}
		if v, ok := dataFloatPtr(n, "appMin"); ok {
			field.Min = v
		}
		if v, ok := dataFloatPtr(n, "appMax"); ok {
			field.Max = v
		}
		out = append(out, field)
	}
	return out
}

// ExtractOutputs walks doc.Nodes and returns one AppOutputField for every
// node with NodeType()=="appOutput". SourceRef is the ID of the first
// upstream edge.Source feeding the appOutput node; Kind defaults to the
// upstream node's media type (imageGen→image, videoGen→video, voiceGen→
// audio, anything else→text) when the node has no explicit appOutputKind.
func ExtractOutputs(doc *canvas.Document) []AppOutputField {
	if doc == nil {
		return nil
	}
	out := make([]AppOutputField, 0, len(doc.Nodes))
	for _, n := range doc.Nodes {
		if n == nil || n.NodeType() != "appOutput" {
			continue
		}
		field := AppOutputField{
			NodeID: n.ID,
			Label:  n.DataString("label"),
		}
		// Find the first upstream source feeding this output.
		for _, e := range doc.Edges {
			if e == nil || e.Target != n.ID {
				continue
			}
			field.SourceRef = e.Source
			break
		}
		field.Kind = strings.TrimSpace(n.DataString("appOutputKind"))
		if field.Kind == "" {
			field.Kind = inferKind(doc, field.SourceRef)
		}
		out = append(out, field)
	}
	return out
}

// ValidateInputs checks supplied inputs against the schema. Required
// fields must be present and non-empty; select fields must be one of
// the listed options; number fields must be coercible to float64. All
// failures are aggregated into a single error so the caller can show
// every problem at once.
func ValidateInputs(fields []AppInputField, inputs map[string]any) error {
	var problems []string
	for _, f := range fields {
		raw, present := inputs[f.Variable]
		if f.Required && (!present || isEmpty(raw)) {
			problems = append(problems, fmt.Sprintf("%s is required", f.Variable))
			continue
		}
		if !present {
			continue
		}
		switch f.Type {
		case "select":
			if len(f.Options) == 0 {
				continue
			}
			s := fmt.Sprintf("%v", raw)
			matched := false
			for _, opt := range f.Options {
				if s == opt {
					matched = true
					break
				}
			}
			if !matched {
				problems = append(problems,
					fmt.Sprintf("%s must be one of %s", f.Variable, strings.Join(f.Options, ", ")))
			}
		case "number":
			if !isCoercibleToFloat(raw) {
				problems = append(problems, fmt.Sprintf("%s must be a number", f.Variable))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("app inputs invalid: %s", strings.Join(problems, "; "))
}

// inferKind returns a sensible default Kind for an output based on the
// upstream node's NodeType.
func inferKind(doc *canvas.Document, sourceID string) string {
	if sourceID == "" {
		return "text"
	}
	src := doc.FindNode(sourceID)
	if src == nil {
		return "text"
	}
	switch src.NodeType() {
	case "imageGen", "image":
		return "image"
	case "videoGen", "video":
		return "video"
	case "voiceGen", "audio":
		return "audio"
	default:
		return "text"
	}
}

func dataBool(n *canvas.Node, key string) bool {
	if n == nil || n.Data == nil {
		return false
	}
	switch v := n.Data[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	default:
		return false
	}
}

func dataStringSlice(n *canvas.Node, key string) []string {
	if n == nil || n.Data == nil {
		return nil
	}
	raw, ok := n.Data[key].([]any)
	if !ok {
		// Tolerate already-typed slices in tests / hand-built docs.
		if typed, ok2 := n.Data[key].([]string); ok2 {
			return append([]string(nil), typed...)
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			continue
		}
		out = append(out, s)
	}
	return out
}

func dataFloatPtr(n *canvas.Node, key string) (*float64, bool) {
	if n == nil || n.Data == nil {
		return nil, false
	}
	v, ok := n.Data[key]
	if !ok || v == nil {
		return nil, false
	}
	switch x := v.(type) {
	case float64:
		return &x, true
	case int:
		f := float64(x)
		return &f, true
	case int64:
		f := float64(x)
		return &f, true
	default:
		return nil, false
	}
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case []string:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	default:
		return false
	}
}

func isCoercibleToFloat(v any) bool {
	switch v.(type) {
	case float64, float32, int, int64, int32:
		return true
	case string:
		// Accept numeric strings ("42", "3.14"). fmt.Sscanf is enough here.
		var f float64
		s := strings.TrimSpace(v.(string))
		if s == "" {
			return false
		}
		_, err := fmt.Sscanf(s, "%f", &f)
		return err == nil
	default:
		return false
	}
}
