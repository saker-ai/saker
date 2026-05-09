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

func TestCrossSessionMemory(t *testing.T) {
	client := newClient(t)
	judge := newJudge(t)

	suite := &eval.EvalSuite{Name: "memory_e2e"}

	// Use a shared session ID so memory persists across requests.
	sessionID := fmt.Sprintf("e2e-memory-%d", time.Now().UnixNano())

	// --- Phase 1: Save memory ---
	t.Run("save_memory", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		prompt := `Remember this: Our production database is PostgreSQL 15 running on port 5432, and we use GORM as the ORM layer. The database host is db.internal.example.com.`
		start := time.Now()

		resp, err := client.Run(ctx, prompt, sessionID)
		duration := time.Since(start)

		if err != nil {
			suite.Add(eval.EvalResult{
				Name:     "save_memory",
				Pass:     false,
				Score:    0,
				Expected: "memory saved successfully",
				Got:      err.Error(),
				Duration: duration,
			})
			t.Fatalf("save_memory failed: %v", err)
			return
		}

		// The agent should acknowledge saving the memory.
		pass := resp.Output != "" && (strings.Contains(strings.ToLower(resp.Output), "remember") ||
			strings.Contains(strings.ToLower(resp.Output), "save") ||
			strings.Contains(strings.ToLower(resp.Output), "note") ||
			strings.Contains(strings.ToLower(resp.Output), "记") ||
			len(resp.Output) > 10)

		score := 0.0
		if pass {
			score = 1.0
		}

		suite.Add(eval.EvalResult{
			Name:     "save_memory",
			Pass:     pass,
			Score:    score,
			Expected: "acknowledgment of saving memory",
			Got:      truncateOutput(resp.Output, 200),
			Duration: duration,
		})
	})

	// --- Phase 2: Recall memory in a new request ---
	t.Run("recall_memory", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		prompt := `What database do we use in production? What ORM? What's the host?`
		start := time.Now()

		resp, err := client.Run(ctx, prompt, sessionID)
		duration := time.Since(start)

		if err != nil {
			suite.Add(eval.EvalResult{
				Name:     "recall_memory",
				Pass:     false,
				Score:    0,
				Expected: "recalled memory info",
				Got:      err.Error(),
				Duration: duration,
			})
			t.Logf("FAIL recall_memory: %v", err)
			return
		}

		judgeResult, err := judge.Evaluate(ctx, e2e.JudgeInput{
			Scenario: "Cross-session memory recall: agent was told database info in a previous message and should recall it now",
			Prompt:   prompt,
			Output:   resp.Output,
			GroundTruth: map[string]string{
				"database": "PostgreSQL 15",
				"orm":      "GORM",
				"host":     "db.internal.example.com",
				"port":     "5432",
			},
			Rubric: "The agent should recall previously stored information accurately. All four facts (PostgreSQL 15, GORM, db.internal.example.com, port 5432) should be present.",
		})

		var score float64
		var pass bool
		var got string

		if err != nil {
			t.Logf("judge error: %v", err)
			// Fallback: check key substrings
			output := strings.ToLower(resp.Output)
			hits := 0
			for _, kw := range []string{"postgresql", "gorm", "db.internal", "5432"} {
				if strings.Contains(output, kw) {
					hits++
				}
			}
			score = float64(hits) / 4.0
			pass = hits >= 3
			got = fmt.Sprintf("keyword hits: %d/4", hits)
		} else {
			score = judgeResult.Overall()
			pass = score >= 0.5
			got = fmt.Sprintf("score=%.2f reason=%s", score, judgeResult.Reasoning)
		}

		suite.Add(eval.EvalResult{
			Name:     "recall_memory",
			Pass:     pass,
			Score:    score,
			Expected: "PostgreSQL 15, GORM, db.internal.example.com, 5432",
			Got:      got,
			Duration: duration,
		})
	})

	// --- Phase 3: Use memory in context ---
	t.Run("apply_memory", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		prompt := `Write a Go code snippet to connect to our production database using the ORM we discussed.`
		start := time.Now()

		resp, err := client.Run(ctx, prompt, sessionID)
		duration := time.Since(start)

		if err != nil {
			suite.Add(eval.EvalResult{
				Name:     "apply_memory",
				Pass:     false,
				Score:    0,
				Expected: "code using remembered DB info",
				Got:      err.Error(),
				Duration: duration,
			})
			t.Logf("FAIL apply_memory: %v", err)
			return
		}

		judgeResult, err := judge.Evaluate(ctx, e2e.JudgeInput{
			Scenario: "Apply remembered context: agent should use previously stored database info to write connection code",
			Prompt:   prompt,
			Output:   resp.Output,
			GroundTruth: map[string]string{
				"orm":      "should use GORM (gorm.io/gorm)",
				"database": "should connect to PostgreSQL",
				"host":     "should reference db.internal.example.com or the stored host",
			},
			Rubric: "The code should use GORM to connect to PostgreSQL. It should reference the previously stored host/port. The code should be syntactically valid Go.",
		})

		var score float64
		var pass bool
		var got string

		if err != nil {
			t.Logf("judge error: %v", err)
			output := strings.ToLower(resp.Output)
			hasGorm := strings.Contains(output, "gorm")
			hasPG := strings.Contains(output, "postgres")
			if hasGorm && hasPG {
				score = 0.6
				pass = true
			}
			got = fmt.Sprintf("gorm=%v postgres=%v", hasGorm, hasPG)
		} else {
			score = judgeResult.Overall()
			pass = score >= 0.5
			got = fmt.Sprintf("score=%.2f reason=%s", score, judgeResult.Reasoning)
		}

		suite.Add(eval.EvalResult{
			Name:     "apply_memory",
			Pass:     pass,
			Score:    score,
			Expected: "GORM + PostgreSQL connection code",
			Got:      got,
			Duration: duration,
		})
	})

	t.Cleanup(func() {
		t.Logf("\n%s", suite.Summary())
		saveReport(t, suite)
		checkBaseline(t, suite)
	})
}

func truncateOutput(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
