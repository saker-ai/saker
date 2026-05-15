//go:build integration

package llm_multi_turn_eval

import (
	"context"
	"strings"
	"testing"

	"github.com/saker-ai/saker/eval"
	evalhelpers "github.com/saker-ai/saker/eval"
	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/testutil"
)

type multiTurnCase struct {
	Name      string
	SessionID string
	Turns     []turn
}

type turn struct {
	Prompt          string
	ExpectedContain string // the model response should contain this
}

func cases() []multiTurnCase {
	return []multiTurnCase{
		{
			Name:      "remember_project_name",
			SessionID: "llm-mt-project",
			Turns: []turn{
				{Prompt: "我正在开发一个叫 Saker 的 Go SDK 项目", ExpectedContain: ""},
				{Prompt: "这个项目叫什么名字？", ExpectedContain: "Saker"},
			},
		},
		{
			Name:      "remember_language",
			SessionID: "llm-mt-lang",
			Turns: []turn{
				{Prompt: "我们团队用 Rust 语言", ExpectedContain: ""},
				{Prompt: "我们用的什么编程语言？", ExpectedContain: "Rust"},
			},
		},
		{
			Name:      "follow_up_on_task",
			SessionID: "llm-mt-task",
			Turns: []turn{
				{Prompt: "我需要修复用户登录时的 token 过期问题", ExpectedContain: ""},
				{Prompt: "刚才说的那个 bug 是关于什么的？", ExpectedContain: "token"},
			},
		},
	}
}

func TestEval_LLMMultiTurnContextRetention(t *testing.T) {
	testutil.RequireIntegration(t)
	testutil.RequireIntegration(t)

	suite := &eval.EvalSuite{Name: "llm_multi_turn"}
	rt := evalhelpers.NewLLMRuntime(t, "",
		evalhelpers.WithSystemPrompt("You are a helpful coding assistant. Always respond concisely."),
	)

	for _, tc := range cases() {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			allPass := true
			for i, turn := range tc.Turns {
				resp, err := rt.Run(context.Background(), api.Request{
					Prompt:    turn.Prompt,
					SessionID: tc.SessionID,
				})
				if err != nil {
					t.Fatalf("turn %d: %v", i, err)
				}
				if resp == nil || resp.Result == nil {
					t.Fatalf("turn %d: nil response", i)
				}

				if turn.ExpectedContain != "" {
					if !strings.Contains(strings.ToLower(resp.Result.Output), strings.ToLower(turn.ExpectedContain)) {
						t.Logf("turn %d: expected %q in response, got %q",
							i, turn.ExpectedContain, truncate(resp.Result.Output, 300))
						allPass = false
					}
				}
			}

			score := 1.0
			if !allPass {
				score = 0.0
			}
			suite.Add(eval.EvalResult{
				Name:  tc.Name,
				Pass:  allPass,
				Score: score,
				Details: map[string]any{
					"turns": len(tc.Turns),
				},
			})
		})
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
