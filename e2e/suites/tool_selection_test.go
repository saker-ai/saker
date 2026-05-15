//go:build e2e

package suites

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/saker-ai/saker/e2e"
	"github.com/saker-ai/saker/eval"
)

func TestToolSelectionE2E(t *testing.T) {
	client := newClient(t)
	judge := newJudge(t)

	cases := []struct {
		Name           string
		Prompt         string
		ExpectInOutput []string // substrings that should appear in output
		GroundTruth    map[string]string
		MinScore       float64
	}{
		{
			Name:           "list_files",
			Prompt:         "List files in the current directory",
			ExpectInOutput: []string{}, // just check non-empty
			GroundTruth: map[string]string{
				"tool": "should use bash with ls or similar command",
			},
			MinScore: 0.5,
		},
		{
			Name:   "search_pattern",
			Prompt: "Search for all files containing the word 'TODO' in the codebase",
			GroundTruth: map[string]string{
				"tool": "should use grep or similar search tool",
			},
			MinScore: 0.5,
		},
		{
			Name:   "read_file",
			Prompt: "Read the contents of /etc/hostname",
			GroundTruth: map[string]string{
				"tool": "should use file_read or Read tool",
			},
			MinScore: 0.5,
		},
		{
			Name:   "git_status",
			Prompt: "Show the current git status",
			GroundTruth: map[string]string{
				"tool": "should use bash with git status command",
			},
			MinScore: 0.5,
		},
	}

	suite := &eval.EvalSuite{Name: "tool_selection_e2e"}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()

			sessionID := "e2e-tool-" + tc.Name
			start := time.Now()

			resp, err := client.Run(ctx, tc.Prompt, sessionID)
			duration := time.Since(start)

			if err != nil {
				suite.Add(eval.EvalResult{
					Name:     tc.Name,
					Pass:     false,
					Score:    0,
					Expected: "successful response",
					Got:      err.Error(),
					Duration: duration,
				})
				t.Logf("FAIL %s: %v", tc.Name, err)
				return
			}

			// Check expected substrings in output
			for _, substr := range tc.ExpectInOutput {
				if !strings.Contains(resp.Output, substr) {
					t.Logf("WARN %s: output missing expected substring %q", tc.Name, substr)
				}
			}

			judgeResult, err := judge.Evaluate(ctx, e2e.JudgeInput{
				Scenario:    "Tool selection and execution for: " + tc.Prompt,
				Prompt:      tc.Prompt,
				Output:      resp.Output,
				GroundTruth: tc.GroundTruth,
				Rubric:      "The agent should select the appropriate tool (bash, grep, file_read, etc.), execute it correctly, and return useful results.",
			})

			var score float64
			var pass bool
			var got string

			if err != nil {
				t.Logf("judge error for %s: %v", tc.Name, err)
				if resp.Output != "" {
					score = 0.3
				}
				got = "judge error: " + err.Error()
			} else {
				score = judgeResult.Overall()
				pass = score >= tc.MinScore
				got = fmt.Sprintf("score=%.2f reason=%s", score, judgeResult.Reasoning)
			}

			suite.Add(eval.EvalResult{
				Name:     tc.Name,
				Pass:     pass,
				Score:    score,
				Expected: fmt.Sprintf("score >= %.2f", tc.MinScore),
				Got:      got,
				Duration: duration,
				Details: map[string]any{
					"output_length": len(resp.Output),
				},
			})
		})
	}

	t.Cleanup(func() {
		t.Logf("\n%s", suite.Summary())
		saveReport(t, suite)
		checkBaseline(t, suite)
	})
}
