package api

import (
	"sort"
	"testing"
)

func TestToolsInGroup_nonEmpty(t *testing.T) {
	for _, g := range []ToolGroup{GroupCoreIO, GroupBashMgmt, GroupTaskMgmt, GroupWeb, GroupMedia, GroupInteraction, GroupCanvas, GroupBrowser} {
		if len(ToolsInGroup(g)) == 0 {
			t.Errorf("group %q is empty", g)
		}
	}
}

func TestPresetTools_CLI(t *testing.T) {
	tools := PresetTools(PresetCLI)
	set := toSet(tools)

	mustHave := []string{"bash", "file_read", "grep", "glob", "web_fetch", "ask_user_question", "task"}
	for _, name := range mustHave {
		if !set[name] {
			t.Errorf("PresetCLI missing %q", name)
		}
	}

	mustNotHave := []string{"canvas_get_node", "browser", "webhook"}
	for _, name := range mustNotHave {
		if set[name] {
			t.Errorf("PresetCLI should not include %q", name)
		}
	}
}

func TestPresetTools_ServerWeb(t *testing.T) {
	tools := PresetTools(PresetServerWeb)
	set := toSet(tools)

	mustHave := []string{"bash", "canvas_get_node", "browser", "webhook", "web_fetch"}
	for _, name := range mustHave {
		if !set[name] {
			t.Errorf("PresetServerWeb missing %q", name)
		}
	}

	mustNotHave := []string{"ask_user_question", "skill", "slash_command"}
	for _, name := range mustNotHave {
		if set[name] {
			t.Errorf("PresetServerWeb should not include %q", name)
		}
	}
}

func TestPresetTools_ServerAPI(t *testing.T) {
	tools := PresetTools(PresetServerAPI)
	set := toSet(tools)

	mustHave := []string{"bash", "file_read", "canvas_get_node", "image_read"}
	for _, name := range mustHave {
		if !set[name] {
			t.Errorf("PresetServerAPI missing %q", name)
		}
	}

	mustNotHave := []string{"web_fetch", "web_search", "browser", "webhook", "ask_user_question"}
	for _, name := range mustNotHave {
		if set[name] {
			t.Errorf("PresetServerAPI should not include %q", name)
		}
	}
}

func TestPresetTools_CI(t *testing.T) {
	tools := PresetTools(PresetCI)
	set := toSet(tools)

	mustHave := []string{"bash", "file_read", "grep", "glob", "bash_output"}
	for _, name := range mustHave {
		if !set[name] {
			t.Errorf("PresetCI missing %q", name)
		}
	}

	mustNotHave := []string{"task", "web_fetch", "ask_user_question", "canvas_get_node", "browser"}
	for _, name := range mustNotHave {
		if set[name] {
			t.Errorf("PresetCI should not include %q", name)
		}
	}
}

func TestAllFactoryKeysCovered(t *testing.T) {
	allKeys := make(map[string]bool)
	for _, g := range []ToolGroup{GroupCoreIO, GroupBashMgmt, GroupTaskMgmt, GroupWeb, GroupMedia, GroupInteraction, GroupCanvas, GroupBrowser} {
		for _, key := range ToolsInGroup(g) {
			if allKeys[key] {
				t.Errorf("duplicate key %q across groups", key)
			}
			allKeys[key] = true
		}
	}
	if len(allKeys) == 0 {
		t.Fatal("no tools defined in any group")
	}
}

func TestPresetForEntry(t *testing.T) {
	tests := []struct {
		entry EntryPoint
		want  ModePreset
	}{
		{EntryPointCLI, PresetCLI},
		{EntryPointPlatform, PresetServerWeb},
		{EntryPointCI, PresetCI},
		{"unknown", PresetCLI},
	}
	for _, tt := range tests {
		if got := presetForEntry(tt.entry); got != tt.want {
			t.Errorf("presetForEntry(%q) = %q, want %q", tt.entry, got, tt.want)
		}
	}
}

func TestPresetToolsNoDuplicates(t *testing.T) {
	for _, p := range []ModePreset{PresetCLI, PresetServerWeb, PresetServerAPI, PresetCI} {
		tools := PresetTools(p)
		seen := make(map[string]bool, len(tools))
		for _, tool := range tools {
			if seen[tool] {
				t.Errorf("preset %q has duplicate tool %q", p, tool)
			}
			seen[tool] = true
		}
	}
}

func TestPresetToolsAreSupersets(t *testing.T) {
	ci := toSet(PresetTools(PresetCI))
	apiSet := toSet(PresetTools(PresetServerAPI))

	// CI is a subset of server_api (minus task tools)
	for key := range ci {
		if !apiSet[key] {
			t.Errorf("CI tool %q not in server_api", key)
		}
	}
}

func toSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}

func TestBuiltinOrderMatchesPreset(t *testing.T) {
	// builtinOrder with empty preset should derive from entry
	got := builtinOrder(EntryPointCLI, "")
	want := PresetTools(PresetCLI)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("builtinOrder(CLI) len=%d, PresetTools(CLI) len=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("mismatch at %d: %q vs %q", i, got[i], want[i])
		}
	}
}
