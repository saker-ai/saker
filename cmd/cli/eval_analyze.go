// Package main: `saker eval analyze` — diff two report.json files.
//
// Why a built-in subcommand instead of a shell script: the report schema is
// owned by the runner. Putting the diff in-tree means breaking schema changes
// fail at compile time (or at least in unit tests), not silently in a Bash
// awk pipeline that nobody re-reads.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/eval/terminalbench"
)

// runEvalAnalyze implements `saker eval analyze --baseline A --current B`.
//
// Output is markdown to stdout so it can be piped into a PR comment, gist,
// or grep'd directly. The function intentionally takes (stdout, stderr)
// rather than printing to package-level streams so it's easy to test.
func runEvalAnalyze(stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("saker eval analyze", flag.ContinueOnError)
	fs.SetOutput(stderr)

	baselinePath := fs.String("baseline", "", "Path to baseline report.json (required)")
	currentPath := fs.String("current", "", "Path to current report.json (required)")
	topN := fs.Int("top", 10, "How many tasks to show in top-N tables (cost/duration deltas)")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*baselinePath) == "" || strings.TrimSpace(*currentPath) == "" {
		fs.Usage()
		return fmt.Errorf("eval analyze: both --baseline and --current are required")
	}
	if *topN < 0 {
		return fmt.Errorf("eval analyze: --top must be >= 0")
	}

	baseline, err := readReport(*baselinePath)
	if err != nil {
		return fmt.Errorf("eval analyze: read baseline: %w", err)
	}
	current, err := readReport(*currentPath)
	if err != nil {
		return fmt.Errorf("eval analyze: read current: %w", err)
	}

	renderDiff(stdout, baseline, current, *topN)
	return nil
}

// readReport decodes a report.json. We tolerate unknown fields (don't call
// DisallowUnknownFields) so a report written by a newer saker version can
// still be diff'd against an older one — the analyzer only depends on a
// stable subset of the schema.
func readReport(path string) (*terminalbench.Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rep terminalbench.Report
	if err := json.NewDecoder(f).Decode(&rep); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &rep, nil
}

// renderDiff writes the full markdown report. Each section is a separate
// helper so they can grow independently (and so the top-of-function reads
// like a table of contents).
func renderDiff(w io.Writer, baseline, current *terminalbench.Report, topN int) {
	fmt.Fprintln(w, "# Terminal-Bench 2 Diff Report")
	fmt.Fprintln(w)
	renderBuildIdentity(w, baseline, current)
	renderAggregateDiff(w, baseline, current)
	renderStopReasonHistogram(w, baseline, current)
	renderTransitions(w, baseline, current)
	if topN > 0 {
		renderTopTokenDeltas(w, baseline, current, topN)
		renderTopDurationDeltas(w, baseline, current, topN)
	}
}

func renderBuildIdentity(w io.Writer, baseline, current *terminalbench.Report) {
	fmt.Fprintln(w, "## Build Identity")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "- **baseline**: saker=`%s`  model=`%s`  go=`%s`  dataset=`%s`\n",
		shortCommit(baseline.Config.SakerCommit), valueOrDash(baseline.Config.Model),
		valueOrDash(baseline.Config.GoVersion), valueOrDash(baseline.Dataset))
	fmt.Fprintf(w, "- **current** : saker=`%s`  model=`%s`  go=`%s`  dataset=`%s`\n",
		shortCommit(current.Config.SakerCommit), valueOrDash(current.Config.Model),
		valueOrDash(current.Config.GoVersion), valueOrDash(current.Dataset))
	if baseline.Dataset != current.Dataset {
		fmt.Fprintln(w, "- **note**: dataset paths differ — task identity assumed by name match.")
	}
	fmt.Fprintln(w)
}

func renderAggregateDiff(w io.Writer, baseline, current *terminalbench.Report) {
	a, b := baseline.Aggregate, current.Aggregate
	fmt.Fprintln(w, "## Aggregate")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| metric | baseline | current | delta |")
	fmt.Fprintln(w, "|--------|---------:|--------:|------:|")
	fmt.Fprintf(w, "| total   | %d | %d | %s |\n", a.Total, b.Total, signedInt(b.Total-a.Total))
	fmt.Fprintf(w, "| passed  | %d | %d | %s |\n", a.Passed, b.Passed, signedInt(b.Passed-a.Passed))
	fmt.Fprintf(w, "| failed  | %d | %d | %s |\n", a.Failed, b.Failed, signedInt(b.Failed-a.Failed))
	fmt.Fprintf(w, "| errored | %d | %d | %s |\n", a.Errored, b.Errored, signedInt(b.Errored-a.Errored))
	fmt.Fprintf(w, "| skipped | %d | %d | %s |\n", a.Skipped, b.Skipped, signedInt(b.Skipped-a.Skipped))
	fmt.Fprintf(w, "| pass_rate | %.2f%% | %.2f%% | %s |\n",
		a.PassRate*100, b.PassRate*100, signedFloat((b.PassRate-a.PassRate)*100, "%"))
	fmt.Fprintf(w, "| duration | %s | %s | %s |\n",
		baseline.Duration.Round(time.Second), current.Duration.Round(time.Second),
		signedDuration(current.Duration-baseline.Duration))
	fmt.Fprintln(w)
}

func renderStopReasonHistogram(w io.Writer, baseline, current *terminalbench.Report) {
	bHist := stopReasonCounts(baseline.Results)
	cHist := stopReasonCounts(current.Results)
	keys := mergedKeys(bHist, cHist)
	if len(keys) == 0 {
		return
	}
	sort.Strings(keys)
	fmt.Fprintln(w, "## Stop Reason Histogram")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| stop_reason | baseline | current | delta |")
	fmt.Fprintln(w, "|-------------|---------:|--------:|------:|")
	for _, k := range keys {
		fmt.Fprintf(w, "| `%s` | %d | %d | %s |\n", k, bHist[k], cHist[k], signedInt(cHist[k]-bHist[k]))
	}
	fmt.Fprintln(w)
}

// renderTransitions surfaces tasks whose pass/fail outcome flipped. This is
// the section regression triage actually opens first — aggregate numbers are
// summary statistics, but "which tasks broke" is the actionable signal.
func renderTransitions(w io.Writer, baseline, current *terminalbench.Report) {
	bIdx := indexResults(baseline.Results)
	cIdx := indexResults(current.Results)
	var (
		fixed     []string // failed → passed
		regressed []string // passed → failed/errored
		newTasks  []string // present only in current
		removed   []string // present only in baseline
	)
	for name, b := range bIdx {
		c, ok := cIdx[name]
		if !ok {
			removed = append(removed, name)
			continue
		}
		switch {
		case !b.Pass && c.Pass:
			fixed = append(fixed, fmt.Sprintf("%s (was: %s)", name, outcomeLabel(b)))
		case b.Pass && !c.Pass:
			regressed = append(regressed, fmt.Sprintf("%s (now: %s)", name, outcomeLabel(c)))
		}
	}
	for name := range cIdx {
		if _, ok := bIdx[name]; !ok {
			newTasks = append(newTasks, name)
		}
	}
	sort.Strings(fixed)
	sort.Strings(regressed)
	sort.Strings(newTasks)
	sort.Strings(removed)

	fmt.Fprintln(w, "## Transitions")
	fmt.Fprintln(w)
	writeTransitionList(w, "Newly passing", fixed)
	writeTransitionList(w, "Newly failing", regressed)
	writeTransitionList(w, "Tasks only in current", newTasks)
	writeTransitionList(w, "Tasks only in baseline", removed)
}

func writeTransitionList(w io.Writer, label string, items []string) {
	fmt.Fprintf(w, "### %s: %d\n\n", label, len(items))
	if len(items) == 0 {
		fmt.Fprintln(w, "_(none)_")
		fmt.Fprintln(w)
		return
	}
	for _, it := range items {
		fmt.Fprintf(w, "- %s\n", it)
	}
	fmt.Fprintln(w)
}

// renderTopTokenDeltas surfaces tasks whose total token usage changed the
// most. Sorted by absolute delta so both regressions (more tokens) and
// improvements (better cache hits) bubble to the top.
func renderTopTokenDeltas(w io.Writer, baseline, current *terminalbench.Report, topN int) {
	type row struct {
		Name  string
		B, C  int
		Delta int
	}
	bIdx := indexResults(baseline.Results)
	cIdx := indexResults(current.Results)
	var rows []row
	for name, c := range cIdx {
		b, ok := bIdx[name]
		if !ok {
			continue
		}
		bt := b.InputTokens + b.OutputTokens
		ct := c.InputTokens + c.OutputTokens
		if bt == 0 && ct == 0 {
			continue
		}
		rows = append(rows, row{Name: name, B: bt, C: ct, Delta: ct - bt})
	}
	if len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		return absInt(rows[i].Delta) > absInt(rows[j].Delta)
	})
	if topN < len(rows) {
		rows = rows[:topN]
	}
	fmt.Fprintf(w, "## Top %d tasks by token delta (input+output)\n\n", len(rows))
	fmt.Fprintln(w, "| task | baseline | current | delta |")
	fmt.Fprintln(w, "|------|---------:|--------:|------:|")
	for _, r := range rows {
		fmt.Fprintf(w, "| %s | %d | %d | %s |\n", r.Name, r.B, r.C, signedInt(r.Delta))
	}
	fmt.Fprintln(w)
}

func renderTopDurationDeltas(w io.Writer, baseline, current *terminalbench.Report, topN int) {
	type row struct {
		Name  string
		B, C  time.Duration
		Delta time.Duration
	}
	bIdx := indexResults(baseline.Results)
	cIdx := indexResults(current.Results)
	var rows []row
	for name, c := range cIdx {
		b, ok := bIdx[name]
		if !ok {
			continue
		}
		rows = append(rows, row{Name: name, B: b.Duration, C: c.Duration, Delta: c.Duration - b.Duration})
	}
	if len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		return absDuration(rows[i].Delta) > absDuration(rows[j].Delta)
	})
	if topN < len(rows) {
		rows = rows[:topN]
	}
	fmt.Fprintf(w, "## Top %d tasks by duration delta\n\n", len(rows))
	fmt.Fprintln(w, "| task | baseline | current | delta |")
	fmt.Fprintln(w, "|------|---------:|--------:|------:|")
	for _, r := range rows {
		fmt.Fprintf(w, "| %s | %s | %s | %s |\n",
			r.Name, r.B.Round(time.Second), r.C.Round(time.Second),
			signedDuration(r.Delta))
	}
	fmt.Fprintln(w)
}

// --- helpers ---------------------------------------------------------------

func indexResults(rs []terminalbench.TaskResult) map[string]terminalbench.TaskResult {
	out := make(map[string]terminalbench.TaskResult, len(rs))
	for _, r := range rs {
		out[r.Name] = r
	}
	return out
}

func stopReasonCounts(rs []terminalbench.TaskResult) map[string]int {
	out := make(map[string]int)
	for _, r := range rs {
		k := r.StopReason
		if k == "" {
			k = "(unset)"
		}
		out[k]++
	}
	return out
}

func mergedKeys(a, b map[string]int) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

// outcomeLabel turns a TaskResult into a one-word verdict for the
// transition list. We intentionally collapse Skipped/Errored/Failed into
// distinct buckets — "fail" alone hides a task that never reached the
// verifier, which is a different class of regression.
func outcomeLabel(r terminalbench.TaskResult) string {
	switch {
	case r.Skipped:
		return "skipped"
	case r.Pass:
		return "passed"
	case !r.VerifierRan:
		return "errored"
	default:
		return "failed"
	}
}

func signedInt(v int) string {
	if v > 0 {
		return fmt.Sprintf("+%d", v)
	}
	return fmt.Sprintf("%d", v)
}

func signedFloat(v float64, suffix string) string {
	if v > 0 {
		return fmt.Sprintf("+%.2f%s", v, suffix)
	}
	return fmt.Sprintf("%.2f%s", v, suffix)
}

func signedDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d > 0 {
		return "+" + d.String()
	}
	return d.String()
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// shortCommit trims a full SHA to 12 chars for display; passes through
// short SHAs and the placeholder "(none)" untouched.
func shortCommit(c string) string {
	c = strings.TrimSpace(c)
	if c == "" {
		return "(unknown)"
	}
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

func valueOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
