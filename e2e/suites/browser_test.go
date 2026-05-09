//go:build e2e

package suites

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cinience/saker/e2e"
	"github.com/cinience/saker/eval"
)

func TestBrowserAutomation(t *testing.T) {
	client := newClient(t)
	judge := newJudge(t)

	cases := []struct {
		Name           string
		Prompt         string
		ExpectInOutput []string
		GroundTruth    map[string]string
		MinScore       float64
	}{
		{
			Name:   "screenshot_page",
			Prompt: "Navigate to https://example.com and take a screenshot.",
			GroundTruth: map[string]string{
				"action": "should navigate to example.com and capture a screenshot",
				"tool":   "should use the browser tool with navigate + screenshot actions",
			},
			MinScore: 0.5,
		},
		{
			Name:           "extract_content",
			Prompt:         "Open https://example.com in the browser and extract the page title and main heading text.",
			ExpectInOutput: []string{"Example Domain"},
			GroundTruth: map[string]string{
				"title":   "Example Domain",
				"content": "should contain the page title and heading",
			},
			MinScore: 0.5,
		},
		{
			Name:           "evaluate_js",
			Prompt:         "Open https://example.com in the browser and run JavaScript to get document.title, then report the result.",
			ExpectInOutput: []string{"Example Domain"},
			GroundTruth: map[string]string{
				"action": "should use browser tool with evaluate action to run JS",
				"result": "document.title should return 'Example Domain'",
			},
			MinScore: 0.5,
		},
	}

	suite := &eval.EvalSuite{Name: "browser_e2e"}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()

			sessionID := "e2e-browser-" + tc.Name
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

			// Check expected substrings
			for _, substr := range tc.ExpectInOutput {
				if !strings.Contains(resp.Output, substr) {
					t.Logf("WARN %s: output missing %q", tc.Name, substr)
				}
			}

			judgeResult, err := judge.Evaluate(ctx, e2e.JudgeInput{
				Scenario:    "Browser automation task: " + tc.Name,
				Prompt:      tc.Prompt,
				Output:      resp.Output,
				GroundTruth: tc.GroundTruth,
				Rubric:      "The agent should use the browser tool to navigate, interact with the page, and return accurate results. Verify correct tool selection and parameter usage.",
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
			})
		})
	}

	t.Cleanup(func() {
		t.Logf("\n%s", suite.Summary())
		saveReport(t, suite)
		checkBaseline(t, suite)
	})
}
