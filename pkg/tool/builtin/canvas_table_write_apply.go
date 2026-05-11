package toolbuiltin

import (
	"errors"
	"fmt"
	"strings"
)

// canvas_table_write_apply.go owns the per-operation mutations
// (replace/set_cell/add_row/delete_row). Validation and persistence helpers
// live in canvas_table_write_helpers.go; the public tool surface lives in
// canvas_table_write_schema.go.

// applyTableOperation mutates data in place per the requested operation.
// Returns a short human-readable summary (used in the tool Output) or an
// error if the operation arguments are malformed.
func applyTableOperation(data map[string]any, operation string, params map[string]interface{}) (string, error) {
	switch operation {
	case "replace":
		return applyTableReplace(data, params)
	case "set_cell":
		return applyTableSetCell(data, params)
	case "add_row":
		return applyTableAddRow(data, params)
	case "delete_row":
		return applyTableDeleteRow(data, params)
	default:
		return "", fmt.Errorf("unknown operation %q (want replace|set_cell|add_row|delete_row)", operation)
	}
}

func applyTableReplace(data map[string]any, params map[string]interface{}) (string, error) {
	colsRaw, _ := params["columns"].([]any)
	rowsRaw, _ := params["rows"].([]any)
	if len(colsRaw) == 0 {
		return "", errors.New("replace: columns must be a non-empty array")
	}
	if len(rowsRaw) > canvasTableMaxRows {
		return "", fmt.Errorf("replace: row count %d exceeds cap %d", len(rowsRaw), canvasTableMaxRows)
	}

	columns, colIDs, err := normaliseColumns(colsRaw)
	if err != nil {
		return "", err
	}
	rows, err := normaliseRows(rowsRaw, colIDs)
	if err != nil {
		return "", err
	}

	data["tableColumns"] = columns
	data["tableRows"] = rows
	return fmt.Sprintf("replaced table: %d columns, %d rows.", len(columns), len(rows)), nil
}

func applyTableSetCell(data map[string]any, params map[string]interface{}) (string, error) {
	rowID, _ := params["row_id"].(string)
	colID, _ := params["column_id"].(string)
	rowID = strings.TrimSpace(rowID)
	colID = strings.TrimSpace(colID)
	if rowID == "" || colID == "" {
		return "", errors.New("set_cell: row_id and column_id are required")
	}
	if !canvasIDPattern.MatchString(rowID) || !canvasIDPattern.MatchString(colID) {
		return "", fmt.Errorf("set_cell: row_id/column_id must match %s", canvasIDPattern.String())
	}

	colIDs := tableColumnIDSet(data)
	if _, ok := colIDs[colID]; !ok {
		return "", fmt.Errorf("set_cell: column %q does not exist (call replace to define columns first)", colID)
	}

	rows, _ := data["tableRows"].([]any)
	for i, raw := range rows {
		r, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := r["id"].(string); id == rowID {
			r[colID] = clampCellValue(params["value"])
			rows[i] = r
			data["tableRows"] = rows
			return fmt.Sprintf("set_cell: row %s column %s updated.", rowID, colID), nil
		}
	}
	return "", fmt.Errorf("set_cell: row %q not found", rowID)
}

func applyTableAddRow(data map[string]any, params map[string]interface{}) (string, error) {
	rows, _ := data["tableRows"].([]any)
	if len(rows)+1 > canvasTableMaxRows {
		return "", fmt.Errorf("add_row: would exceed row cap %d", canvasTableMaxRows)
	}

	colIDs := tableColumnIDSet(data)
	values, _ := params["values"].(map[string]any)
	rowID, _ := params["row_id"].(string)
	rowID = strings.TrimSpace(rowID)
	if rowID == "" {
		rowID = nextRowID(rows)
	} else if !canvasIDPattern.MatchString(rowID) {
		return "", fmt.Errorf("add_row: row_id must match %s", canvasIDPattern.String())
	} else if rowExists(rows, rowID) {
		return "", fmt.Errorf("add_row: row %q already exists", rowID)
	}

	row := map[string]any{"id": rowID}
	for k, v := range values {
		if _, ok := colIDs[k]; !ok {
			continue
		}
		row[k] = clampCellValue(v)
	}

	position, _ := params["position"].(string)
	position = strings.TrimSpace(position)
	switch {
	case position == "" || position == "bottom":
		rows = append(rows, row)
	case position == "top":
		rows = append([]any{row}, rows...)
	case strings.HasPrefix(position, "after:"):
		anchor := strings.TrimPrefix(position, "after:")
		idx := -1
		for i, raw := range rows {
			if r, ok := raw.(map[string]any); ok {
				if id, _ := r["id"].(string); id == anchor {
					idx = i
					break
				}
			}
		}
		if idx < 0 {
			return "", fmt.Errorf("add_row: anchor row %q (from position) not found", anchor)
		}
		rows = append(rows[:idx+1], append([]any{row}, rows[idx+1:]...)...)
	default:
		return "", fmt.Errorf("add_row: invalid position %q (want top|bottom|after:<row_id>)", position)
	}

	data["tableRows"] = rows
	return fmt.Sprintf("add_row: inserted %s (%s).", rowID, ifEmpty(position, "bottom")), nil
}

func applyTableDeleteRow(data map[string]any, params map[string]interface{}) (string, error) {
	rowID, _ := params["row_id"].(string)
	rowID = strings.TrimSpace(rowID)
	if rowID == "" {
		return "", errors.New("delete_row: row_id is required")
	}
	rows, _ := data["tableRows"].([]any)
	for i, raw := range rows {
		if r, ok := raw.(map[string]any); ok {
			if id, _ := r["id"].(string); id == rowID {
				rows = append(rows[:i], rows[i+1:]...)
				data["tableRows"] = rows
				return fmt.Sprintf("delete_row: removed %s.", rowID), nil
			}
		}
	}
	return "", fmt.Errorf("delete_row: row %q not found", rowID)
}
