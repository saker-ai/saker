//go:build e2e

package suites

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cinience/saker/e2e"
	"github.com/cinience/saker/eval"
)

func TestVideoQuickAnalysis(t *testing.T) {
	client := newClient(t)
	judge := newJudge(t)

	cases := []struct {
		Name        string
		Video       string
		Prompt      string
		GroundTruth map[string]string
		MinScore    float64
	}{
		{
			Name:   "color_bars_describe",
			Video:  "color_bars.mp4",
			Prompt: "Analyze the video at " + videoPath("color_bars.mp4") + " and describe its content.",
			GroundTruth: map[string]string{
				"content": "Color test pattern / color bars / test signal",
			},
			MinScore: 0.5,
		},
		{
			Name:   "countdown_text_recognition",
			Video:  "countdown.mp4",
			Prompt: "Analyze the video at " + videoPath("countdown.mp4") + ". What text appears in this video?",
			GroundTruth: map[string]string{
				"text":    "timestamp / timecode / TEST VIDEO",
				"content": "countdown or timer display",
			},
			MinScore: 0.5,
		},
		{
			Name:   "moving_object_action",
			Video:  "moving_object.mp4",
			Prompt: "Analyze the video at " + videoPath("moving_object.mp4") + ". What is happening in this video?",
			GroundTruth: map[string]string{
				"action":  "moving text / horizontal movement",
				"content": "text moving across the screen",
			},
			MinScore: 0.5,
		},
	}

	suite := &eval.EvalSuite{Name: "video_quick_e2e"}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			sessionID := "e2e-video-quick-" + tc.Name
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

			// Use LLM judge to evaluate the output
			judgeResult, err := judge.Evaluate(ctx, e2e.JudgeInput{
				Scenario:    "Quick video analysis of synthetic test video: " + tc.Video,
				Prompt:      tc.Prompt,
				Output:      resp.Output,
				GroundTruth: tc.GroundTruth,
			})

			var score float64
			var pass bool
			var got string

			if err != nil {
				t.Logf("judge error for %s: %v", tc.Name, err)
				// Fallback: check if output is non-empty
				if resp.Output != "" {
					score = 0.3
					pass = false
				}
				got = "judge error: " + err.Error()
			} else {
				score = judgeResult.Overall()
				pass = score >= tc.MinScore
				got = judgeResult.Reasoning
			}

			suite.Add(eval.EvalResult{
				Name:     tc.Name,
				Pass:     pass,
				Score:    score,
				Expected: "score >= " + formatFloat(tc.MinScore),
				Got:      formatFloat(score) + " - " + got,
				Duration: duration,
				Details: map[string]any{
					"output_length": len(resp.Output),
					"session_id":    resp.SessionID,
				},
			})

			if !pass {
				t.Logf("WARN %s: score %.2f below threshold %.2f", tc.Name, score, tc.MinScore)
			}
		})
	}

	t.Cleanup(func() {
		t.Logf("\n%s", suite.Summary())
		saveReport(t, suite)
		checkBaseline(t, suite)
	})
}

func formatFloat(f float64) string {
	return fmt.Sprintf("%.2f", f)
}
