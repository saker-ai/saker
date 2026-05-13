package api

// ToolGroup represents a named category of built-in tools.
type ToolGroup string

const (
	GroupCoreIO      ToolGroup = "core_io"
	GroupBashMgmt    ToolGroup = "bash_mgmt"
	GroupTaskMgmt    ToolGroup = "task_mgmt"
	GroupWeb         ToolGroup = "web"
	GroupMedia       ToolGroup = "media"
	GroupInteraction ToolGroup = "interaction"
	GroupCanvas      ToolGroup = "canvas"
	GroupBrowser     ToolGroup = "browser"
)

var groupTools = map[ToolGroup][]string{
	GroupCoreIO:      {"bash", "file_read", "file_write", "file_edit", "grep", "glob"},
	GroupBashMgmt:    {"bash_output", "bash_status", "kill_task"},
	GroupTaskMgmt:    {"task", "task_create", "task_list", "task_get", "task_update"},
	GroupWeb:         {"web_fetch", "web_search"},
	GroupMedia:       {"image_read", "video_sampler", "stream_capture", "stream_monitor", "frame_analyzer", "video_summarizer", "analyze_video", "media_index", "media_search"},
	GroupInteraction: {"ask_user_question", "skill", "slash_command"},
	GroupCanvas:      {"canvas_get_node", "canvas_list_nodes", "canvas_table_write"},
	GroupBrowser:     {"browser", "webhook"},
}

// ToolsInGroup returns the factory keys belonging to the given group.
func ToolsInGroup(g ToolGroup) []string {
	return append([]string(nil), groupTools[g]...)
}

// ModePreset selects a curated set of tool groups for a runtime mode.
type ModePreset string

const (
	PresetCLI       ModePreset = "cli"
	PresetServerWeb ModePreset = "server_web"
	PresetServerAPI ModePreset = "server_api"
	PresetCI        ModePreset = "ci"
)

var presetGroups = map[ModePreset][]ToolGroup{
	PresetCLI:       {GroupCoreIO, GroupBashMgmt, GroupTaskMgmt, GroupWeb, GroupMedia, GroupInteraction},
	PresetServerWeb: {GroupCoreIO, GroupBashMgmt, GroupTaskMgmt, GroupWeb, GroupMedia, GroupCanvas, GroupBrowser},
	PresetServerAPI: {GroupCoreIO, GroupBashMgmt, GroupTaskMgmt, GroupWeb, GroupMedia, GroupInteraction, GroupCanvas},
	PresetCI:        {GroupCoreIO, GroupBashMgmt},
}

// GroupsForPreset returns the tool groups included in a preset.
func GroupsForPreset(p ModePreset) []ToolGroup {
	return presetGroups[p]
}

// PresetTools expands a preset into an ordered list of factory keys.
// For CI mode the "task" subagent tool is excluded from task_mgmt.
func PresetTools(p ModePreset) []string {
	groups := presetGroups[p]
	if groups == nil {
		groups = presetGroups[PresetCLI]
	}

	var out []string
	for _, g := range groups {
		tools := groupTools[g]
		if p == PresetCI && g == GroupTaskMgmt {
			tools = withoutTask(tools)
		}
		out = append(out, tools...)
	}
	return out
}

func withoutTask(tools []string) []string {
	var filtered []string
	for _, t := range tools {
		if t != "task" {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// presetForEntry maps an EntryPoint to its default ModePreset.
func presetForEntry(entry EntryPoint) ModePreset {
	switch entry {
	case EntryPointCI:
		return PresetCI
	case EntryPointPlatform:
		return PresetServerWeb
	default:
		return PresetCLI
	}
}
