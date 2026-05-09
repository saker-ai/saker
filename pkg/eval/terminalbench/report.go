package terminalbench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeReport persists the structured report alongside a human-readable
// summary into outputDir. results.jsonl is already streamed by Runner.Run.
//
//	<outputDir>/report.json    — full machine-readable Report
//	<outputDir>/summary.txt    — pass-rate / per-category text overview
//
// The function is intentionally tolerant: a write failure on summary.txt does
// not invalidate report.json (and vice-versa). The first error encountered is
// returned so callers can surface it; later writes still attempt their work.
func writeReport(outputDir string, report *Report) error {
	if strings.TrimSpace(outputDir) == "" {
		return fmt.Errorf("terminalbench: output dir is empty")
	}
	if report == nil {
		return fmt.Errorf("terminalbench: report is nil")
	}
	var firstErr error

	if err := writeJSONFile(filepath.Join(outputDir, "report.json"), report); err != nil {
		firstErr = err
	}
	if err := writeSummary(filepath.Join(outputDir, "summary.txt"), report); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// writeJSONFile marshals value with two-space indentation and writes it
// atomically (write to temp + rename) so a crash mid-write doesn't leave a
// partial file on disk.
func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("terminalbench: marshal %s: %w", filepath.Base(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("terminalbench: write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("terminalbench: rename %s: %w", filepath.Base(path), err)
	}
	return nil
}

// writeSummary emits a plain-text table the user can eyeball without jq.
func writeSummary(path string, report *Report) error {
	var b strings.Builder
	agg := report.Aggregate
	fmt.Fprintf(&b, "Terminal-Bench 2 Evaluation Summary\n")
	fmt.Fprintf(&b, "===================================\n")
	if strings.TrimSpace(report.Dataset) != "" {
		fmt.Fprintf(&b, "Dataset:      %s\n", report.Dataset)
	}
	if strings.TrimSpace(report.OutputDir) != "" {
		fmt.Fprintf(&b, "Output dir:   %s\n", report.OutputDir)
	}
	if strings.TrimSpace(report.Config.Provider) != "" || strings.TrimSpace(report.Config.Model) != "" {
		fmt.Fprintf(&b, "Provider:     %s\n", report.Config.Provider)
		fmt.Fprintf(&b, "Model:        %s\n", report.Config.Model)
	}
	if strings.TrimSpace(report.Config.SakerCommit) != "" {
		fmt.Fprintf(&b, "saker build: %s (%s)\n", report.Config.SakerCommit, valueOr(report.Config.GoVersion, "?"))
	}
	fmt.Fprintf(&b, "Concurrency:  %d\n", report.Config.Concurrency)
	fmt.Fprintf(&b, "Started:      %s\n", report.StartedAt.UTC().Format("2006-01-02T15:04:05Z"))
	fmt.Fprintf(&b, "Finished:     %s\n", report.FinishedAt.UTC().Format("2006-01-02T15:04:05Z"))
	fmt.Fprintf(&b, "Duration:     %s\n", report.Duration.Round(1e9))
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "Aggregate\n")
	fmt.Fprintf(&b, "---------\n")
	fmt.Fprintf(&b, "Total:        %d\n", agg.Total)
	fmt.Fprintf(&b, "Passed:       %d\n", agg.Passed)
	fmt.Fprintf(&b, "Failed:       %d\n", agg.Failed)
	fmt.Fprintf(&b, "Errored:      %d  (verifier never produced a reward)\n", agg.Errored)
	fmt.Fprintf(&b, "Skipped:      %d\n", agg.Skipped)
	fmt.Fprintf(&b, "Pass rate:    %.2f%%\n", agg.PassRate*100)
	fmt.Fprintf(&b, "\n")

	// Stop-reason histogram across non-skipped tasks. Helps separate "model
	// truly errored" from "task budget exhausted" when reading a baseline.
	stopHist := stopReasonHistogram(report.Results)
	if len(stopHist) > 0 {
		fmt.Fprintf(&b, "Stop reasons\n")
		fmt.Fprintf(&b, "------------\n")
		for _, e := range stopHist {
			fmt.Fprintf(&b, "%-22s %6d\n", e.reason, e.count)
		}
		fmt.Fprintf(&b, "\n")
	}

	// Token totals + cache hit ratio. Prompt cache wins are expensive to
	// detect after the fact, so surface them up-front.
	tin, tout, tcr, tcc := tokenTotals(report.Results)
	if tin+tout > 0 {
		ratio := 0.0
		if tin+tcr > 0 {
			ratio = float64(tcr) / float64(tin+tcr) * 100
		}
		fmt.Fprintf(&b, "Tokens\n")
		fmt.Fprintf(&b, "------\n")
		fmt.Fprintf(&b, "Input:        %d\n", tin)
		fmt.Fprintf(&b, "Output:       %d\n", tout)
		fmt.Fprintf(&b, "Cache read:   %d\n", tcr)
		fmt.Fprintf(&b, "Cache write:  %d\n", tcc)
		fmt.Fprintf(&b, "Cache hit:    %.1f%%  (cache_read / (input + cache_read))\n", ratio)
		fmt.Fprintf(&b, "\n")
	}

	if len(report.Categories) > 0 {
		fmt.Fprintf(&b, "By category\n")
		fmt.Fprintf(&b, "-----------\n")
		fmt.Fprintf(&b, "%-32s %6s %6s %6s %8s\n", "Category", "Total", "Pass", "Skip", "Rate")
		for _, cat := range report.Categories {
			fmt.Fprintf(&b, "%-32s %6d %6d %6d %7.2f%%\n",
				cat.Category, cat.Total, cat.Passed, cat.Skipped, cat.PassRate*100)
		}
		fmt.Fprintf(&b, "\n")
	}

	if len(report.Results) > 0 {
		fmt.Fprintf(&b, "Failures & errors\n")
		fmt.Fprintf(&b, "-----------------\n")
		any := false
		for _, r := range report.Results {
			if r.Pass || r.Skipped {
				continue
			}
			any = true
			label := "FAIL"
			if !r.VerifierRan {
				label = "ERR "
			}
			fmt.Fprintf(&b, "%s %-40s stage=%s score=%.2f", label, r.Name, valueOr(r.Stage, "-"), r.Score)
			if r.TestsTotal > 0 {
				fmt.Fprintf(&b, " tests=%d/%d", r.TestsPassed, r.TestsTotal)
			}
			if r.StopReason != "" {
				fmt.Fprintf(&b, " stop=%s", r.StopReason)
			}
			if r.ErrorMsg != "" {
				fmt.Fprintf(&b, " err=%q", oneLine(r.ErrorMsg, 120))
			}
			fmt.Fprintf(&b, "\n")
		}
		if !any {
			fmt.Fprintf(&b, "(none)\n")
		}
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("terminalbench: write summary.txt: %w", err)
	}
	return nil
}

// stopHistEntry is a (reason, count) pair used by the summary's stop-reason
// histogram. Kept local to report.go — it doesn't belong in the public surface.
type stopHistEntry struct {
	reason string
	count  int
}

func stopReasonHistogram(results []TaskResult) []stopHistEntry {
	counts := map[string]int{}
	for _, r := range results {
		if r.Skipped {
			continue
		}
		key := r.StopReason
		if strings.TrimSpace(key) == "" {
			key = "(unset)"
		}
		counts[key]++
	}
	out := make([]stopHistEntry, 0, len(counts))
	for k, v := range counts {
		out = append(out, stopHistEntry{reason: k, count: v})
	}
	// Sort by count desc, then reason asc for stable output.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			a, b := out[j-1], out[j]
			if a.count > b.count || (a.count == b.count && a.reason <= b.reason) {
				break
			}
			out[j-1], out[j] = b, a
		}
	}
	return out
}

func tokenTotals(results []TaskResult) (in, out, cacheRead, cacheCreate int) {
	for _, r := range results {
		in += r.InputTokens
		out += r.OutputTokens
		cacheRead += r.CacheReadTokens
		cacheCreate += r.CacheCreationTokens
	}
	return
}

func valueOr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// oneLine collapses newlines and truncates to keep the summary table tidy.
func oneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if max > 0 && len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
