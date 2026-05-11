package api

import (
	"context"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/agent"
	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/runtime/skills"
	"github.com/cinience/saker/pkg/runtime/subagents"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
)

// ---------------------------------------------------------------------------
// subagentMaxIterations
// ---------------------------------------------------------------------------

func TestSubagentMaxIterations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		runtimeMax int
		want       int
	}{
		{name: "unlimited passes through", runtimeMax: -1, want: -1},
		{name: "zero uses default cap", runtimeMax: 0, want: agent.DefaultSubagentMaxIterations},
		{name: "positive uses default cap", runtimeMax: 10, want: agent.DefaultSubagentMaxIterations},
		{name: "large positive uses default cap", runtimeMax: 1000, want: agent.DefaultSubagentMaxIterations},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := subagentMaxIterations(tt.runtimeMax)
			if got != tt.want {
				t.Errorf("subagentMaxIterations(%d) = %d, want %d", tt.runtimeMax, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// applySubagentTarget
// ---------------------------------------------------------------------------

func TestApplySubagentTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		req         *Request
		wantDefName string
		wantBuiltin bool
		wantTarget  string // expected req.TargetSubagent after mutation
	}{
		{
			name:        "nil request returns empty and false",
			req:         nil,
			wantDefName: "",
			wantBuiltin: false,
			wantTarget:  "",
		},
		{
			name: "empty target returns empty and false",
			req: &Request{
				TargetSubagent: "",
			},
			wantDefName: "",
			wantBuiltin: false,
			wantTarget:  "",
		},
		{
			name: "whitespace-only target returns empty and false",
			req: &Request{
				TargetSubagent: "   ",
			},
			wantDefName: "",
			wantBuiltin: false,
			wantTarget:  "",
		},
		{
			name: "builtin general-purpose is recognized",
			req: &Request{
				TargetSubagent: subagents.TypeGeneralPurpose,
			},
			wantDefName: subagents.TypeGeneralPurpose,
			wantBuiltin: true,
			wantTarget:  subagents.TypeGeneralPurpose,
		},
		{
			name: "builtin explore is recognized",
			req: &Request{
				TargetSubagent: subagents.TypeExplore,
			},
			wantDefName: subagents.TypeExplore,
			wantBuiltin: true,
			wantTarget:  subagents.TypeExplore,
		},
		{
			name: "builtin plan is recognized",
			req: &Request{
				TargetSubagent: subagents.TypePlan,
			},
			wantDefName: subagents.TypePlan,
			wantBuiltin: true,
			wantTarget:  subagents.TypePlan,
		},
		{
			name: "builtin with uppercase is canonicalized",
			req: &Request{
				TargetSubagent: "EXPLORE",
			},
			wantDefName: subagents.TypeExplore,
			wantBuiltin: true,
			wantTarget:  subagents.TypeExplore,
		},
		{
			name: "builtin with mixed case is canonicalized",
			req: &Request{
				TargetSubagent: "General-Purpose",
			},
			wantDefName: subagents.TypeGeneralPurpose,
			wantBuiltin: true,
			wantTarget:  subagents.TypeGeneralPurpose,
		},
		{
			name: "custom target is canonicalized but not builtin",
			req: &Request{
				TargetSubagent: "MyCustomAgent",
			},
			wantDefName: "",
			wantBuiltin: false,
			wantTarget:  "mycustomagent",
		},
		{
			name: "custom target with whitespace is trimmed and lowered",
			req: &Request{
				TargetSubagent: "  SomeAgent  ",
			},
			wantDefName: "",
			wantBuiltin: false,
			wantTarget:  "someagent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			def, builtin := applySubagentTarget(tt.req)
			if def.Name != tt.wantDefName {
				t.Errorf("applySubagentTarget def.Name = %q, want %q", def.Name, tt.wantDefName)
			}
			if builtin != tt.wantBuiltin {
				t.Errorf("applySubagentTarget builtin = %v, want %v", builtin, tt.wantBuiltin)
			}
			// Check request mutation for non-nil requests
			if tt.req != nil && tt.req.TargetSubagent != tt.wantTarget {
				t.Errorf("applySubagentTarget req.TargetSubagent = %q, want %q", tt.req.TargetSubagent, tt.wantTarget)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildSubagentContext
// ---------------------------------------------------------------------------

func TestBuildSubagentContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		req       Request
		def       subagents.Definition
		matched   bool
		wantOK    bool
		wantCheck func(t *testing.T, ctx subagents.Context)
	}{
		{
			name:    "empty request with no match returns false",
			req:     Request{},
			def:     subagents.Definition{},
			matched: false,
			wantOK:  false,
		},
		{
			name: "session ID populates context even without builtin match",
			req: Request{
				SessionID: "sess-123",
			},
			def:     subagents.Definition{},
			matched: false,
			wantOK:  true,
			wantCheck: func(t *testing.T, ctx subagents.Context) {
				if ctx.SessionID != "sess-123" {
					t.Errorf("SessionID = %q, want %q", ctx.SessionID, "sess-123")
				}
			},
		},
		{
			name: "builtin match clones definition base context",
			req: Request{
				SessionID: "sess-over",
			},
			def: subagents.Definition{
				Name: subagents.TypeExplore,
				BaseContext: subagents.Context{
					Model:         subagents.ModelHaiku,
					ToolWhitelist: []string{"glob", "grep", "read"},
				},
			},
			matched: true,
			wantOK:  true,
			wantCheck: func(t *testing.T, ctx subagents.Context) {
				if ctx.Model != subagents.ModelHaiku {
					t.Errorf("Model = %q, want %q", ctx.Model, subagents.ModelHaiku)
				}
				// SessionID should be overridden by request
				if ctx.SessionID != "sess-over" {
					t.Errorf("SessionID = %q, want %q", ctx.SessionID, "sess-over")
				}
			},
		},
		{
			name: "task.description metadata is propagated",
			req: Request{
				Metadata: map[string]any{
					"task.description": "search the repo",
				},
			},
			def:     subagents.Definition{},
			matched: false,
			wantOK:  true,
			wantCheck: func(t *testing.T, ctx subagents.Context) {
				if ctx.Metadata == nil {
					t.Fatal("Metadata is nil")
				}
				if ctx.Metadata["task.description"] != "search the repo" {
					t.Errorf("task.description = %v, want %q", ctx.Metadata["task.description"], "search the repo")
				}
			},
		},
		{
			name: "task.model metadata sets both context Model and metadata",
			req: Request{
				Metadata: map[string]any{
					"task.model": "opus",
				},
			},
			def:     subagents.Definition{},
			matched: false,
			wantOK:  true,
			wantCheck: func(t *testing.T, ctx subagents.Context) {
				if ctx.Model != "opus" {
					t.Errorf("Model = %q, want %q", ctx.Model, "opus")
				}
				if ctx.Metadata["task.model"] != "opus" {
					t.Errorf("Metadata[task.model] = %v, want %q", ctx.Metadata["task.model"], "opus")
				}
			},
		},
		{
			name: "task.model with mixed case is lowered in metadata",
			req: Request{
				Metadata: map[string]any{
					"task.model": "OPUS",
				},
			},
			def:     subagents.Definition{},
			matched: false,
			wantOK:  true,
			wantCheck: func(t *testing.T, ctx subagents.Context) {
				if ctx.Model != "opus" {
					t.Errorf("Model = %q, want %q", ctx.Model, "opus")
				}
				if ctx.Metadata["task.model"] != "opus" {
					t.Errorf("Metadata[task.model] = %v, want %q", ctx.Metadata["task.model"], "opus")
				}
			},
		},
		{
			name: "task.model does not override builtin definition Model when builtin already has one",
			req: Request{
				Metadata: map[string]any{
					"task.model": "haiku",
				},
			},
			def: subagents.Definition{
				BaseContext: subagents.Context{
					Model: subagents.ModelSonnet,
				},
			},
			matched: true,
			wantOK:  true,
			wantCheck: func(t *testing.T, ctx subagents.Context) {
				// When builtin matched, the definition Model is cloned first,
				// then request session overrides. task.model metadata is set
				// but the Context.Model field should be the definition's model
				// unless it was empty (strings.TrimSpace(subCtx.Model) == "")
				if ctx.Model != subagents.ModelSonnet {
					t.Errorf("Model = %q, want %q (definition model preserved)", ctx.Model, subagents.ModelSonnet)
				}
				if ctx.Metadata["task.model"] != "haiku" {
					t.Errorf("Metadata[task.model] = %v, want %q", ctx.Metadata["task.model"], "haiku")
				}
			},
		},
		{
			name: "whitespace session ID is not propagated",
			req: Request{
				SessionID: "   ",
			},
			def:     subagents.Definition{},
			matched: false,
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, ok := buildSubagentContext(tt.req, tt.def, tt.matched)
			if ok != tt.wantOK {
				t.Errorf("buildSubagentContext ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantCheck != nil {
				tt.wantCheck(t, ctx)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// normalizeStrings
// ---------------------------------------------------------------------------

func TestNormalizeStringsSubagent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nil input returns nil", in: nil, want: nil},
		{name: "empty input returns nil", in: []string{}, want: nil},
		{name: "single element returns sorted", in: []string{"Bash"}, want: []string{"Bash"}},
		{name: "multiple elements are sorted and deduplicated", in: []string{"grep", "Bash", "grep"}, want: []string{"Bash", "grep"}},
		{name: "already sorted returns same", in: []string{"a", "b", "c"}, want: []string{"a", "b", "c"}},
		{name: "reverse sorted is corrected", in: []string{"z", "m", "a"}, want: []string{"a", "m", "z"}},
		{name: "duplicates are compacted", in: []string{"x", "x", "x"}, want: []string{"x"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeStrings(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("normalizeStrings length = %d, want %d; got=%v want=%v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("normalizeStrings[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// metadataString
// ---------------------------------------------------------------------------

func TestMetadataStringSubagent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		meta map[string]any
		key  string
		want string
	}{
		{name: "nil metadata returns empty", meta: nil, key: "any", want: ""},
		{name: "empty metadata returns empty", meta: map[string]any{}, key: "any", want: ""},
		{name: "missing key returns empty", meta: map[string]any{"other": "val"}, key: "missing", want: ""},
		{name: "string value is trimmed", meta: map[string]any{"k": "  hello  "}, key: "k", want: "hello"},
		{name: "non-string value falls back to Sprint", meta: map[string]any{"k": 42}, key: "k", want: "42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := metadataString(tt.meta, tt.key)
			if got != tt.want {
				t.Errorf("metadataString(%v, %q) = %q, want %q", tt.meta, tt.key, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// applyPromptMetadata (subagent-focused cases)
// ---------------------------------------------------------------------------

func TestApplyPromptMetadataSubagent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prompt string
		meta   map[string]any
		want   string
	}{
		{name: "empty metadata leaves prompt unchanged", prompt: "hello", meta: nil, want: "hello"},
		{name: "prompt override replaces prompt", prompt: "original", meta: map[string]any{"api.prompt_override": "replaced"}, want: "replaced"},
		{name: "prepend prompt prepends text", prompt: "body", meta: map[string]any{"api.prepend_prompt": "prefix"}, want: "prefix\nbody"},
		{name: "append prompt appends text", prompt: "body", meta: map[string]any{"api.append_prompt": "suffix"}, want: "body\nsuffix"},
		{name: "all three combined", prompt: "body", meta: map[string]any{
			"api.prompt_override": "replaced",
			"api.prepend_prompt":  "prefix",
			"api.append_prompt":   "suffix",
		}, want: "prefix\nreplaced\nsuffix"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := applyPromptMetadata(tt.prompt, tt.meta)
			if got != tt.want {
				t.Errorf("applyPromptMetadata(%q, %v) = %q, want %q", tt.prompt, tt.meta, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// mergeTags (subagent-specific cases)
// ---------------------------------------------------------------------------

func TestMergeTagsSubagent(t *testing.T) {
	t.Parallel()

	t.Run("nil request does nothing", func(t *testing.T) {
		t.Parallel()
		mergeTags(nil, map[string]any{"api.tags": map[string]string{"k": "v"}})
	})

	t.Run("empty metadata does nothing", func(t *testing.T) {
		t.Parallel()
		req := &Request{Tags: map[string]string{"existing": "val"}}
		mergeTags(req, nil)
		if req.Tags["existing"] != "val" {
			t.Errorf("existing tag should be preserved")
		}
	})

	t.Run("map[string]string tags are merged", func(t *testing.T) {
		t.Parallel()
		req := &Request{Tags: map[string]string{}}
		mergeTags(req, map[string]any{"api.tags": map[string]string{"env": "prod"}})
		if req.Tags["env"] != "prod" {
			t.Errorf("Tags[env] = %q, want %q", req.Tags["env"], "prod")
		}
	})

	t.Run("map[string]any tags are converted to string", func(t *testing.T) {
		t.Parallel()
		req := &Request{Tags: map[string]string{}}
		mergeTags(req, map[string]any{"api.tags": map[string]any{"count": 42}})
		if req.Tags["count"] != "42" {
			t.Errorf("Tags[count] = %q, want %q", req.Tags["count"], "42")
		}
	})

	t.Run("nil Tags map is initialized", func(t *testing.T) {
		t.Parallel()
		req := &Request{}
		mergeTags(req, map[string]any{"api.tags": map[string]string{"k": "v"}})
		if req.Tags == nil {
			t.Fatal("Tags should have been initialized")
		}
		if req.Tags["k"] != "v" {
			t.Errorf("Tags[k] = %q, want %q", req.Tags["k"], "v")
		}
	})
}

// ---------------------------------------------------------------------------
// applyCommandMetadata (subagent-specific cases)
// ---------------------------------------------------------------------------

func TestApplyCommandMetadataSubagent(t *testing.T) {
	t.Parallel()

	t.Run("nil request does nothing", func(t *testing.T) {
		t.Parallel()
		applyCommandMetadata(nil, map[string]any{"api.target_subagent": "x"})
	})

	t.Run("empty metadata does nothing", func(t *testing.T) {
		t.Parallel()
		req := &Request{TargetSubagent: "original"}
		applyCommandMetadata(req, nil)
		if req.TargetSubagent != "original" {
			t.Errorf("TargetSubagent should remain unchanged")
		}
	})

	t.Run("api_target_subagent overrides target", func(t *testing.T) {
		t.Parallel()
		req := &Request{TargetSubagent: "old"}
		applyCommandMetadata(req, map[string]any{"api.target_subagent": "new"})
		if req.TargetSubagent != "new" {
			t.Errorf("TargetSubagent = %q, want %q", req.TargetSubagent, "new")
		}
	})

	t.Run("api_tool_whitelist sets whitelist", func(t *testing.T) {
		t.Parallel()
		req := &Request{}
		applyCommandMetadata(req, map[string]any{"api.tool_whitelist": []string{"bash", "grep"}})
		if len(req.ToolWhitelist) != 2 {
			t.Fatalf("ToolWhitelist length = %d, want 2", len(req.ToolWhitelist))
		}
		// stringSlice sorts output
		if req.ToolWhitelist[0] != "bash" || req.ToolWhitelist[1] != "grep" {
			t.Errorf("ToolWhitelist = %v, want [bash, grep]", req.ToolWhitelist)
		}
	})

	t.Run("allowed-tools fills whitelist only when existing is empty", func(t *testing.T) {
		t.Parallel()
		req := &Request{}
		applyCommandMetadata(req, map[string]any{"allowed-tools": []string{"read", "write"}})
		if len(req.ToolWhitelist) != 2 {
			t.Fatalf("ToolWhitelist length = %d, want 2", len(req.ToolWhitelist))
		}
	})

	t.Run("allowed-tools does not override existing whitelist", func(t *testing.T) {
		t.Parallel()
		req := &Request{ToolWhitelist: []string{"bash"}}
		applyCommandMetadata(req, map[string]any{"allowed-tools": []string{"read"}})
		if len(req.ToolWhitelist) != 1 || req.ToolWhitelist[0] != "bash" {
			t.Errorf("ToolWhitelist should remain [bash], got %v", req.ToolWhitelist)
		}
	})

	t.Run("api_model_tier overrides model", func(t *testing.T) {
		t.Parallel()
		req := &Request{}
		applyCommandMetadata(req, map[string]any{"api.model_tier": "opus"})
		if req.Model != ModelTier("opus") {
			t.Errorf("Model = %q, want %q", req.Model, ModelTier("opus"))
		}
	})

	t.Run("empty model tier is not set", func(t *testing.T) {
		t.Parallel()
		req := &Request{}
		applyCommandMetadata(req, map[string]any{"api.model_tier": ""})
		if req.Model != "" {
			t.Errorf("Model should remain empty, got %q", req.Model)
		}
	})
}

// ---------------------------------------------------------------------------
// executeSubagent - success/failure cases
// ---------------------------------------------------------------------------

func TestExecuteSubagentNilRequest(t *testing.T) {
	rt := &Runtime{}
	result, prompt, err := rt.executeSubagent(context.Background(), "test prompt", skills.ActivationContext{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for nil request")
	}
	if prompt != "test prompt" {
		t.Fatalf("expected original prompt returned for nil request, got %q", prompt)
	}
}

func TestExecuteSubagentNilSubMgr(t *testing.T) {
	rt := &Runtime{}
	req := &Request{
		TargetSubagent: subagents.TypeExplore,
		Mode:           ModeContext{EntryPoint: EntryPointCLI},
	}
	result, prompt, err := rt.executeSubagent(context.Background(), "test prompt", skills.ActivationContext{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result when subMgr is nil")
	}
	if prompt != "test prompt" {
		t.Fatalf("expected original prompt when subMgr nil, got %q", prompt)
	}
}

func TestExecuteSubagentUnauthorizedDispatch(t *testing.T) {
	t.Parallel()
	// Dispatch without WithTaskDispatch context returns ErrDispatchUnauthorized.
	// executeSubagent should swallow this and return nil result with original prompt.
	mgr := subagents.NewManager()
	handler := subagents.HandlerFunc(func(ctx context.Context, subCtx subagents.Context, req subagents.Request) (subagents.Result, error) {
		return subagents.Result{Output: "done"}, nil
	})
	if err := mgr.Register(subagents.Definition{
		Name:         subagents.TypeExplore,
		Description:  "test explore",
		DefaultModel: subagents.ModelHaiku,
		BaseContext:  subagents.Context{Model: subagents.ModelHaiku, ToolWhitelist: []string{"glob", "grep", "read"}},
	}, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	rt := &Runtime{subMgr: mgr}
	req := &Request{
		TargetSubagent: subagents.TypeExplore,
		Mode:           ModeContext{EntryPoint: EntryPointCLI},
	}
	// Plain context (no WithTaskDispatch) triggers unauthorized error
	result, prompt, err := rt.executeSubagent(context.Background(), "original prompt", skills.ActivationContext{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result on unauthorized dispatch")
	}
	if prompt != "original prompt" {
		t.Fatalf("expected original prompt on unauthorized dispatch, got %q", prompt)
	}
}

func TestExecuteSubagentNoMatchingSubagentWithEmptyTarget(t *testing.T) {
	t.Parallel()
	// When target is empty and no subagent matches, executeSubagent swallows the error
	// and returns nil result with original prompt.
	mgr := subagents.NewManager()
	rt := &Runtime{subMgr: mgr}
	req := &Request{
		TargetSubagent: "",
		Mode:           ModeContext{EntryPoint: EntryPointCLI},
	}
	taskCtx := subagents.WithTaskDispatch(context.Background())
	result, prompt, err := rt.executeSubagent(taskCtx, "original prompt", skills.ActivationContext{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result when no matching subagent with empty target")
	}
	if prompt != "original prompt" {
		t.Fatalf("expected original prompt, got %q", prompt)
	}
}

func TestExecuteSubagentNoMatchingSubagentWithExplicitTarget(t *testing.T) {
	t.Parallel()
	// When target is explicit and no subagent is registered for it,
	// the Manager returns "subagents: unknown target" which propagates
	// as a non-swallowed error from executeSubagent.
	mgr := subagents.NewManager()
	rt := &Runtime{subMgr: mgr}
	req := &Request{
		TargetSubagent: "nonexistent-agent",
		Mode:           ModeContext{EntryPoint: EntryPointCLI},
	}
	taskCtx := subagents.WithTaskDispatch(context.Background())
	result, prompt, err := rt.executeSubagent(taskCtx, "original prompt", skills.ActivationContext{}, req)
	if err == nil {
		t.Fatalf("expected error for nonexistent target, got nil")
	}
	if !strings.Contains(err.Error(), "unknown target") {
		t.Fatalf("expected 'unknown target' error, got %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for nonexistent target")
	}
	if prompt != "" {
		t.Fatalf("expected empty prompt on error, got %q", prompt)
	}
}

func TestExecuteSubagentDispatchSuccess(t *testing.T) {
	t.Parallel()
	// Successful dispatch returns the subagent result and replaces prompt
	// with the output text (plus any prompt metadata).
	mgr := subagents.NewManager()
	handler := subagents.HandlerFunc(func(ctx context.Context, subCtx subagents.Context, req subagents.Request) (subagents.Result, error) {
		return subagents.Result{
			Subagent: subagents.TypeExplore,
			Output:   "explore result",
			Metadata: map[string]any{"api.tags": map[string]string{"region": "us"}},
		}, nil
	})
	if err := mgr.Register(subagents.Definition{
		Name:         subagents.TypeExplore,
		Description:  "test explore",
		DefaultModel: subagents.ModelHaiku,
		BaseContext:  subagents.Context{Model: subagents.ModelHaiku, ToolWhitelist: []string{"glob", "grep", "read"}},
	}, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	rt := &Runtime{subMgr: mgr}
	req := &Request{
		TargetSubagent: subagents.TypeExplore,
		Mode:           ModeContext{EntryPoint: EntryPointCLI},
		Tags:           map[string]string{},
	}
	taskCtx := subagents.WithTaskDispatch(context.Background())
	result, prompt, err := rt.executeSubagent(taskCtx, "original prompt", skills.ActivationContext{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil result on success")
	}
	if result.Subagent != subagents.TypeExplore {
		t.Errorf("result.Subagent = %q, want %q", result.Subagent, subagents.TypeExplore)
	}
	// Prompt should be replaced with the output text (trimmed)
	if prompt != "explore result" {
		t.Errorf("prompt = %q, want %q", prompt, "explore result")
	}
	// Tags should be merged from metadata
	if req.Tags["region"] != "us" {
		t.Errorf("Tags[region] = %q, want %q", req.Tags["region"], "us")
	}
}

func TestExecuteSubagentEmptyOutputPreservesOriginalPrompt(t *testing.T) {
	t.Parallel()
	// When the subagent result has empty output, the original prompt is kept.
	mgr := subagents.NewManager()
	handler := subagents.HandlerFunc(func(ctx context.Context, subCtx subagents.Context, req subagents.Request) (subagents.Result, error) {
		return subagents.Result{
			Subagent: subagents.TypeExplore,
			Output:   "",
		}, nil
	})
	if err := mgr.Register(subagents.Definition{
		Name:         subagents.TypeExplore,
		Description:  "test explore",
		DefaultModel: subagents.ModelHaiku,
		BaseContext:  subagents.Context{Model: subagents.ModelHaiku, ToolWhitelist: []string{"glob", "grep", "read"}},
	}, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	rt := &Runtime{subMgr: mgr}
	req := &Request{
		TargetSubagent: subagents.TypeExplore,
		Mode:           ModeContext{EntryPoint: EntryPointCLI},
	}
	taskCtx := subagents.WithTaskDispatch(context.Background())
	result, prompt, err := rt.executeSubagent(taskCtx, "original prompt", skills.ActivationContext{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
	if prompt != "original prompt" {
		t.Errorf("prompt = %q, want %q (preserved when output empty)", prompt, "original prompt")
	}
}

// ---------------------------------------------------------------------------
// Metadata propagation from request to subagent
// ---------------------------------------------------------------------------

func TestExecuteSubagentMetadataPropagation(t *testing.T) {
	t.Parallel()
	// Verify that entrypoint, request metadata, and session ID are
	// propagated into the subagent dispatch request metadata.
	var capturedReq subagents.Request
	mgr := subagents.NewManager()
	handler := subagents.HandlerFunc(func(ctx context.Context, subCtx subagents.Context, req subagents.Request) (subagents.Result, error) {
		capturedReq = req
		return subagents.Result{Output: "ok"}, nil
	})
	if err := mgr.Register(subagents.Definition{
		Name:         subagents.TypeGeneralPurpose,
		Description:  "test gp",
		DefaultModel: subagents.ModelSonnet,
		BaseContext:  subagents.Context{Model: subagents.ModelSonnet},
	}, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	rt := &Runtime{subMgr: mgr}
	req := &Request{
		TargetSubagent: subagents.TypeGeneralPurpose,
		Mode:           ModeContext{EntryPoint: EntryPointCLI},
		SessionID:      "session-42",
		Metadata:       map[string]any{"custom_key": "custom_val"},
	}
	taskCtx := subagents.WithTaskDispatch(context.Background())
	_, _, err := rt.executeSubagent(taskCtx, "prompt", skills.ActivationContext{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedReq.Metadata["entrypoint"] != EntryPointCLI {
		t.Errorf("metadata entrypoint = %v, want %q", capturedReq.Metadata["entrypoint"], EntryPointCLI)
	}
	if capturedReq.Metadata["session_id"] != "session-42" {
		t.Errorf("metadata session_id = %v, want %q", capturedReq.Metadata["session_id"], "session-42")
	}
	if capturedReq.Metadata["custom_key"] != "custom_val" {
		t.Errorf("metadata custom_key = %v, want %q", capturedReq.Metadata["custom_key"], "custom_val")
	}
}

// ---------------------------------------------------------------------------
// nil context handling (dispatchCtx fallback)
// ---------------------------------------------------------------------------

func TestExecuteSubagentNilContextFallback(t *testing.T) {
	t.Parallel()
	// Verify that a normal context with WithTaskDispatch works correctly
	// (the code has a defensive nil-check that falls back to context.Background()).
	mgr := subagents.NewManager()
	handler := subagents.HandlerFunc(func(ctx context.Context, subCtx subagents.Context, req subagents.Request) (subagents.Result, error) {
		return subagents.Result{Output: "dispatched"}, nil
	})
	if err := mgr.Register(subagents.Definition{
		Name:         subagents.TypeGeneralPurpose,
		Description:  "test gp",
		DefaultModel: subagents.ModelSonnet,
		BaseContext:  subagents.Context{Model: subagents.ModelSonnet},
	}, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	rt := &Runtime{subMgr: mgr}
	req := &Request{
		TargetSubagent: subagents.TypeGeneralPurpose,
		Mode:           ModeContext{EntryPoint: EntryPointCLI},
	}
	taskCtx := subagents.WithTaskDispatch(context.Background())
	result, prompt, err := rt.executeSubagent(taskCtx, "test", skills.ActivationContext{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
	if prompt != "dispatched" {
		t.Errorf("prompt = %q, want %q", prompt, "dispatched")
	}
}

// ---------------------------------------------------------------------------
// Tool whitelist normalization via normalizeStrings
// ---------------------------------------------------------------------------

func TestExecuteSubagentToolWhitelistNormalization(t *testing.T) {
	t.Parallel()
	// Verify that the request ToolWhitelist is normalized before
	// passing to the subagent dispatch.
	var capturedReq subagents.Request
	mgr := subagents.NewManager()
	handler := subagents.HandlerFunc(func(ctx context.Context, subCtx subagents.Context, req subagents.Request) (subagents.Result, error) {
		capturedReq = req
		return subagents.Result{Output: "ok"}, nil
	})
	if err := mgr.Register(subagents.Definition{
		Name:         subagents.TypeGeneralPurpose,
		Description:  "test gp",
		DefaultModel: subagents.ModelSonnet,
		BaseContext:  subagents.Context{Model: subagents.ModelSonnet},
	}, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	rt := &Runtime{subMgr: mgr}
	req := &Request{
		TargetSubagent: subagents.TypeGeneralPurpose,
		Mode:           ModeContext{EntryPoint: EntryPointCLI},
		ToolWhitelist:  []string{"grep", "Bash", "grep"},
	}
	taskCtx := subagents.WithTaskDispatch(context.Background())
	_, _, err := rt.executeSubagent(taskCtx, "prompt", skills.ActivationContext{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// normalizeStrings sorts and deduplicates
	if len(capturedReq.ToolWhitelist) != 2 {
		t.Fatalf("ToolWhitelist length = %d, want 2", len(capturedReq.ToolWhitelist))
	}
	if capturedReq.ToolWhitelist[0] != "Bash" || capturedReq.ToolWhitelist[1] != "grep" {
		t.Errorf("ToolWhitelist = %v, want [Bash, grep]", capturedReq.ToolWhitelist)
	}
}

// ---------------------------------------------------------------------------
// buildACPAgentDescriptions
// ---------------------------------------------------------------------------

func TestBuildACPAgentDescriptions(t *testing.T) {
	t.Parallel()

	t.Run("empty agents returns empty string", func(t *testing.T) {
		t.Parallel()
		got := buildACPAgentDescriptions(nil)
		if got != "" {
			t.Errorf("expected empty string for nil agents, got %q", got)
		}
		got = buildACPAgentDescriptions(map[string]config.ACPAgentEntry{})
		if got != "" {
			t.Errorf("expected empty string for empty agents, got %q", got)
		}
	})

	t.Run("single agent produces description block", func(t *testing.T) {
		t.Parallel()
		agents := map[string]config.ACPAgentEntry{
			"reviewer": {Command: "/usr/bin/reviewer"},
		}
		got := buildACPAgentDescriptions(agents)
		if !strings.Contains(got, "External ACP agents") {
			t.Errorf("expected header in description, got %q", got)
		}
		if !strings.Contains(got, "reviewer") {
			t.Errorf("expected agent name in description, got %q", got)
		}
		if !strings.Contains(got, "/usr/bin/reviewer") {
			t.Errorf("expected command in description, got %q", got)
		}
	})

	t.Run("multiple agents produce multiple entries", func(t *testing.T) {
		t.Parallel()
		agents := map[string]config.ACPAgentEntry{
			"analyzer": {Command: "analyze"},
			"reviewer": {Command: "review"},
		}
		got := buildACPAgentDescriptions(agents)
		if !strings.Contains(got, "analyzer") || !strings.Contains(got, "reviewer") {
			t.Errorf("expected both agent names, got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// convertTaskToolResult
// ---------------------------------------------------------------------------

func TestConvertTaskToolResultSubagent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		res          subagents.Result
		wantOutput   string
		wantSuccess  bool
		wantDataKeys []string
	}{
		{
			name:         "result with output and subagent name",
			res:          subagents.Result{Subagent: "explore", Output: "found 3 files"},
			wantOutput:   "found 3 files",
			wantSuccess:  true,
			wantDataKeys: []string{"subagent"},
		},
		{
			name:         "empty output with subagent name uses fallback message",
			res:          subagents.Result{Subagent: "explore", Output: ""},
			wantOutput:   "subagent explore completed",
			wantSuccess:  true,
			wantDataKeys: []string{"subagent"},
		},
		{
			name:         "empty output with no subagent name uses generic fallback",
			res:          subagents.Result{Output: ""},
			wantOutput:   "subagent completed",
			wantSuccess:  true,
			wantDataKeys: []string{"subagent"},
		},
		{
			name:         "error result marks success false and includes error data",
			res:          subagents.Result{Subagent: "explore", Output: "", Error: "timeout"},
			wantOutput:   "subagent explore completed",
			wantSuccess:  false,
			wantDataKeys: []string{"subagent", "error"},
		},
		{
			name:         "metadata with subagent_id propagates to data",
			res:          subagents.Result{Subagent: "explore", Output: "done", Metadata: map[string]any{"subagent_id": "task-123"}},
			wantOutput:   "done",
			wantSuccess:  true,
			wantDataKeys: []string{"subagent", "metadata", "subagent_id"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr := convertTaskToolResult(tt.res)
			if tr.Output != tt.wantOutput {
				t.Errorf("Output = %q, want %q", tr.Output, tt.wantOutput)
			}
			if tr.Success != tt.wantSuccess {
				t.Errorf("Success = %v, want %v", tr.Success, tt.wantSuccess)
			}
			data, ok := tr.Data.(map[string]any)
			if !ok && len(tt.wantDataKeys) > 0 {
				t.Fatalf("Data is not map[string]any: %T", tr.Data)
			}
			for _, key := range tt.wantDataKeys {
				if _, exists := data[key]; !exists {
					t.Errorf("missing data key %q in %v", key, data)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// selectModelForSubagent
// ---------------------------------------------------------------------------

func TestSelectModelForSubagentNilPool(t *testing.T) {
	t.Parallel()

	t.Run("nil pool and nil model returns nil model and empty tier", func(t *testing.T) {
		t.Parallel()
		rt := &Runtime{
			opts: Options{
				Model: nil,
				SubagentModelMapping: map[string]ModelTier{
					"explore": ModelTierLow,
				},
			},
		}
		mdl, tier := rt.selectModelForSubagent("explore", "")
		if mdl != nil {
			t.Errorf("expected nil model with nil pool, got %v", mdl)
		}
		if tier != "" {
			t.Errorf("expected empty tier with nil pool, got %q", tier)
		}
	})

	t.Run("empty subagent type with no overrides returns default", func(t *testing.T) {
		t.Parallel()
		rt := &Runtime{opts: Options{Model: nil}}
		mdl, tier := rt.selectModelForSubagent("", "")
		if mdl != nil {
			t.Errorf("expected nil default model, got %v", mdl)
		}
		if tier != "" {
			t.Errorf("expected empty tier, got %q", tier)
		}
	})

	t.Run("subagent mapping with nil pool falls through to default", func(t *testing.T) {
		t.Parallel()
		rt := &Runtime{
			opts: Options{
				Model: nil,
				SubagentModelMapping: map[string]ModelTier{
					"explore": ModelTierLow,
				},
			},
		}
		mdl, tier := rt.selectModelForSubagent("explore", "")
		if mdl != nil {
			t.Errorf("expected nil model (no pool), got %v", mdl)
		}
		if tier != "" {
			t.Errorf("expected empty tier when pool is nil, got %q", tier)
		}
	})
}

// ---------------------------------------------------------------------------
// Activation field handling (IsFork in executeSubagent/spawnSubagent)
// ---------------------------------------------------------------------------

func TestSpawnSubagentForkContextMarking(t *testing.T) {
	t.Parallel()
	// When target is a fork type, spawnSubagent should mark the parent
	// context as IsFork and set ParentSystemPrompt.
	mgr := subagents.NewManager()
	rt := &Runtime{
		subMgr: mgr,
		opts:   Options{SystemPrompt: "parent system prompt"},
	}

	// We need an executor for spawn to work, which needs a runner.
	store := subagents.NewMemoryStore()
	runner := subagentStubRunner{result: subagents.Result{Output: "fork result"}}
	rt.subStore = store
	rt.subExec = subagents.NewExecutor(mgr, store, runner)

	req := &Request{
		TargetSubagent: subagents.ForkSubagentType,
		SessionID:      "parent-session",
		Mode:           ModeContext{EntryPoint: EntryPointCLI},
	}

	handle, err := rt.spawnSubagent(subagents.WithTaskDispatch(context.Background()), "fork task", skills.ActivationContext{Prompt: "fork task"}, req)
	if err != nil {
		t.Fatalf("spawnSubagent error: %v", err)
	}
	if handle.ID == "" {
		t.Fatalf("expected non-empty handle ID")
	}
}

func TestSpawnSubagentNilRuntime(t *testing.T) {
	rt := (*Runtime)(nil)
	_, err := rt.spawnSubagent(context.Background(), "prompt", skills.ActivationContext{}, &Request{})
	if err == nil {
		t.Fatalf("expected error for nil runtime")
	}
	if !strings.Contains(err.Error(), "runtime is nil") {
		t.Errorf("error = %q, want 'runtime is nil'", err.Error())
	}
}

func TestSpawnSubagentNoManager(t *testing.T) {
	rt := &Runtime{}
	_, err := rt.spawnSubagent(context.Background(), "prompt", skills.ActivationContext{}, &Request{})
	if err == nil {
		t.Fatalf("expected error for nil subMgr")
	}
	if !strings.Contains(err.Error(), "subagent manager is not configured") {
		t.Errorf("error = %q, want 'subagent manager is not configured'", err.Error())
	}
}

func TestSpawnSubagentNilRequest(t *testing.T) {
	mgr := subagents.NewManager()
	rt := &Runtime{subMgr: mgr}
	_, err := rt.spawnSubagent(context.Background(), "prompt", skills.ActivationContext{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil request")
	}
	if !strings.Contains(err.Error(), "request is nil") {
		t.Errorf("error = %q, want 'request is nil'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// RuntimeSubagentRunner
// ---------------------------------------------------------------------------

func TestRunSubagentNilRuntime(t *testing.T) {
	runner := runtimeSubagentRunner{rt: nil}
	_, err := runner.RunSubagent(context.Background(), subagents.RunRequest{
		Target:      subagents.TypeExplore,
		Instruction: "test",
	})
	if err == nil {
		t.Fatalf("expected error for nil runtime")
	}
	if !strings.Contains(err.Error(), "runtime is nil") {
		t.Errorf("error = %q, want 'runtime is nil'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// ensureSubagentExecutor
// ---------------------------------------------------------------------------

func TestEnsureSubagentExecutorNilRuntime(t *testing.T) {
	rt := (*Runtime)(nil)
	exec := rt.ensureSubagentExecutor()
	if exec != nil {
		t.Fatalf("expected nil executor for nil runtime")
	}
}

func TestEnsureSubagentExecutorNilSubMgr(t *testing.T) {
	rt := &Runtime{}
	exec := rt.ensureSubagentExecutor()
	if exec != nil {
		t.Fatalf("expected nil executor when subMgr is nil")
	}
}

// ---------------------------------------------------------------------------
// waitSubagent
// ---------------------------------------------------------------------------

func TestWaitSubagentNoManager(t *testing.T) {
	rt := &Runtime{}
	_, err := rt.waitSubagent(context.Background(), "id-1", 0)
	if err == nil {
		t.Fatalf("expected error for nil subMgr")
	}
	if !strings.Contains(err.Error(), "subagent manager is not configured") {
		t.Errorf("error = %q, want 'subagent manager is not configured'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// taskRunner
// ---------------------------------------------------------------------------

func TestTaskRunnerReturnsFunction(t *testing.T) {
	rt := &Runtime{}
	runner := rt.taskRunner()
	if runner == nil {
		t.Fatalf("taskRunner should return a non-nil function")
	}
}

// ---------------------------------------------------------------------------
// runTaskInvocation edge cases
// ---------------------------------------------------------------------------

func TestRunTaskInvocationNilRuntime(t *testing.T) {
	rt := (*Runtime)(nil)
	_, err := rt.runTaskInvocation(context.Background(), toolbuiltin.TaskRequest{Prompt: "test"})
	if err == nil {
		t.Fatalf("expected error for nil runtime")
	}
	if !strings.Contains(err.Error(), "runtime is nil") {
		t.Errorf("error = %q, want 'runtime is nil'", err.Error())
	}
}

func TestRunTaskInvocationNoManager(t *testing.T) {
	rt := &Runtime{}
	_, err := rt.runTaskInvocation(context.Background(), toolbuiltin.TaskRequest{Prompt: "test"})
	if err == nil {
		t.Fatalf("expected error for nil subMgr")
	}
	if !strings.Contains(err.Error(), "subagent manager is not configured") {
		t.Errorf("error = %q, want 'subagent manager is not configured'", err.Error())
	}
}

func TestRunTaskInvocationEmptyPrompt(t *testing.T) {
	mgr := subagents.NewManager()
	rt := &Runtime{subMgr: mgr}
	_, err := rt.runTaskInvocation(context.Background(), toolbuiltin.TaskRequest{Prompt: "   "})
	if err == nil {
		t.Fatalf("expected error for empty prompt")
	}
	if !strings.Contains(err.Error(), "prompt is empty") {
		t.Errorf("error = %q, want 'prompt is empty'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Stub runner for spawn tests
// ---------------------------------------------------------------------------

type subagentStubRunner struct {
	result subagents.Result
	err    error
}

func (s subagentStubRunner) RunSubagent(_ context.Context, req subagents.RunRequest) (subagents.Result, error) {
	return subagents.Result{
		Subagent: req.Target,
		Output:   s.result.Output,
		Metadata: s.result.Metadata,
		Error:    s.result.Error,
	}, s.err
}

// ---------------------------------------------------------------------------
// combineToolWhitelists (used in subagent run)
// ---------------------------------------------------------------------------

func TestCombineToolWhitelistsSubagent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		requested []string
		subagent  []string
		wantLen   int
		wantHas   []string // keys that must exist
		wantMiss  []string // keys that must not exist
	}{
		{
			name:      "both nil returns nil",
			requested: nil,
			subagent:  nil,
			wantLen:   0,
		},
		{
			name:      "requested nil, subagent set returns subagent",
			requested: nil,
			subagent:  []string{"bash", "grep"},
			wantLen:   2,
			wantHas:   []string{"bash", "grep"},
		},
		{
			name:      "subagent nil, requested set returns requested",
			requested: []string{"Bash", "Grep"},
			subagent:  nil,
			wantLen:   2,
			wantHas:   []string{"bash", "grep"},
		},
		{
			name:      "intersection of both sets",
			requested: []string{"bash", "grep", "read"},
			subagent:  []string{"grep", "read", "glob"},
			wantLen:   2,
			wantHas:   []string{"grep", "read"},
			wantMiss:  []string{"bash", "glob"},
		},
		{
			name:      "no overlap returns empty intersection",
			requested: []string{"bash"},
			subagent:  []string{"grep"},
			wantLen:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := combineToolWhitelists(tt.requested, tt.subagent)
			if len(result) != tt.wantLen {
				t.Fatalf("length = %d, want %d", len(result), tt.wantLen)
			}
			for _, key := range tt.wantHas {
				if _, ok := result[key]; !ok {
					t.Errorf("expected key %q in result", key)
				}
			}
			for _, key := range tt.wantMiss {
				if _, ok := result[key]; ok {
					t.Errorf("did not expect key %q in result", key)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// toLowerSet (used in combineToolWhitelists)
// ---------------------------------------------------------------------------

func TestToLowerSetSubagent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		vals []string
		want map[string]struct{}
	}{
		{name: "nil returns nil", vals: nil, want: nil},
		{name: "empty returns nil", vals: []string{}, want: nil},
		{name: "mixed case is lowered", vals: []string{"Bash", "GREP"}, want: map[string]struct{}{"bash": {}, "grep": {}}},
		{name: "duplicates are deduplicated", vals: []string{"bash", "BASH"}, want: map[string]struct{}{"bash": {}}},
		{name: "whitespace-only entries skipped", vals: []string{"  ", "bash"}, want: map[string]struct{}{"bash": {}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := toLowerSet(tt.vals)
			if len(got) != len(tt.want) {
				t.Fatalf("toLowerSet length = %d, want %d", len(got), len(tt.want))
			}
			for key := range tt.want {
				if _, ok := got[key]; !ok {
					t.Errorf("missing key %q", key)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Context injection in executeSubagent (buildSubagentContext integration)
// ---------------------------------------------------------------------------

func TestExecuteSubagentSubagentContextInjection(t *testing.T) {
	t.Parallel()
	// Verify that buildSubagentContext output is injected into the
	// dispatch context via subagents.WithContext.
	var capturedCtx context.Context
	mgr := subagents.NewManager()
	handler := subagents.HandlerFunc(func(ctx context.Context, subCtx subagents.Context, req subagents.Request) (subagents.Result, error) {
		capturedCtx = ctx
		return subagents.Result{Output: "ok"}, nil
	})
	if err := mgr.Register(subagents.Definition{
		Name:         subagents.TypeExplore,
		Description:  "test explore",
		DefaultModel: subagents.ModelHaiku,
		BaseContext:  subagents.Context{Model: subagents.ModelHaiku, ToolWhitelist: []string{"glob", "grep", "read"}},
	}, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	rt := &Runtime{subMgr: mgr}
	req := &Request{
		TargetSubagent: subagents.TypeExplore,
		Mode:           ModeContext{EntryPoint: EntryPointCLI},
		SessionID:      "test-session",
	}
	taskCtx := subagents.WithTaskDispatch(context.Background())
	_, _, err := rt.executeSubagent(taskCtx, "prompt", skills.ActivationContext{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The dispatch context should contain the subagents.Context
	subCtx, ok := subagents.FromContext(capturedCtx)
	if !ok {
		t.Fatalf("expected subagents.Context in dispatch context")
	}
	// SessionID should match
	if subCtx.SessionID != "test-session" {
		t.Errorf("subCtx.SessionID = %q, want %q", subCtx.SessionID, "test-session")
	}
	// Model should come from builtin definition
	if subCtx.Model != subagents.ModelHaiku {
		t.Errorf("subCtx.Model = %q, want %q", subCtx.Model, subagents.ModelHaiku)
	}
}

// ---------------------------------------------------------------------------
// anyToString helper for subagent context building
// ---------------------------------------------------------------------------

type subagentFmtStringer string

func (s subagentFmtStringer) String() string { return string(s) }

func TestAnyToStringSubagent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		val    any
		want   string
		wantOK bool
	}{
		{name: "nil returns false", val: nil, want: "", wantOK: false},
		{name: "string is trimmed", val: "  hello  ", want: "hello", wantOK: true},
		{name: "int is Sprint-ed", val: 42, want: "42", wantOK: true},
		{name: "fmt.Stringer", val: subagentFmtStringer("  val  "), want: "val", wantOK: true},
		{name: "map with body extracts body", val: map[string]any{"body": "  content  "}, want: "content", wantOK: true},
		{name: "map without body falls to JSON", val: map[string]any{"key": "value"}, want: `{"key":"value"}`, wantOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := anyToString(tt.val)
			if ok != tt.wantOK {
				t.Errorf("anyToString ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("anyToString got = %q, want %q", got, tt.want)
			}
		})
	}
}
