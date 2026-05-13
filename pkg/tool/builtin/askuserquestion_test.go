package toolbuiltin

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// withMockAskFn returns a context carrying an AskQuestionFunc that returns
// the provided canned answers. Used by tests to simulate a wired interactive UI.
func withMockAskFn(ctx context.Context, answers map[string]string) context.Context {
	return WithAskQuestionFunc(ctx, func(_ context.Context, qs []Question) (map[string]string, error) {
		out := make(map[string]string, len(qs))
		for _, q := range qs {
			if v, ok := answers[q.Question]; ok {
				out[q.Question] = v
			}
		}
		return out, nil
	})
}

func TestAskUserQuestionSingleQuestionSingleSelect(t *testing.T) {
	tool := NewAskUserQuestionTool()
	params := map[string]interface{}{
		"questions": []interface{}{
			map[string]interface{}{
				"question": "Which database should we use?",
				"header":   "DB",
				"options": []interface{}{
					map[string]interface{}{"label": "Postgres", "description": "Use PostgreSQL for production"},
					map[string]interface{}{"label": "SQLite", "description": "Use SQLite for simplicity"},
				},
				"multiSelect": false,
			},
		},
	}

	ctx := withMockAskFn(context.Background(), map[string]string{
		"Which database should we use?": "Postgres",
	})
	res, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res == nil || !res.Success {
		t.Fatalf("expected success result")
	}
	if !strings.Contains(res.Output, "Postgres") {
		t.Fatalf("expected answer in output, got %q", res.Output)
	}
	data, ok := res.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	qs, ok := data["questions"].([]Question)
	if !ok {
		t.Fatalf("unexpected questions type %T", data["questions"])
	}
	if len(qs) != 1 {
		t.Fatalf("expected 1 question, got %d", len(qs))
	}
	if qs[0].Header != "DB" || qs[0].MultiSelect {
		t.Fatalf("unexpected question: %+v", qs[0])
	}
	if len(qs[0].Options) != 2 || qs[0].Options[0].Label != "Postgres" {
		t.Fatalf("unexpected options: %+v", qs[0].Options)
	}
	answers, ok := data["answers"].(map[string]string)
	if !ok {
		t.Fatalf("unexpected answers type %T", data["answers"])
	}
	if answers["Which database should we use?"] != "Postgres" {
		t.Fatalf("unexpected answers: %+v", answers)
	}
}

func TestAskUserQuestionMultipleQuestions(t *testing.T) {
	tool := NewAskUserQuestionTool()
	params := map[string]interface{}{
		"questions": []interface{}{
			map[string]interface{}{
				"question": "Choose output format?",
				"header":   "Fmt",
				"options": []interface{}{
					map[string]interface{}{"label": "JSON", "description": "Machine readable output"},
					map[string]interface{}{"label": "Text", "description": "Human friendly output"},
				},
				"multiSelect": false,
			},
			map[string]interface{}{
				"question": "Enable caching?",
				"header":   "Cache",
				"options": []interface{}{
					map[string]interface{}{"label": "Yes", "description": "Cache results"},
					map[string]interface{}{"label": "No", "description": "Always recompute"},
				},
				"multiSelect": false,
			},
			map[string]interface{}{
				"question": "Pick deployment target?",
				"header":   "Deploy",
				"options": []interface{}{
					map[string]interface{}{"label": "Staging", "description": "Deploy to staging"},
					map[string]interface{}{"label": "Prod", "description": "Deploy to production"},
				},
				"multiSelect": false,
			},
		},
	}

	ctx := withMockAskFn(context.Background(), map[string]string{
		"Choose output format?":    "JSON",
		"Enable caching?":          "Yes",
		"Pick deployment target?":  "Staging",
	})
	res, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	data := res.Data.(map[string]interface{})
	qs := data["questions"].([]Question)
	if len(qs) != 3 {
		t.Fatalf("expected 3 questions, got %d", len(qs))
	}
	if !strings.Contains(res.Output, "JSON") || !strings.Contains(res.Output, "Yes") || !strings.Contains(res.Output, "Staging") {
		t.Fatalf("expected all answers in output, got %q", res.Output)
	}
}

func TestAskUserQuestionMultiSelect(t *testing.T) {
	tool := NewAskUserQuestionTool()
	params := map[string]interface{}{
		"questions": []interface{}{
			map[string]interface{}{
				"question": "Which platforms should we support?",
				"header":   "OS",
				"options": []interface{}{
					map[string]interface{}{"label": "Linux", "description": "Support Linux"},
					map[string]interface{}{"label": "macOS", "description": "Support macOS"},
					map[string]interface{}{"label": "Windows", "description": "Support Windows"},
				},
				"multiSelect": true,
			},
		},
	}

	ctx := withMockAskFn(context.Background(), map[string]string{
		"Which platforms should we support?": "Linux,macOS",
	})
	res, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(res.Output, "Linux,macOS") {
		t.Fatalf("expected multi-select answer in output, got %q", res.Output)
	}
	data := res.Data.(map[string]interface{})
	qs := data["questions"].([]Question)
	if !qs[0].MultiSelect {
		t.Fatalf("expected MultiSelect true")
	}
}

// TestAskUserQuestionNoAskFnReturnsFailure verifies the guard against the old
// "succeeds with the question text" hallucination bug: when no askFn is wired
// in the context (e.g. legacy REPL or headless mode), Execute must return
// Success: false with a clear "not available" message so the LLM cannot
// confuse the question echo for a user answer.
func TestAskUserQuestionNoAskFnReturnsFailure(t *testing.T) {
	tool := NewAskUserQuestionTool()
	params := map[string]interface{}{
		"questions": []interface{}{
			map[string]interface{}{
				"question": "Pick one?",
				"header":   "P",
				"options": []interface{}{
					map[string]interface{}{"label": "A", "description": "a"},
					map[string]interface{}{"label": "B", "description": "b"},
				},
				"multiSelect": false,
			},
		},
	}
	res, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil result")
	}
	if res.Success {
		t.Fatalf("expected Success=false when askFn missing, got Success=true (hallucination guard regressed)")
	}
	if !strings.Contains(res.Output, "not available") {
		t.Fatalf("expected 'not available' in output, got %q", res.Output)
	}
	if !strings.Contains(res.Output, "Do not assume") {
		t.Fatalf("expected explicit 'Do not assume' guidance in output, got %q", res.Output)
	}
	data, ok := res.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected Data map even on failure (preserves observability), got %T", res.Data)
	}
	if qs, _ := data["questions"].([]Question); len(qs) != 1 {
		t.Fatalf("expected questions to be preserved in failure Data")
	}
	if _, ok := data["answers"]; ok {
		t.Fatalf("must not include answers on failure")
	}
}

// TestAskUserQuestionAskFnError verifies that a propagated askFn error becomes
// a tool-level error (not a silent success).
func TestAskUserQuestionAskFnError(t *testing.T) {
	tool := NewAskUserQuestionTool()
	params := map[string]interface{}{
		"questions": []interface{}{
			map[string]interface{}{
				"question":    "Pick?",
				"header":      "P",
				"options":     []interface{}{map[string]interface{}{"label": "A", "description": "a"}, map[string]interface{}{"label": "B", "description": "b"}},
				"multiSelect": false,
			},
		},
	}
	wantErr := fmt.Errorf("user cancelled")
	ctx := WithAskQuestionFunc(context.Background(), func(_ context.Context, _ []Question) (map[string]string, error) {
		return nil, wantErr
	})
	_, err := tool.Execute(ctx, params)
	if err == nil {
		t.Fatalf("expected error from Execute, got nil")
	}
	if !strings.Contains(err.Error(), "user cancelled") {
		t.Fatalf("expected wrapped askFn error, got %v", err)
	}
}

func TestAskUserQuestionAcceptsTypedArraysAndAnswers(t *testing.T) {
	tool := NewAskUserQuestionTool()

	t.Run("answers map[string]interface{}", func(t *testing.T) {
		params := map[string]interface{}{
			"questions": []map[string]interface{}{
				{
					"question": "Pick one?",
					"header":   "Pick",
					"options": []map[string]interface{}{
						{"label": "A", "description": "Option A"},
						{"label": "B", "description": "Option B"},
					},
					"multiSelect": false,
				},
			},
			"answers": map[string]interface{}{"Pick": "A"},
		}
		res, err := tool.Execute(context.Background(), params)
		if err != nil {
			t.Fatalf("Execute returned error: %v", err)
		}
		data := res.Data.(map[string]interface{})
		answers, ok := data["answers"].(map[string]string)
		if !ok {
			t.Fatalf("unexpected answers type %T", data["answers"])
		}
		if answers["Pick"] != "A" {
			t.Fatalf("unexpected answers: %+v", answers)
		}
	})

	t.Run("answers map[string]string", func(t *testing.T) {
		params := map[string]interface{}{
			"questions": []interface{}{
				map[string]interface{}{
					"question": "Confirm?",
					"header":   "OK",
					"options": []interface{}{
						map[string]interface{}{"label": "Yes", "description": "Proceed"},
						map[string]interface{}{"label": "No", "description": "Stop"},
					},
					"multiSelect": false,
				},
			},
			"answers": map[string]string{"OK": "Yes"},
		}
		res, err := tool.Execute(context.Background(), params)
		if err != nil {
			t.Fatalf("Execute returned error: %v", err)
		}
		data := res.Data.(map[string]interface{})
		if _, ok := data["answers"].(map[string]string); !ok {
			t.Fatalf("unexpected answers type %T", data["answers"])
		}
	})
}

func TestAskUserQuestionConcurrentExecutions(t *testing.T) {
	tool := NewAskUserQuestionTool()

	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			params := map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question": fmt.Sprintf("Worker %d ok?", i),
						"header":   fmt.Sprintf("W%d", i),
						"options": []interface{}{
							map[string]interface{}{"label": "Yes", "description": "Proceed"},
							map[string]interface{}{"label": "No", "description": "Stop"},
						},
						"multiSelect": false,
					},
				},
			}
			if _, err := tool.Execute(context.Background(), params); err != nil {
				t.Errorf("worker %d execute error: %v", i, err)
			}
		}()
	}
	wg.Wait()
}

func TestAskUserQuestionMetadata(t *testing.T) {
	tool := NewAskUserQuestionTool()
	if tool.Name() != "ask_user_question" {
		t.Fatalf("unexpected name %q", tool.Name())
	}
	if tool.Description() != askUserQuestionDescription {
		t.Fatalf("unexpected description")
	}
	schema := tool.Schema()
	if schema == nil || schema.Type != "object" {
		t.Fatalf("unexpected schema: %#v", schema)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "questions" {
		t.Fatalf("unexpected schema required: %#v", schema.Required)
	}
	if _, ok := schema.Properties["questions"]; !ok {
		t.Fatalf("schema missing questions property")
	}
}

func TestAskUserQuestionErrors(t *testing.T) {
	tool := NewAskUserQuestionTool()

	cases := []struct {
		name   string
		ctx    context.Context
		params map[string]interface{}
		want   string
	}{
		{name: "nil context", ctx: nil, params: map[string]interface{}{}, want: "context is nil"},
		{name: "nil params", ctx: context.Background(), params: nil, want: "params is nil"},
		{name: "missing questions", ctx: context.Background(), params: map[string]interface{}{}, want: "questions is required"},
		{name: "questions not array", ctx: context.Background(), params: map[string]interface{}{"questions": "oops"}, want: "questions must be an array"},
		{name: "question entry not object", ctx: context.Background(), params: map[string]interface{}{"questions": []interface{}{"bad"}}, want: "questions[0] must be object"},
		{
			name: "question missing question field",
			ctx:  context.Background(),
			params: map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"header": "H",
						"options": []interface{}{
							map[string]interface{}{"label": "A", "description": "a"},
							map[string]interface{}{"label": "B", "description": "b"},
						},
						"multiSelect": false,
					},
				},
			},
			want: "questions[0].question",
		},
		{
			name: "question missing header field",
			ctx:  context.Background(),
			params: map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question": "Pick one?",
						"options": []interface{}{
							map[string]interface{}{"label": "A", "description": "a"},
							map[string]interface{}{"label": "B", "description": "b"},
						},
						"multiSelect": false,
					},
				},
			},
			want: "questions[0].header",
		},
		{
			name: "options missing",
			ctx:  context.Background(),
			params: map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question":    "Pick one?",
						"header":      "H",
						"multiSelect": false,
					},
				},
			},
			want: "questions[0].options",
		},
		{
			name: "options not array",
			ctx:  context.Background(),
			params: map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question":    "Pick one?",
						"header":      "H",
						"options":     "nope",
						"multiSelect": false,
					},
				},
			},
			want: "options must be an array",
		},
		{
			name: "option entry not object",
			ctx:  context.Background(),
			params: map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question":    "Pick one?",
						"header":      "H",
						"options":     []interface{}{"bad", map[string]interface{}{"label": "B", "description": "b"}},
						"multiSelect": false,
					},
				},
			},
			want: "options[0] must be object",
		},
		{
			name: "option missing label",
			ctx:  context.Background(),
			params: map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question": "Pick one?",
						"header":   "H",
						"options": []interface{}{
							map[string]interface{}{"description": "a"},
							map[string]interface{}{"label": "B", "description": "b"},
						},
						"multiSelect": false,
					},
				},
			},
			want: "options[0].label",
		},
		{
			name: "option empty label",
			ctx:  context.Background(),
			params: map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question": "Pick one?",
						"header":   "H",
						"options": []interface{}{
							map[string]interface{}{"label": "   ", "description": "a"},
							map[string]interface{}{"label": "B", "description": "b"},
						},
						"multiSelect": false,
					},
				},
			},
			want: "cannot be empty",
		},
		{
			name: "option missing description",
			ctx:  context.Background(),
			params: map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question": "Pick one?",
						"header":   "H",
						"options": []interface{}{
							map[string]interface{}{"label": "A"},
							map[string]interface{}{"label": "B", "description": "b"},
						},
						"multiSelect": false,
					},
				},
			},
			want: "options[0].description",
		},
		{
			name: "multiSelect missing",
			ctx:  context.Background(),
			params: map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question": "Pick one?",
						"header":   "H",
						"options": []interface{}{
							map[string]interface{}{"label": "A", "description": "a"},
							map[string]interface{}{"label": "B", "description": "b"},
						},
					},
				},
			},
			want: "multiSelect: field is required",
		},
		{
			name: "multiSelect wrong type",
			ctx:  context.Background(),
			params: map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question": "Pick one?",
						"header":   "H",
						"options": []interface{}{
							map[string]interface{}{"label": "A", "description": "a"},
							map[string]interface{}{"label": "B", "description": "b"},
						},
						"multiSelect": "false",
					},
				},
			},
			want: "multiSelect must be boolean",
		},
		{
			name: "answers non-string value",
			ctx:  context.Background(),
			params: map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question": "Pick one?",
						"header":   "H",
						"options": []interface{}{
							map[string]interface{}{"label": "A", "description": "a"},
							map[string]interface{}{"label": "B", "description": "b"},
						},
						"multiSelect": false,
					},
				},
				"answers": map[string]interface{}{
					"H": map[string]interface{}{"nested": true},
				},
			},
			want: "answers[\"H\"] must be string",
		},
		{name: "answers wrong type", ctx: context.Background(), params: map[string]interface{}{"questions": []interface{}{map[string]interface{}{
			"question": "Pick one?",
			"header":   "H",
			"options": []interface{}{
				map[string]interface{}{"label": "A", "description": "a"},
				map[string]interface{}{"label": "B", "description": "b"},
			},
			"multiSelect": false,
		}}, "answers": "oops"}, want: "answers must be object"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Execute(tc.ctx, tc.params)
			if err == nil {
				t.Fatalf("expected error")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error mismatch: want %q got %v", tc.want, err)
			}
		})
	}
}
