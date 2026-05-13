package api

import "testing"

// TestBuiltinOrderIncludesCanvasTools verifies canvas tools are present in
// server presets (server_web, server_api) where chat sessions use them.
// CLI and CI presets intentionally exclude canvas tools.
func TestBuiltinOrderIncludesCanvasTools(t *testing.T) {
	t.Parallel()
	required := []string{"canvas_get_node", "canvas_list_nodes", "canvas_table_write"}

	// Canvas tools must be in Platform (server_web) preset.
	order := builtinOrder(EntryPointPlatform, "")
	set := make(map[string]struct{}, len(order))
	for _, name := range order {
		set[name] = struct{}{}
	}
	for _, want := range required {
		if _, ok := set[want]; !ok {
			t.Errorf("builtinOrder(Platform) missing %q — model will not see this tool", want)
		}
	}

	// Canvas tools must NOT be in CLI preset.
	cliOrder := builtinOrder(EntryPointCLI, "")
	cliSet := make(map[string]struct{}, len(cliOrder))
	for _, name := range cliOrder {
		cliSet[name] = struct{}{}
	}
	for _, want := range required {
		if _, ok := cliSet[want]; ok {
			t.Errorf("builtinOrder(CLI) should not include %q", want)
		}
	}
}
