package system_prompt_eval

import (
	"context"
	"strings"
	"testing"

	"github.com/cinience/saker/eval"
	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/testutil"
	"github.com/cinience/saker/pkg/tool"
)

type noopModel struct{}

func (noopModel) Complete(context.Context, model.Request) (*model.Response, error) {
	return &model.Response{Message: model.Message{Role: "assistant", Content: "ok"}}, nil
}

func (noopModel) CompleteStream(_ context.Context, req model.Request, cb model.StreamHandler) error {
	resp, _ := noopModel{}.Complete(nil, req)
	if cb != nil {
		return cb(model.StreamResult{Final: true, Response: resp})
	}
	return nil
}

// customTool is a minimal tool for testing custom tool registration in system prompt.
type customTool struct {
	name string
	desc string
}

func (c customTool) Name() string             { return c.name }
func (c customTool) Description() string      { return c.desc }
func (c customTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (c customTool) Execute(_ context.Context, _ map[string]any) (*tool.ToolResult, error) {
	return &tool.ToolResult{Output: "ok"}, nil
}

func TestEval_SystemPromptContainsBuiltinTools(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "system_prompt_builtin_tools"}

	// Create runtime with all builtin tools (nil = all defaults).
	root := testutil.TempHome(t)
	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot: root,
		Model:       noopModel{},
	})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	defer rt.Close()

	infos := rt.ToolInfos()
	toolNames := make(map[string]bool, len(infos))
	for _, info := range infos {
		toolNames[info.Name] = true
	}

	// Core builtin tools that must always be registered (PascalCase names).
	expectedTools := []string{"bash", "read", "write", "edit", "grep", "glob"}

	for _, name := range expectedTools {
		pass := toolNames[name]
		score := 0.0
		if pass {
			score = 1.0
		}
		suite.Add(eval.EvalResult{
			Name:     "builtin_tool_" + name,
			Pass:     pass,
			Score:    score,
			Expected: name,
			Got:      boolStr(pass),
		})
		if !pass {
			t.Errorf("expected builtin tool %q to be registered", name)
		}
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func TestEval_SystemPromptCustomToolRegistration(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "system_prompt_custom_tools"}

	root := testutil.TempHome(t)
	ct := customTool{name: "eval_custom_tool", desc: "A custom tool for eval testing"}

	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot: root,
		Model:       noopModel{},
		CustomTools: []tool.Tool{ct},
	})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	defer rt.Close()

	infos := rt.ToolInfos()
	found := false
	for _, info := range infos {
		if info.Name == "eval_custom_tool" {
			found = true
			break
		}
	}

	pass := found
	score := 0.0
	if pass {
		score = 1.0
	}
	suite.Add(eval.EvalResult{
		Name:  "custom_tool_registered",
		Pass:  pass,
		Score: score,
	})
	if !pass {
		t.Error("custom tool not found in runtime ToolInfos")
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func TestEval_SystemPromptClaudeMDAppended(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "system_prompt_claudemd"}

	root := testutil.TempHome(t)
	testutil.WriteFile(t, root, "CLAUDE.md", "# Project Rules\n\nAlways use Go standard library.")

	// We need a model that captures the system prompt from the request.
	var capturedSystem string
	captureModel := &captureSystemModel{capture: func(s string) { capturedSystem = s }}

	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot:         root,
		Model:               captureModel,
		EnabledBuiltinTools: []string{},
	})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	defer rt.Close()

	// Run a request so the model receives the system prompt.
	_, _ = rt.Run(context.Background(), api.Request{
		Prompt:    "hello",
		SessionID: "eval-claudemd",
	})

	pass := strings.Contains(capturedSystem, "Always use Go standard library")
	score := 0.0
	if pass {
		score = 1.0
	}
	suite.Add(eval.EvalResult{
		Name:     "claudemd_in_system_prompt",
		Pass:     pass,
		Score:    score,
		Expected: "Always use Go standard library",
		Got:      truncate(capturedSystem, 200),
	})
	if !pass {
		t.Error("CLAUDE.md content not found in system prompt")
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func TestEval_SystemPromptDisabledBuiltins(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "system_prompt_disabled_builtins"}

	root := testutil.TempHome(t)
	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot:         root,
		Model:               noopModel{},
		EnabledBuiltinTools: []string{}, // disable all
	})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	defer rt.Close()

	infos := rt.ToolInfos()

	// With empty EnabledBuiltinTools, no builtin tools should be registered.
	builtinNames := []string{"bash", "read", "write", "grep", "glob"}
	registeredBuiltins := 0
	for _, info := range infos {
		for _, bn := range builtinNames {
			if info.Name == bn {
				registeredBuiltins++
			}
		}
	}

	pass := registeredBuiltins == 0
	score := 0.0
	if pass {
		score = 1.0
	}
	suite.Add(eval.EvalResult{
		Name:  "no_builtins_when_disabled",
		Pass:  pass,
		Score: score,
		Details: map[string]any{
			"registered_builtins": registeredBuiltins,
			"total_tools":         len(infos),
		},
	})
	if !pass {
		t.Errorf("expected 0 builtin tools, found %d", registeredBuiltins)
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

// captureSystemModel captures the system prompt from model requests.
type captureSystemModel struct {
	capture func(string)
}

func (m *captureSystemModel) Complete(_ context.Context, req model.Request) (*model.Response, error) {
	if m.capture != nil {
		// System prompt may be in System or SystemBlocks.
		sys := req.System
		if len(req.SystemBlocks) > 0 {
			sys = strings.Join(req.SystemBlocks, "\n\n")
		}
		m.capture(sys)
	}
	return &model.Response{
		Message:    model.Message{Role: "assistant", Content: "ok"},
		StopReason: "end_turn",
	}, nil
}

func (m *captureSystemModel) CompleteStream(ctx context.Context, req model.Request, cb model.StreamHandler) error {
	resp, err := m.Complete(ctx, req)
	if err != nil {
		return err
	}
	if cb != nil {
		return cb(model.StreamResult{Final: true, Response: resp})
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
