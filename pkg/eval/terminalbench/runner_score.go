// runner_score.go: verifier execution, reward parsing, dataset-wide aggregation.
package terminalbench

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/eval/dataset"
	sandboxenv "github.com/saker-ai/saker/pkg/sandbox/env"
)

// runVerifier uploads tests.tar, executes the verifier command and reads
// /logs/verifier/reward.txt. Records score/pass/log on res.
//
// verifierEnv is injected via `docker exec -e ...` (per-call), so the agent's
// shell never observes these vars — keeps the eval signal honest while still
// letting the verifier's uv/pip reach mirrors on a constrained network.
func runVerifier(ctx context.Context, env sandboxenv.ExecutionEnvironment, uploader archiveUploader, ps *sandboxenv.PreparedSession, task dataset.Task, res *TaskResult, terminalTimeout time.Duration, outputDir string, verifierEnv map[string]string) error {
	if task.TestsTar == "" {
		return errors.New("terminalbench: tests_tar missing")
	}
	testsFile, err := task.OpenTests()
	if err != nil {
		return fmt.Errorf("open tests_tar: %w", err)
	}
	defer testsFile.Close()

	if err := uploader.CopyArchiveTo(ctx, ps, defaultTestsDir, testsFile); err != nil {
		return fmt.Errorf("upload tests: %w", err)
	}

	if _, err := env.RunCommand(ctx, ps, sandboxenv.CommandRequest{
		Command: "mkdir -p " + defaultLogsDir,
		Timeout: 30 * time.Second,
		Env:     verifierEnv,
	}); err != nil {
		return fmt.Errorf("ensure verifier dir: %w", err)
	}

	verifierCmd := strings.TrimSpace(task.TestSh)
	if verifierCmd == "" || verifierCmd == "bash test.sh" {
		verifierCmd = defaultVerifierCmd
	}

	verifyTimeout := terminalTimeout * 2
	if task.TerminalTimeout > 0 {
		candidate := task.TerminalTimeout * 2
		if candidate < verifyTimeout {
			verifyTimeout = candidate
		}
	}

	verResult, err := env.RunCommand(ctx, ps, sandboxenv.CommandRequest{
		Command: verifierCmd,
		Timeout: verifyTimeout,
		Env:     verifierEnv,
	})
	if err != nil {
		return fmt.Errorf("run verifier: %w", err)
	}
	if verResult != nil {
		full := verResult.Stdout + verResult.Stderr
		// pytest results land at the *tail*; keep the last verifierLogInlineCap
		// bytes so apt-install spam doesn't crowd out the assertion summary.
		res.VerifierLog = tailBytes(strings.TrimSpace(full), verifierLogInlineCap)
		if path, lerr := writeVerifierLog(outputDir, task.Name, full); lerr == nil && path != "" {
			res.VerifierLogPath = path
		}
		passed, total := parsePytestSummary(full)
		res.TestsPassed = passed
		res.TestsTotal = total
	}

	rewardPath := filepath.Join(defaultLogsDir, defaultRewardFilename)
	rewardData, err := env.ReadFile(ctx, ps, rewardPath)
	if err != nil {
		return fmt.Errorf("read reward: %w", err)
	}
	score, err := parseReward(string(rewardData))
	if err != nil {
		return err
	}
	res.Score = score
	res.Pass = score >= defaultPassScore
	res.VerifierRan = true
	return nil
}

// pytestSummaryRE matches pytest's footer line, e.g.:
//
//	========================= 1 failed, 8 passed in 1.27s ==========================
//	========================= 9 passed in 1.27s ==========================
//	==================== 2 failed, 7 passed, 1 skipped in 0.42s ====================
//
// Captures (failed?, errors?, passed?, skipped?). Best-effort: tasks that
// don't use pytest leave TestsPassed/TestsTotal at zero.
var pytestSummaryRE = regexp.MustCompile(`(?:(\d+) failed)?(?:, )?(?:(\d+) errors?)?(?:, )?(?:(\d+) passed)?(?:, )?(?:(\d+) skipped)?\s+in\s+[\d.]+s`)

// parsePytestSummary scans `log` for pytest's summary footer and returns
// (passed, total). total = passed + failed + errors (skipped excluded so the
// ratio reflects actionable signal). Returns (0, 0) if not parseable.
func parsePytestSummary(log string) (passed, total int) {
	matches := pytestSummaryRE.FindAllStringSubmatch(log, -1)
	if len(matches) == 0 {
		return 0, 0
	}
	last := matches[len(matches)-1]
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	failed := atoi(last[1])
	errored := atoi(last[2])
	passed = atoi(last[3])
	if failed+errored+passed == 0 {
		return 0, 0
	}
	return passed, failed + errored + passed
}

// parseReward accepts "1.0", "1", "0.75", "0.0\n", etc.
func parseReward(raw string) (float64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, errors.New("terminalbench: reward.txt is empty")
	}
	score, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, fmt.Errorf("terminalbench: parse reward %q: %w", trimmed, err)
	}
	return score, nil
}

// aggregate computes the dataset-wide pass/fail/error/skip counts.
//
// Classification:
//   - Skipped: task.json declared a skip_reason
//   - Passed:  verifier ran and score >= passScore
//   - Errored: infrastructure failure — verifier never produced a reward
//     (sandbox start, dataset upload, verifier crash). The agent loop hitting
//     a deadline or model_error is NOT errored if the verifier still ran;
//     that's just a "failed" run with partial work.
//   - Failed:  verifier ran but did not pass (any partial credit, any 0)
func aggregate(results []TaskResult) AggregateScore {
	agg := AggregateScore{Total: len(results)}
	for _, r := range results {
		switch {
		case r.Skipped:
			agg.Skipped++
		case r.Pass:
			agg.Passed++
		case !r.VerifierRan:
			agg.Errored++
		default:
			agg.Failed++
		}
	}
	denom := agg.Total - agg.Skipped
	if denom > 0 {
		agg.PassRate = float64(agg.Passed) / float64(denom)
	}
	return agg
}

// byCategory bins results by task category and reports per-bin pass rates.
func byCategory(results []TaskResult) []CategoryScore {
	buckets := map[string]*CategoryScore{}
	for _, r := range results {
		key := strings.TrimSpace(r.Category)
		if key == "" {
			key = "uncategorized"
		}
		bucket, ok := buckets[key]
		if !ok {
			bucket = &CategoryScore{Category: key}
			buckets[key] = bucket
		}
		bucket.Total++
		if r.Skipped {
			bucket.Skipped++
			continue
		}
		if r.Pass {
			bucket.Passed++
		}
	}
	out := make([]CategoryScore, 0, len(buckets))
	for _, b := range buckets {
		denom := b.Total - b.Skipped
		if denom > 0 {
			b.PassRate = float64(b.Passed) / float64(denom)
		}
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Category < out[j].Category })
	return out
}
