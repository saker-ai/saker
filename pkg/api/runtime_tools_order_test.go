package api

import "testing"

// TestBuiltinOrderIncludesCanvasTools is a regression guard for the issue where
// canvas_table_write was registered in factories but missing from builtinOrder,
// so filterBuiltinNames silently dropped it and the model never saw the tool.
//
// All canvas-facing tools that the chat handler injects thread context into MUST
// be in builtinOrder for at least one entrypoint that the server actually uses,
// otherwise users will see the model fall back to bash heredoc / file_write.
func TestBuiltinOrderIncludesCanvasTools(t *testing.T) {
	t.Parallel()
	required := []string{"canvas_get_node", "canvas_list_nodes", "canvas_table_write"}
	// Try every entrypoint; the canvas tools should appear under each one we
	// expose to chat sessions.
	entries := []EntryPoint{EntryPointCLI, EntryPointCI, EntryPointPlatform}
	for _, entry := range entries {
		order := builtinOrder(entry)
		set := make(map[string]struct{}, len(order))
		for _, name := range order {
			set[name] = struct{}{}
		}
		for _, want := range required {
			if _, ok := set[want]; !ok {
				t.Errorf("builtinOrder(%v) missing %q — model will not see this tool", entry, want)
			}
		}
	}
}
