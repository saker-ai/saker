//go:build integration

package llm_system_prompt_eval

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/saker-ai/saker/eval"
	evalhelpers "github.com/saker-ai/saker/eval"
	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/testutil"
)

func TestEval_LLMSystemPromptAdherence_JSONFormat(t *testing.T) {
	testutil.RequireIntegration(t)

	suite := &eval.EvalSuite{Name: "llm_system_prompt_json"}

	// Use model directly to avoid runtime system prompt (~18k tokens) diluting
	// the custom JSON instruction.
	mdl := evalhelpers.NewLLMModel(t, "")

	prompts := []struct {
		name   string
		prompt string
	}{
		{"simple_greeting", "Say hello"},
		{"list_colors", "List three primary colors"},
		{"math_answer", "What is 2+2?"},
	}

	for _, tc := range prompts {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp, err := mdl.Complete(context.Background(), model.Request{
				System: "You are a JSON-only assistant. Every response MUST be valid JSON. Do not include any text before or after the JSON. Do not use markdown code blocks.",
				Messages: []model.Message{
					{Role: "user", Content: tc.prompt},
				},
				MaxTokens: 1024,
			})
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}

			output := strings.TrimSpace(resp.Message.TextContent())
			// Strip markdown code blocks if present.
			output = stripCodeBlock(output)
			var js json.RawMessage
			pass := json.Unmarshal([]byte(output), &js) == nil

			score := 0.0
			if pass {
				score = 1.0
			}
			suite.Add(eval.EvalResult{
				Name:     tc.name,
				Pass:     pass,
				Score:    score,
				Expected: "valid JSON",
				Got:      truncate(output, 200),
			})
			if !pass {
				t.Logf("response is not valid JSON: %s", truncate(output, 200))
			}
		})
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func TestEval_LLMSystemPromptAdherence_Prefix(t *testing.T) {
	testutil.RequireIntegration(t)

	suite := &eval.EvalSuite{Name: "llm_system_prompt_prefix"}

	mdl := evalhelpers.NewLLMModel(t, "")

	prompts := []struct {
		name   string
		prompt string
	}{
		{"greeting", "Say hi"},
		{"question", "What is Go?"},
		{"instruction", "List two fruits"},
	}

	for _, tc := range prompts {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp, err := mdl.Complete(context.Background(), model.Request{
				System: "You must always start every response with exactly 'ROGER: ' followed by your answer. No exceptions.",
				Messages: []model.Message{
					{Role: "user", Content: tc.prompt},
				},
				MaxTokens: 1024,
			})
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}

			output := strings.TrimSpace(resp.Message.TextContent())
			pass := strings.HasPrefix(output, "ROGER: ") || strings.HasPrefix(output, "ROGER:")

			score := 0.0
			if pass {
				score = 1.0
			}
			suite.Add(eval.EvalResult{
				Name:     tc.name,
				Pass:     pass,
				Score:    score,
				Expected: "ROGER: ...",
				Got:      truncate(output, 200),
			})
			if !pass {
				t.Logf("response does not start with 'ROGER: ': %s", truncate(output, 100))
			}
		})
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func TestEval_LLMSystemPromptAdherence_Language(t *testing.T) {
	testutil.RequireIntegration(t)

	suite := &eval.EvalSuite{Name: "llm_system_prompt_language"}

	mdl := evalhelpers.NewLLMModel(t, "")

	prompts := []struct {
		name   string
		prompt string
	}{
		{"english_input", "What is machine learning?"},
		{"chinese_input", "什么是人工智能？"},
	}

	for _, tc := range prompts {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp, err := mdl.Complete(context.Background(), model.Request{
				System: "你是一个中文助手。所有回复必须使用中文。不要使用英文。",
				Messages: []model.Message{
					{Role: "user", Content: tc.prompt},
				},
				MaxTokens: 1024,
			})
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}

			output := resp.Message.TextContent()
			// Check that the response contains Chinese characters.
			hasChinese := false
			for _, r := range output {
				if r >= 0x4E00 && r <= 0x9FFF {
					hasChinese = true
					break
				}
			}

			pass := hasChinese
			score := 0.0
			if pass {
				score = 1.0
			}
			suite.Add(eval.EvalResult{
				Name:  tc.name,
				Pass:  pass,
				Score: score,
				Got:   truncate(output, 200),
			})
			if !pass {
				t.Logf("response does not contain Chinese: %s", truncate(output, 100))
			}
		})
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func stripCodeBlock(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Remove first line (```json or ```)
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
		// Remove trailing ```
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
