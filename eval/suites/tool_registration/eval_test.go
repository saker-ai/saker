package tool_registration_eval

import (
	"context"
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

type dummyTool struct {
	name string
	desc string
}

func (d dummyTool) Name() string             { return d.name }
func (d dummyTool) Description() string      { return d.desc }
func (d dummyTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (d dummyTool) Execute(_ context.Context, _ map[string]any) (*tool.ToolResult, error) {
	return &tool.ToolResult{Output: "ok"}, nil
}

func TestEval_AllBuiltinToolsRegister(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "tool_registration_builtins"}

	root := testutil.TempHome(t)
	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot: root,
		Model:       noopModel{},
		// nil EnabledBuiltinTools = register all defaults
	})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	defer rt.Close()

	infos := rt.ToolInfos()
	registered := make(map[string]api.ToolInfo, len(infos))
	for _, info := range infos {
		registered[info.Name] = info
	}

	// Core builtin tools that must always be present (PascalCase names).
	expectedTools := map[string]string{
		"Bash":  "Bash",
		"Read":  "Read",
		"Write": "Write",
		"Edit":  "Edit",
		"Grep":  "Grep",
		"Glob":  "Glob",
	}

	for name, label := range expectedTools {
		info, found := registered[name]
		pass := found && info.Description != ""
		score := 0.0
		if pass {
			score = 1.0
		}
		suite.Add(eval.EvalResult{
			Name:     "builtin_" + label,
			Pass:     pass,
			Score:    score,
			Expected: name,
			Got:      boolStr(found),
		})
		if !pass {
			t.Errorf("expected builtin tool %q (%s) to be registered with description", name, label)
		}
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func TestEval_ToolSchemaValidity(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "tool_schema_validity"}

	root := testutil.TempHome(t)
	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot: root,
		Model:       noopModel{},
	})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	defer rt.Close()

	toolsToCheck := []string{"Bash", "Read", "Write", "Grep", "Glob"}
	for _, name := range toolsToCheck {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			schema, err := rt.ToolSchema(name, "")
			pass := err == nil && schema != nil && len(schema.Schema) > 0
			score := 0.0
			if pass {
				score = 1.0
			}
			suite.Add(eval.EvalResult{
				Name:  "schema_" + name,
				Pass:  pass,
				Score: score,
			})
			if err != nil {
				t.Errorf("ToolSchema(%q): %v", name, err)
			} else if schema == nil || len(schema.Schema) == 0 {
				t.Errorf("ToolSchema(%q): empty schema", name)
			}
		})
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func TestEval_CustomToolIntegration(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "tool_registration_custom"}

	root := testutil.TempHome(t)
	custom1 := dummyTool{name: "eval_tool_alpha", desc: "Alpha tool"}
	custom2 := dummyTool{name: "eval_tool_beta", desc: "Beta tool"}

	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot:         root,
		Model:               noopModel{},
		EnabledBuiltinTools: []string{},
		CustomTools:         []tool.Tool{custom1, custom2},
	})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	defer rt.Close()

	infos := rt.ToolInfos()
	found := make(map[string]bool)
	for _, info := range infos {
		found[info.Name] = true
	}

	cases := []struct {
		name     string
		toolName string
	}{
		{"custom_alpha_registered", "eval_tool_alpha"},
		{"custom_beta_registered", "eval_tool_beta"},
	}

	for _, tc := range cases {
		pass := found[tc.toolName]
		score := 0.0
		if pass {
			score = 1.0
		}
		suite.Add(eval.EvalResult{
			Name:  tc.name,
			Pass:  pass,
			Score: score,
		})
		if !pass {
			t.Errorf("custom tool %q not registered", tc.toolName)
		}
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func TestEval_DisallowedToolExclusion(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "tool_registration_disallowed"}

	root := testutil.TempHome(t)
	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot:     root,
		Model:           noopModel{},
		DisallowedTools: []string{"grep", "glob"},
	})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	defer rt.Close()

	infos := rt.ToolInfos()
	found := make(map[string]bool)
	for _, info := range infos {
		found[info.Name] = true
	}

	// grep and glob should be excluded.
	cases := []struct {
		name     string
		toolName string
		wantGone bool
	}{
		{"grep_excluded", "Grep", true},
		{"glob_excluded", "Glob", true},
		{"bash_still_present", "Bash", false},
	}

	for _, tc := range cases {
		var pass bool
		if tc.wantGone {
			pass = !found[tc.toolName]
		} else {
			pass = found[tc.toolName]
		}
		score := 0.0
		if pass {
			score = 1.0
		}
		suite.Add(eval.EvalResult{
			Name:  tc.name,
			Pass:  pass,
			Score: score,
		})
		if !pass {
			if tc.wantGone {
				t.Errorf("tool %q should be excluded but is registered", tc.toolName)
			} else {
				t.Errorf("tool %q should still be registered but is missing", tc.toolName)
			}
		}
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
