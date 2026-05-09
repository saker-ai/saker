package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Reporter writes evaluation results in various formats.
type Reporter struct{}

// WriteJSON writes the full report as JSON.
func (Reporter) WriteJSON(w io.Writer, report *EvalReport) error {
	if report.Generated.IsZero() {
		report.Generated = time.Now()
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// WriteText writes a human-readable summary.
func (Reporter) WriteText(w io.Writer, report *EvalReport) error {
	_, err := fmt.Fprint(w, report.Summary())
	if err != nil {
		return err
	}

	// Print failures.
	for _, s := range report.Suites {
		failures := s.Failed()
		if len(failures) == 0 {
			continue
		}
		fmt.Fprintf(w, "\nFailed cases in [%s]:\n", s.Name)
		for _, f := range failures {
			fmt.Fprintf(w, "  - %s: expected=%q got=%q (score=%.2f)\n",
				f.Name, f.Expected, truncate(f.Got, 80), f.Score)
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
