package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/model"
)

// JudgeInput describes a scenario for the LLM judge to evaluate.
type JudgeInput struct {
	Scenario    string            `json:"scenario"`
	Prompt      string            `json:"prompt"`
	Output      string            `json:"output"`
	GroundTruth map[string]string `json:"ground_truth,omitempty"`
	Rubric      string            `json:"rubric,omitempty"`
}

// JudgeResult contains the judge's evaluation scores and reasoning.
type JudgeResult struct {
	Correctness  float64 `json:"correctness"`  // [0-5]
	Completeness float64 `json:"completeness"` // [0-5]
	Accuracy     float64 `json:"accuracy"`     // [0-5]
	Structure    float64 `json:"structure"`    // [0-5]
	Utility      float64 `json:"utility"`      // [0-5]
	Reasoning    string  `json:"reasoning"`
}

// Overall returns a normalized score in [0,1].
func (r *JudgeResult) Overall() float64 {
	return (r.Correctness + r.Completeness + r.Accuracy + r.Structure + r.Utility) / 25.0
}

// Pass returns true if the overall score meets or exceeds the threshold.
func (r *JudgeResult) Pass(threshold float64) bool {
	return r.Overall() >= threshold
}

const judgeSystemPrompt = `You are an AI Agent output quality evaluator. You must evaluate the given output strictly and objectively.

Score each dimension from 0 to 5:

1. **Correctness**: Did the agent select the right tool and use correct parameters?
2. **Completeness**: Does the output cover all key information requested?
3. **Accuracy**: How well does the description match the ground truth?
4. **Structure**: Is the output well-formatted, clear, and organized?
5. **Utility**: Is the output practically useful to the user?

Return ONLY a JSON object with these fields:
{"correctness":N,"completeness":N,"accuracy":N,"structure":N,"utility":N,"reasoning":"brief explanation"}

Be strict: 0=completely wrong, 1=mostly wrong, 2=partially correct, 3=acceptable, 4=good, 5=excellent.`

func buildJudgePrompt(input JudgeInput) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Scenario\n%s\n\n", input.Scenario))
	b.WriteString(fmt.Sprintf("## User Prompt\n%s\n\n", input.Prompt))
	b.WriteString(fmt.Sprintf("## Agent Output\n%s\n\n", input.Output))

	if len(input.GroundTruth) > 0 {
		b.WriteString("## Ground Truth\n")
		for k, v := range input.GroundTruth {
			b.WriteString(fmt.Sprintf("- **%s**: %s\n", k, v))
		}
		b.WriteString("\n")
	}

	if input.Rubric != "" {
		b.WriteString(fmt.Sprintf("## Additional Rubric\n%s\n\n", input.Rubric))
	}

	b.WriteString("Evaluate and return JSON only.")
	return b.String()
}

// Judge evaluates agent outputs using a separate LLM call.
type Judge struct {
	Model model.Model
}

// NewJudge creates a Judge backed by a real LLM. Skips the test if no API key.
// Supports both Anthropic and OpenAI-compatible providers (e.g., DashScope).
// Set E2E_JUDGE_PROVIDER=openai and DASHSCOPE_API_KEY to use DashScope.
func NewJudge(t *testing.T) *Judge {
	t.Helper()

	judgeModel := os.Getenv("E2E_JUDGE_MODEL")
	judgeProvider := strings.ToLower(os.Getenv("E2E_JUDGE_PROVIDER"))

	// Auto-detect provider
	if judgeProvider == "" {
		if os.Getenv("DASHSCOPE_API_KEY") != "" {
			judgeProvider = "openai"
		} else {
			judgeProvider = "anthropic"
		}
	}

	var provider model.Provider
	switch judgeProvider {
	case "openai":
		apiKey := os.Getenv("DASHSCOPE_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		if apiKey == "" {
			t.Skip("skipping: no API key for OpenAI-compatible judge")
		}
		if judgeModel == "" {
			judgeModel = "qwen3.6-plus"
		}
		baseURL := os.Getenv("E2E_JUDGE_BASE_URL")
		if baseURL == "" {
			baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
		}
		provider = &model.OpenAIProvider{
			APIKey:    apiKey,
			BaseURL:   baseURL,
			ModelName: judgeModel,
		}
	default:
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
		}
		if apiKey == "" {
			t.Skip("skipping: ANTHROPIC_API_KEY not set for judge")
		}
		if judgeModel == "" {
			judgeModel = "claude-haiku-4-5-20251001"
		}
		provider = &model.AnthropicProvider{
			APIKey:    apiKey,
			BaseURL:   os.Getenv("ANTHROPIC_BASE_URL"),
			ModelName: judgeModel,
		}
	}

	mdl, err := provider.Model(context.Background())
	if err != nil {
		t.Fatalf("NewJudge: %v", err)
	}
	return &Judge{Model: mdl}
}

// Evaluate runs the LLM judge on the given input.
func (j *Judge) Evaluate(ctx context.Context, input JudgeInput) (*JudgeResult, error) {
	prompt := buildJudgePrompt(input)

	resp, err := j.Model.Complete(ctx, model.Request{
		System: judgeSystemPrompt,
		Messages: []model.Message{
			{Role: "user", Content: prompt},
		},
		MaxTokens: 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("judge: model error: %w", err)
	}

	text := resp.Message.TextContent()
	// Strip potential markdown fencing
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		if idx := strings.Index(text, "\n"); idx >= 0 {
			text = text[idx+1:]
		}
		if strings.HasSuffix(text, "```") {
			text = text[:len(text)-3]
		}
		text = strings.TrimSpace(text)
	}

	var result JudgeResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("judge: parse response: %w (raw: %s)", err, truncateStr(text, 200))
	}
	return &result, nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
