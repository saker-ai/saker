//go:build e2e

package suites

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cinience/saker/e2e"
	"github.com/cinience/saker/eval"
)

func TestWebhookSend(t *testing.T) {
	client := newClient(t)
	judge := newJudge(t)

	webhookURL := os.Getenv("WEBHOOK_ECHO_URL")
	if webhookURL == "" {
		t.Skip("WEBHOOK_ECHO_URL not set")
	}

	cases := []struct {
		Name        string
		Prompt      string
		GroundTruth map[string]string
		MinScore    float64
	}{
		{
			Name:   "post_json",
			Prompt: fmt.Sprintf(`Send a POST request to %s/test-hook with JSON body: {"event":"e2e_test","status":"ok","timestamp":"2026-04-15"}. Report the response status code.`, webhookURL),
			GroundTruth: map[string]string{
				"action": "should use webhook tool to send HTTP POST with JSON body",
				"result": "should receive a 200 OK response",
			},
			MinScore: 0.5,
		},
		{
			Name:   "custom_headers",
			Prompt: fmt.Sprintf(`Send a POST request to %s/custom-header with header "X-E2E-Test: true" and body {"check":"headers"}. Report what you get back.`, webhookURL),
			GroundTruth: map[string]string{
				"action":  "should use webhook tool with custom headers",
				"headers": "should include X-E2E-Test header",
			},
			MinScore: 0.5,
		},
	}

	suite := &eval.EvalSuite{Name: "webhook_e2e"}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()

			sessionID := "e2e-webhook-" + tc.Name
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

			judgeResult, err := judge.Evaluate(ctx, e2e.JudgeInput{
				Scenario:    "Webhook HTTP request: " + tc.Name,
				Prompt:      tc.Prompt,
				Output:      resp.Output,
				GroundTruth: tc.GroundTruth,
				Rubric:      "The agent should use the webhook tool to send an HTTP request with the correct method, URL, headers, and body. Verify the response is reported correctly.",
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
