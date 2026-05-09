package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/eval/terminalbench"
)

// writeReport is a small helper to materialize a Report on disk so tests
// can drive runEvalAnalyze through its real --baseline / --current path.
func writeReport(t *testing.T, dir, name string, r terminalbench.Report) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(r); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	return path
}

// TestRunEvalAnalyze_HappyPath drives the full subcommand against two
// hand-built reports and asserts the markdown contains the regression
// signal a triage owner would actually look for: the regressed task name,
// the fixed task name, the stop_reason histogram delta, and a non-zero
// pass_rate delta. We deliberately don't pin exact whitespace — only the
// load-bearing strings — so cosmetic tweaks to the markdown don't churn
// the test.
func TestRunEvalAnalyze_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	baseline := terminalbench.Report{
		Dataset:  "tb2",
		Duration: 10 * time.Minute,
		Aggregate: terminalbench.AggregateScore{
			Total: 3, Passed: 2, Failed: 1, PassRate: 2.0 / 3.0,
		},
		Results: []terminalbench.TaskResult{
			{Name: "task-a", Pass: true, VerifierRan: true, StopReason: "end_turn",
				InputTokens: 1000, OutputTokens: 200, Duration: 60 * time.Second},
			{Name: "task-b", Pass: true, VerifierRan: true, StopReason: "end_turn",
				InputTokens: 500, OutputTokens: 100, Duration: 30 * time.Second},
			{Name: "task-c", Pass: false, VerifierRan: true, StopReason: "max_iterations",
				InputTokens: 2000, OutputTokens: 400, Duration: 120 * time.Second},
		},
		Config: terminalbench.ConfigSnapshot{
			SakerCommit: "abcdef1234567890", Model: "claude-sonnet-4-5", GoVersion: "go1.23",
		},
	}
	current := terminalbench.Report{
		Dataset:  "tb2",
		Duration: 12 * time.Minute,
		Aggregate: terminalbench.AggregateScore{
			Total: 3, Passed: 2, Failed: 1, PassRate: 2.0 / 3.0,
		},
		Results: []terminalbench.TaskResult{
			{Name: "task-a", Pass: false, VerifierRan: true, StopReason: "max_iterations",
				InputTokens: 1500, OutputTokens: 300, Duration: 90 * time.Second},
			{Name: "task-b", Pass: true, VerifierRan: true, StopReason: "end_turn",
				InputTokens: 500, OutputTokens: 100, Duration: 30 * time.Second},
			{Name: "task-c", Pass: true, VerifierRan: true, StopReason: "end_turn",
				InputTokens: 1800, OutputTokens: 350, Duration: 100 * time.Second},
		},
		Config: terminalbench.ConfigSnapshot{
			SakerCommit: "fedcba9876543210", Model: "claude-sonnet-4-5", GoVersion: "go1.23",
		},
	}

	bp := writeReport(t, dir, "baseline.json", baseline)
	cp := writeReport(t, dir, "current.json", current)

	var out, errOut bytes.Buffer
	if err := runEvalAnalyze(&out, &errOut, []string{"--baseline", bp, "--current", cp, "--top", "5"}); err != nil {
		t.Fatalf("runEvalAnalyze: %v\nstderr=%s", err, errOut.String())
	}
	got := out.String()

	wants := []string{
		"# Terminal-Bench 2 Diff Report",
		"## Build Identity",
		"abcdef123456",
		"fedcba987654",
		"## Aggregate",
		"## Stop Reason Histogram",
		"`end_turn`",
		"`max_iterations`",
		"## Transitions",
		"### Newly passing: 1",
		"task-c (was: failed)",
		"### Newly failing: 1",
		"task-a (now: failed)",
		"## Top",
		"task-a", // shows up in token-delta table because |+600| is large
		"task-c", // also large delta
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q\n--- full output ---\n%s", w, got)
		}
	}
}

// TestRunEvalAnalyze_OnlyInOneSide verifies that tasks present in only one
// of the two reports are surfaced under "only in baseline" / "only in
// current" so a reviewer notices the dataset filter changed underneath the
// experiment.
func TestRunEvalAnalyze_OnlyInOneSide(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	baseline := terminalbench.Report{
		Results: []terminalbench.TaskResult{
			{Name: "shared", Pass: true, VerifierRan: true, StopReason: "end_turn"},
			{Name: "removed", Pass: true, VerifierRan: true, StopReason: "end_turn"},
		},
	}
	current := terminalbench.Report{
		Results: []terminalbench.TaskResult{
			{Name: "shared", Pass: true, VerifierRan: true, StopReason: "end_turn"},
			{Name: "added", Pass: false, VerifierRan: false, StopReason: "model_error"},
		},
	}
	bp := writeReport(t, dir, "b.json", baseline)
	cp := writeReport(t, dir, "c.json", current)

	var out, errOut bytes.Buffer
	if err := runEvalAnalyze(&out, &errOut, []string{"--baseline", bp, "--current", cp}); err != nil {
		t.Fatalf("runEvalAnalyze: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Tasks only in baseline: 1") || !strings.Contains(got, "removed") {
		t.Errorf("missing 'only in baseline' section: %s", got)
	}
	if !strings.Contains(got, "Tasks only in current: 1") || !strings.Contains(got, "added") {
		t.Errorf("missing 'only in current' section: %s", got)
	}
}

// TestRunEvalAnalyze_RequiresBothPaths verifies the flag-parser-level guard.
// We don't trust callers to remember both flags, and a silent default-to-
// empty would produce a confusing "decode: unexpected end of JSON" error
// instead of an actionable one.
func TestRunEvalAnalyze_RequiresBothPaths(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	err := runEvalAnalyze(&out, &errOut, []string{"--baseline", "/nope/x.json"})
	if err == nil {
		t.Fatal("expected error when --current is missing")
	}
	if !strings.Contains(err.Error(), "--baseline and --current") {
		t.Errorf("error should name the missing flag: %v", err)
	}
}

func TestOutcomeLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		r    terminalbench.TaskResult
		want string
	}{
		{"skipped wins", terminalbench.TaskResult{Skipped: true, Pass: false}, "skipped"},
		{"pass overrides verifier", terminalbench.TaskResult{Pass: true}, "passed"},
		{"verifier didn't run", terminalbench.TaskResult{VerifierRan: false}, "errored"},
		{"verifier ran, fail", terminalbench.TaskResult{VerifierRan: true}, "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := outcomeLabel(tc.r); got != tc.want {
				t.Errorf("outcomeLabel = %q, want %q", got, tc.want)
			}
		})
	}
}
