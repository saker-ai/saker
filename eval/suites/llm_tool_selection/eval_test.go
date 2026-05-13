//go:build integration

package llm_tool_selection_eval

import (
	"context"
	"strings"
	"testing"

	"github.com/cinience/saker/eval"
	evalhelpers "github.com/cinience/saker/eval"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/testutil"
)

// ToolSelectionCase defines a prompt and the expected tool the LLM should select.
type ToolSelectionCase struct {
	Name           string
	Prompt         string
	ExpectedTool   string
	AcceptedTools  []string          // alternative acceptable tools (model exploration)
	ExpectedParams map[string]string // param name → expected substring
}

func cases() []ToolSelectionCase {
	return []ToolSelectionCase{
		{
			Name:         "list_directory",
			Prompt:       "列出当前目录的所有文件",
			ExpectedTool: "bash",
			ExpectedParams: map[string]string{
				"command": "ls",
			},
		},
		{
			Name:          "run_tests",
			Prompt:        "运行 Go 测试",
			ExpectedTool:  "bash",
			AcceptedTools: []string{"glob"}, // model may search for test files first
			ExpectedParams: map[string]string{
				"command": "go test",
			},
		},
		{
			Name:         "read_file",
			Prompt:       "读取 main.go 文件内容",
			ExpectedTool: "read",
			ExpectedParams: map[string]string{
				"file_path": "main.go",
			},
		},
		{
			Name:         "search_pattern",
			Prompt:       "在代码中搜索所有包含 TODO 的地方",
			ExpectedTool: "grep",
			ExpectedParams: map[string]string{
				"pattern": "TODO",
			},
		},
		{
			Name:         "find_go_files",
			Prompt:       "找到所有 .go 结尾的文件",
			ExpectedTool: "glob",
			ExpectedParams: map[string]string{
				"pattern": ".go",
			},
		},
		{
			Name:         "write_file",
			Prompt:       "创建一个 hello.txt 文件，内容写 Hello World",
			ExpectedTool: "write",
		},
		{
			Name:          "edit_file",
			Prompt:        "把 main.go 中的 fmt.Println 替换为 log.Println",
			ExpectedTool:  "edit",
			AcceptedTools: []string{"read"}, // model may read the file first
		},
		{
			Name:         "git_status",
			Prompt:       "查看当前 git 状态",
			ExpectedTool: "bash",
			ExpectedParams: map[string]string{
				"command": "git",
			},
		},
	}
}

// toolDefinitions returns the tool schemas passed to the model so it can
// select the right tool without actually executing anything.
func toolDefinitions() []model.ToolDefinition {
	return []model.ToolDefinition{
		{
			Name:        "bash",
			Description: "Execute a shell command",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "The command to execute"},
				},
				"required": []string{"command"},
			},
		},
		{
			Name:        "read",
			Description: "Read a file from the filesystem",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string", "description": "The file path to read"},
				},
				"required": []string{"file_path"},
			},
		},
		{
			Name:        "write",
			Description: "Write content to a file",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string", "description": "The file path to write"},
					"content":   map[string]any{"type": "string", "description": "The content to write"},
				},
				"required": []string{"file_path", "content"},
			},
		},
		{
			Name:        "edit",
			Description: "Edit an existing file by replacing text",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path":  map[string]any{"type": "string", "description": "The file to edit"},
					"old_string": map[string]any{"type": "string", "description": "Text to replace"},
					"new_string": map[string]any{"type": "string", "description": "Replacement text"},
				},
				"required": []string{"file_path", "old_string", "new_string"},
			},
		},
		{
			Name:        "grep",
			Description: "Search file contents using regex patterns",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{"type": "string", "description": "The regex pattern to search for"},
				},
				"required": []string{"pattern"},
			},
		},
		{
			Name:        "glob",
			Description: "Find files matching a glob pattern",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{"type": "string", "description": "The glob pattern to match"},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

func TestEval_LLMToolSelection(t *testing.T) {
	testutil.RequireIntegration(t)

	suite := &eval.EvalSuite{Name: "llm_tool_selection"}
	mdl := evalhelpers.NewLLMModel(t, "")

	for _, tc := range cases() {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			// Call the model directly with tool definitions — no agent loop,
			// so the response contains the raw tool call without execution.
			resp, err := mdl.Complete(context.Background(), model.Request{
				System: "You are a coding assistant. When the user asks you to perform a task, select the most appropriate tool. Always use a tool call, never respond with plain text.",
				Messages: []model.Message{
					{Role: "user", Content: tc.Prompt},
				},
				Tools:     toolDefinitions(),
				MaxTokens: 1024,
			})
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}

			// Check if the model selected the expected tool.
			toolMatch := false
			paramMatch := true
			gotTool := "(no tool call)"
			gotParams := map[string]any{}

			if len(resp.Message.ToolCalls) > 0 {
				call := resp.Message.ToolCalls[0]
				gotTool = call.Name
				gotParams = call.Arguments
				toolMatch = call.Name == tc.ExpectedTool
				if !toolMatch {
					for _, alt := range tc.AcceptedTools {
						if call.Name == alt {
							toolMatch = true
							break
						}
					}
				}
			}

			// Check expected params (only when the primary tool matched).
			if gotTool == tc.ExpectedTool {
				for key, expectedSubstr := range tc.ExpectedParams {
					val, ok := gotParams[key]
					if !ok {
						paramMatch = false
						continue
					}
					valStr, _ := val.(string)
					if !strings.Contains(valStr, expectedSubstr) {
						paramMatch = false
					}
				}
			}

			pass := toolMatch && paramMatch
			score := 0.0
			if toolMatch {
				score += 0.5
			}
			if paramMatch {
				score += 0.5
			}

			suite.Add(eval.EvalResult{
				Name:     tc.Name,
				Pass:     pass,
				Score:    score,
				Expected: tc.ExpectedTool,
				Got:      gotTool,
				Details: map[string]any{
					"tool_match":  toolMatch,
					"param_match": paramMatch,
				},
			})

			if !pass {
				// Log but don't fail individual cases — LLM evals are non-deterministic.
				// The overall pass rate threshold determines suite success.
				t.Logf("tool selection %q: want %s, got %s (tool=%v, param=%v)",
					tc.Name, tc.ExpectedTool, gotTool, toolMatch, paramMatch)
			}
		})
	}

	t.Cleanup(func() {
		t.Logf("\n%s", suite.Summary())
		rate := suite.PassRate()
		if rate < 0.75 {
			t.Errorf("tool selection pass rate %.1f%% below 75%% threshold", rate*100)
		}
	})
}
