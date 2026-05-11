package config

// This file contains pure helpers that merge or clone collection types used
// across the various Settings sub-blocks: string slices, string/int maps, and
// MCP-server rule lists.

// mergeMaps merges string maps; higher values override lower keys.
func mergeMaps(lower, higher map[string]string) map[string]string {
	if len(lower) == 0 && len(higher) == 0 {
		return nil
	}
	out := make(map[string]string, len(lower)+len(higher))
	for k, v := range lower {
		out[k] = v
	}
	for k, v := range higher {
		out[k] = v
	}
	return out
}

func mergeIntMap(lower, higher map[string]int) map[string]int {
	if len(lower) == 0 && len(higher) == 0 {
		return nil
	}
	out := make(map[string]int, len(lower)+len(higher))
	for k, v := range lower {
		out[k] = v
	}
	for k, v := range higher {
		out[k] = v
	}
	return out
}

// mergeStringSlices appends slices and removes duplicates while preserving order.
func mergeStringSlices(lower, higher []string) []string {
	if len(lower) == 0 && len(higher) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(lower)+len(higher))
	out := make([]string, 0, len(lower)+len(higher))
	for _, v := range lower {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, v := range higher {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func mergeMCPServerRules(lower, higher []MCPServerRule) []MCPServerRule {
	if len(higher) > 0 {
		return append([]MCPServerRule(nil), higher...)
	}
	if len(lower) == 0 {
		return nil
	}
	return append([]MCPServerRule(nil), lower...)
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	return boolPtr(*v)
}
