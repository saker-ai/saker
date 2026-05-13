package toolgroups_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/model"
)

// ---------------------------------------------------------------------------
// scriptedModel — returns pre-scripted responses in order
// ---------------------------------------------------------------------------

type scriptedModel struct {
	mu        sync.Mutex
	responses []*model.Response
}

func (s *scriptedModel) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.responses) == 0 {
		return nil, errors.New("no scripted responses left")
	}
	resp := s.responses[0]
	s.responses = s.responses[1:]
	return resp, nil
}

func (s *scriptedModel) CompleteStream(ctx context.Context, req model.Request, cb model.StreamHandler) error {
	resp, err := s.Complete(ctx, req)
	if err != nil {
		return err
	}
	if cb != nil {
		return cb(model.StreamResult{Final: true, Response: resp})
	}
	return nil
}

func clearAigoEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"DASHSCOPE_API_KEY", "OPENAI_API_KEY",
		"NEWAPI_API_KEY", "ARK_API_KEY",
		"OPENROUTER_API_KEY", "VOLC_SPEECH_ACCESS_TOKEN",
		"GOOGLE_API_KEY", "GEMINI_API_KEY",
		"FLUX_API_KEY", "STABILITY_API_KEY",
		"IDEOGRAM_API_KEY", "RECRAFT_API_KEY",
		"MIDJOURNEY_API_KEY", "JIMENG_API_KEY",
		"LIBLIB_ACCESS_KEY", "KLING_API_KEY",
		"HAILUO_API_KEY", "LUMA_API_KEY",
		"RUNWAY_API_KEY", "PIKA_API_KEY",
		"HEDRA_API_KEY", "ELEVENLABS_API_KEY",
		"MINIMAX_API_KEY", "SUNO_API_KEY",
		"MESHY_API_KEY", "NVIDIA_API_KEY",
	} {
		t.Setenv(k, "")
	}
}

func doneResponse() *model.Response {
	return &model.Response{Message: model.Message{Role: "assistant", Content: "done"}}
}

func toolCallResponse(name string, args map[string]any) *model.Response {
	return &model.Response{
		Message: model.Message{
			Role: "assistant",
			ToolCalls: []model.ToolCall{{
				ID:        "call-1",
				Name:      name,
				Arguments: args,
			}},
		},
	}
}

// ---------------------------------------------------------------------------
// Test: CLI preset excludes canvas and browser tools
// ---------------------------------------------------------------------------

func TestCLIPreset_ExcludesCanvasAndBrowser(t *testing.T) {
	t.Parallel()
	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot: t.TempDir(),
		EntryPoint:  api.EntryPointCLI,
		Model:       &scriptedModel{responses: []*model.Response{doneResponse()}},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	tools := rt.AvailableTools()
	names := toolNames(tools)

	forbidden := []string{"canvas_get_node", "canvas_list_nodes", "canvas_table_write", "browser", "webhook"}
	for _, name := range forbidden {
		if names[name] {
			t.Errorf("CLI preset should NOT include %q", name)
		}
	}

	required := []string{"bash", "read", "write", "edit", "grep", "glob", "web_fetch", "web_search", "ask_user_question"}
	for _, name := range required {
		if !names[name] {
			t.Errorf("CLI preset missing expected tool %q", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: Platform preset includes canvas and browser tools
// ---------------------------------------------------------------------------

func TestPlatformPreset_IncludesCanvasAndBrowser(t *testing.T) {
	t.Parallel()
	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot: t.TempDir(),
		EntryPoint:  api.EntryPointPlatform,
		Model:       &scriptedModel{responses: []*model.Response{doneResponse()}},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	tools := rt.AvailableTools()
	names := toolNames(tools)

	required := []string{"bash", "read", "write", "browser", "webhook", "canvas_get_node"}
	for _, name := range required {
		if !names[name] {
			t.Errorf("Platform preset missing %q", name)
		}
	}

	excluded := []string{"ask_user_question", "skill", "slash_command"}
	for _, name := range excluded {
		if names[name] {
			t.Errorf("Platform preset should NOT include %q", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: CI preset is minimal (only core_io + bash_mgmt)
// ---------------------------------------------------------------------------

func TestCIPreset_MinimalTools(t *testing.T) {
	clearAigoEnv(t)
	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot: t.TempDir(),
		EntryPoint:  api.EntryPointCI,
		Model:       &scriptedModel{responses: []*model.Response{doneResponse()}},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	tools := rt.AvailableTools()
	names := toolNames(tools)

	required := []string{"bash", "read", "write", "edit", "grep", "glob", "bash_output", "bash_status", "kill_task"}
	for _, name := range required {
		if !names[name] {
			t.Errorf("CI preset missing %q", name)
		}
	}

	excluded := []string{"task", "web_fetch", "ask_user_question", "browser", "canvas_get_node"}
	for _, name := range excluded {
		if names[name] {
			t.Errorf("CI preset should NOT include %q", name)
		}
	}

	if len(tools) > 12 {
		t.Errorf("CI preset should be minimal, got %d tools", len(tools))
	}
}

// ---------------------------------------------------------------------------
// Test: ModePreset override takes precedence over EntryPoint
// ---------------------------------------------------------------------------

func TestModePresetOverride(t *testing.T) {
	t.Parallel()
	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot: t.TempDir(),
		EntryPoint:  api.EntryPointCLI,
		ModePreset:  api.PresetServerAPI,
		Model:       &scriptedModel{responses: []*model.Response{doneResponse()}},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	tools := rt.AvailableTools()
	names := toolNames(tools)

	if names["canvas_get_node"] {
		t.Error("server_api preset should NOT include canvas_get_node")
	}
	if !names["web_fetch"] {
		t.Error("server_api preset should include web_fetch")
	}
	if !names["ask_user_question"] {
		t.Error("server_api preset should include ask_user_question")
	}
	if names["browser"] {
		t.Error("server_api preset should NOT include browser")
	}
}

// ---------------------------------------------------------------------------
// Test: EnabledBuiltinTools whitelist filters the preset
// ---------------------------------------------------------------------------

func TestEnabledBuiltinToolsWhitelist(t *testing.T) {
	clearAigoEnv(t)
	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot:         t.TempDir(),
		EntryPoint:          api.EntryPointCLI,
		Model:               &scriptedModel{responses: []*model.Response{doneResponse()}},
		EnabledBuiltinTools: []string{"bash", "file_read", "grep"},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	tools := rt.AvailableTools()
	if len(tools) != 3 {
		names := make([]string, len(tools))
		for i, t := range tools {
			names[i] = t.Name
		}
		sort.Strings(names)
		t.Fatalf("expected 3 tools, got %d: %v", len(tools), names)
	}
}

// ---------------------------------------------------------------------------
// Test: DisallowedTools blacklist removes specific tools
// ---------------------------------------------------------------------------

func TestDisallowedToolsBlacklist(t *testing.T) {
	t.Parallel()
	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot:     t.TempDir(),
		EntryPoint:      api.EntryPointCLI,
		Model:           &scriptedModel{responses: []*model.Response{doneResponse()}},
		DisallowedTools: []string{"web_fetch", "web_search"},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	tools := rt.AvailableTools()
	names := toolNames(tools)

	if names["web_fetch"] || names["web_search"] {
		t.Error("disallowed tools should not be present")
	}
	if !names["bash"] || !names["read"] {
		t.Error("non-disallowed tools should still be present")
	}
}

// ---------------------------------------------------------------------------
// Test: Runtime.Run completes with tool call (file_read)
// ---------------------------------------------------------------------------

func TestRuntimeRun_ToolExecution(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	mdl := &scriptedModel{responses: []*model.Response{
		toolCallResponse("read", map[string]any{"file_path": tmpDir + "/test.txt"}),
		doneResponse(),
	}}

	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot:                tmpDir,
		EntryPoint:                 api.EntryPointCLI,
		Model:                      mdl,
		DangerouslySkipPermissions: true,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	resp, err := rt.Run(context.Background(), api.Request{Prompt: "read test.txt"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.Result.Output != "done" {
		t.Errorf("unexpected output: %q", resp.Result.Output)
	}
}

// ---------------------------------------------------------------------------
// Test: Runtime.Run with bash tool in CI mode
// ---------------------------------------------------------------------------

func TestRuntimeRun_CIBashExecution(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	mdl := &scriptedModel{responses: []*model.Response{
		toolCallResponse("bash", map[string]any{"command": "echo hello"}),
		doneResponse(),
	}}

	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot:                tmpDir,
		EntryPoint:                 api.EntryPointCI,
		Model:                      mdl,
		DangerouslySkipPermissions: true,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	resp, err := rt.Run(context.Background(), api.Request{Prompt: "run echo"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.Result.Output != "done" {
		t.Errorf("unexpected output: %q", resp.Result.Output)
	}
}

// ---------------------------------------------------------------------------
// Test: Runtime.RunStream produces events
// ---------------------------------------------------------------------------

func TestRuntimeRunStream(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	mdl := &scriptedModel{responses: []*model.Response{doneResponse()}}

	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot: tmpDir,
		EntryPoint:  api.EntryPointCLI,
		Model:       mdl,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	ch, err := rt.RunStream(context.Background(), api.Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var count int
	for range ch {
		count++
	}
	if count == 0 {
		t.Error("expected at least one stream event")
	}
}

// ---------------------------------------------------------------------------
// Test: Multiple sequential runs on same session
// ---------------------------------------------------------------------------

func TestRuntimeMultipleRuns(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	mdl := &scriptedModel{responses: []*model.Response{
		doneResponse(),
		doneResponse(),
		doneResponse(),
	}}

	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot: tmpDir,
		EntryPoint:  api.EntryPointCLI,
		Model:       mdl,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	for i := 0; i < 3; i++ {
		resp, err := rt.Run(context.Background(), api.Request{Prompt: "turn"})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if resp.Result.Output != "done" {
			t.Errorf("run %d: unexpected output %q", i, resp.Result.Output)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: AvailableToolsForWhitelist intersection
// ---------------------------------------------------------------------------

func TestAvailableToolsForWhitelist(t *testing.T) {
	t.Parallel()
	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot: t.TempDir(),
		EntryPoint:  api.EntryPointCLI,
		Model:       &scriptedModel{responses: []*model.Response{doneResponse()}},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	all := rt.AvailableTools()
	filtered := rt.AvailableToolsForWhitelist([]string{"bash", "read"})

	if len(filtered) != 2 {
		names := make([]string, len(filtered))
		for i, t := range filtered {
			names[i] = t.Name
		}
		t.Fatalf("expected 2 tools, got %d: %v", len(filtered), names)
	}
	if len(all) <= len(filtered) {
		t.Error("full set should be larger than whitelist-filtered set")
	}
}

// ---------------------------------------------------------------------------
// Test: Timeout cancels the run
// ---------------------------------------------------------------------------

func TestRuntimeRun_Timeout(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	// Model never responds — relies on timeout
	mdl := &scriptedModel{}

	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot:   tmpDir,
		EntryPoint:    api.EntryPointCLI,
		Model:         mdl,
		MaxIterations: 1,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = rt.Run(ctx, api.Request{Prompt: "wait forever"})
	if err == nil {
		t.Fatal("expected error from timeout/cancel")
	}
}

// ---------------------------------------------------------------------------
// Test: EnabledBuiltinToolKeys returns correct key list
// ---------------------------------------------------------------------------

func TestEnabledBuiltinToolKeys(t *testing.T) {
	t.Parallel()

	keys := api.EnabledBuiltinToolKeys(api.Options{EntryPoint: api.EntryPointCLI})
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}

	if !set["bash"] || !set["file_read"] || !set["grep"] {
		t.Error("CLI keys should include core tools")
	}
	if set["canvas_get_node"] || set["browser"] {
		t.Error("CLI keys should not include canvas/browser")
	}

	ciKeys := api.EnabledBuiltinToolKeys(api.Options{EntryPoint: api.EntryPointCI})
	if len(ciKeys) >= len(keys) {
		t.Error("CI should have fewer keys than CLI")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func toolNames(tools []model.ToolDefinition) map[string]bool {
	m := make(map[string]bool, len(tools))
	for _, t := range tools {
		m[strings.TrimSpace(t.Name)] = true
	}
	return m
}
