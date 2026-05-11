package toolbuiltin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// canvas_table_write_helpers.go owns the validation/normalisation helpers
// that translate raw tool params into canonical canvas table data, plus the
// atomic on-disk writer. Mutation logic lives in canvas_table_write_apply.go
// and the public tool surface in canvas_table_write_schema.go.

// normaliseColumns validates and canonicalises the columns array. Each column
// gets a stable id (auto-generated when omitted), and unknown column types
// fall back to "text". Returns (columns, idSet, error).
func normaliseColumns(colsRaw []any) ([]any, map[string]struct{}, error) {
	columns := make([]any, 0, len(colsRaw))
	ids := map[string]struct{}{}
	for i, raw := range colsRaw {
		c, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("columns[%d] must be an object", i)
		}
		name, _ := c["name"].(string)
		if strings.TrimSpace(name) == "" {
			return nil, nil, fmt.Errorf("columns[%d].name is required", i)
		}
		id, _ := c["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			id = fmt.Sprintf("col_%d", i+1)
		} else if !canvasIDPattern.MatchString(id) {
			return nil, nil, fmt.Errorf("columns[%d].id %q must match %s", i, id, canvasIDPattern.String())
		}
		if _, dup := ids[id]; dup {
			return nil, nil, fmt.Errorf("columns[%d].id %q is duplicated", i, id)
		}
		ids[id] = struct{}{}
		colType, _ := c["type"].(string)
		switch colType {
		case "text", "longText", "number", "select":
			// ok
		case "":
			colType = "text"
		default:
			colType = "text"
		}
		out := map[string]any{
			"id":   id,
			"name": clampString(name),
			"type": colType,
		}
		if opts, ok := c["options"].([]any); ok && len(opts) > 0 {
			out["options"] = opts
		}
		columns = append(columns, out)
	}
	return columns, ids, nil
}

// normaliseRows produces canonical row maps. Unknown columns are dropped so
// the resulting rows stay aligned with the (just-normalised) column set.
func normaliseRows(rowsRaw []any, colIDs map[string]struct{}) ([]any, error) {
	rows := make([]any, 0, len(rowsRaw))
	seenIDs := map[string]struct{}{}
	for i, raw := range rowsRaw {
		r, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("rows[%d] must be an object", i)
		}
		id, _ := r["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			id = fmt.Sprintf("row_%d", i+1)
		} else if !canvasIDPattern.MatchString(id) {
			return nil, fmt.Errorf("rows[%d].id %q must match %s", i, id, canvasIDPattern.String())
		}
		if _, dup := seenIDs[id]; dup {
			return nil, fmt.Errorf("rows[%d].id %q is duplicated", i, id)
		}
		seenIDs[id] = struct{}{}

		out := map[string]any{"id": id}
		for k, v := range r {
			if k == "id" {
				continue
			}
			if _, ok := colIDs[k]; !ok {
				continue
			}
			out[k] = clampCellValue(v)
		}
		rows = append(rows, out)
	}
	return rows, nil
}

// tableColumnIDSet snapshots the current column ids so cell writes can refuse
// to scribble on columns that don't exist.
func tableColumnIDSet(data map[string]any) map[string]struct{} {
	cols, _ := data["tableColumns"].([]any)
	out := map[string]struct{}{}
	for _, raw := range cols {
		if c, ok := raw.(map[string]any); ok {
			if id, _ := c["id"].(string); id != "" {
				out[id] = struct{}{}
			}
		}
	}
	return out
}

// nextRowID returns the smallest row_<n> id not yet present in rows. Linear
// scan is fine at canvasTableMaxRows.
func nextRowID(rows []any) string {
	used := map[string]struct{}{}
	for _, raw := range rows {
		if r, ok := raw.(map[string]any); ok {
			if id, _ := r["id"].(string); id != "" {
				used[id] = struct{}{}
			}
		}
	}
	for i := 1; i <= canvasTableMaxRows+1; i++ {
		candidate := fmt.Sprintf("row_%d", i)
		if _, taken := used[candidate]; !taken {
			return candidate
		}
	}
	return fmt.Sprintf("row_%d", len(rows)+1)
}

func rowExists(rows []any, id string) bool {
	for _, raw := range rows {
		if r, ok := raw.(map[string]any); ok {
			if existing, _ := r["id"].(string); existing == id {
				return true
			}
		}
	}
	return false
}

// clampCellValue normalises a cell value: strings get trimmed and capped;
// numbers pass through; everything else is stringified and capped (so a
// stray bool / nested object can't smuggle in unbounded data).
func clampCellValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return clampString(t)
	case float64, float32, int, int32, int64:
		return v
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return clampString(string(raw))
	}
}

func clampString(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= canvasTableMaxCellChars {
		return s
	}
	return s[:canvasTableMaxCellChars] + "…"
}

func ifEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// atomicWriteCanvasJSON writes the document via tmp+rename so a crash mid
// write cannot corrupt the canvas. Indentation matches handleCanvasSave so
// diffs against browser-saved files stay readable.
func atomicWriteCanvasJSON(path string, doc map[string]any) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
