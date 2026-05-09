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

func TestVideoDeepAnalysis(t *testing.T) {
	client := newClient(t)
	judge := newJudge(t)

	cases := []struct {
		Name        string
		Video       string
		Prompt      string
		GroundTruth map[string]string
		MinScore    float64
		Timeout     time.Duration
	}{
		{
			Name:   "scene_change_timeline",
			Video:  "scene_change.mp4",
			Prompt: "Deep analyze the video at " + videoPath("scene_change.mp4") + " and provide a complete timeline report.",
			GroundTruth: map[string]string{
				"scenes":   "3 distinct scenes/segments: red, blue, green",
				"text":     "Scene 1, Scene 2, Scene 3",
				"timeline": "should contain time markers",
			},
			MinScore: 0.5,
			Timeout:  4 * time.Minute,
		},
		{
			Name:   "scene_change_entity_detection",
			Video:  "scene_change.mp4",
			Prompt: "Deep analyze the video at " + videoPath("scene_change.mp4") + " and identify all text and color patterns that appear.",
			GroundTruth: map[string]string{
				"colors": "red, blue, green",
				"text":   "Scene 1, Scene 2, Scene 3",
			},
			MinScore: 0.5,
			Timeout:  4 * time.Minute,
		},
	}

	suite := &eval.EvalSuite{Name: "video_deep_e2e"}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			timeout := tc.Timeout
			if timeout == 0 {
				timeout = 5 * time.Minute
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			sessionID := "e2e-video-deep-" + tc.Name
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
				Scenario:    "Deep video analysis with timeline and multi-track annotation: " + tc.Video,
				Prompt:      tc.Prompt,
				Output:      resp.Output,
				GroundTruth: tc.GroundTruth,
				Rubric:      "Deep analysis should produce a timeline report with segment markers and multi-dimensional annotations (visual, entity, scene, action, text, search_tags).",
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
					"duration_s":    duration.Seconds(),
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
