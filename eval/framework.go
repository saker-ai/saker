// Package eval provides a lightweight evaluation framework for the saker SDK.
// Eval suites are standard Go tests under eval/suites/ and can be run with
// `go test ./eval/...`.
package eval

import (
	"fmt"
	"strings"
	"time"
)

// EvalResult captures the outcome of a single evaluation case.
type EvalResult struct {
	Suite    string         `json:"suite"`
	Name     string         `json:"name"`
	Pass     bool           `json:"pass"`
	Score    float64        `json:"score"` // [0,1]
	Expected string         `json:"expected,omitempty"`
	Got      string         `json:"got,omitempty"`
	Duration time.Duration  `json:"duration_ns"`
	Details  map[string]any `json:"details,omitempty"`
}

// EvalSuite aggregates results for a named evaluation suite.
type EvalSuite struct {
	Name    string       `json:"name"`
	Results []EvalResult `json:"results"`
}

// Add appends a result to the suite.
func (s *EvalSuite) Add(r EvalResult) {
	if r.Suite == "" {
		r.Suite = s.Name
	}
	s.Results = append(s.Results, r)
}

// PassRate returns the fraction of passing cases in [0,1].
func (s *EvalSuite) PassRate() float64 {
	if len(s.Results) == 0 {
		return 0
	}
	passed := 0
	for _, r := range s.Results {
		if r.Pass {
			passed++
		}
	}
	return float64(passed) / float64(len(s.Results))
}

// AvgScore returns the mean score across all results.
func (s *EvalSuite) AvgScore() float64 {
	if len(s.Results) == 0 {
		return 0
	}
	total := 0.0
	for _, r := range s.Results {
		total += r.Score
	}
	return total / float64(len(s.Results))
}

// Passed returns the number of passing cases.
func (s *EvalSuite) Passed() int {
	n := 0
	for _, r := range s.Results {
		if r.Pass {
			n++
		}
	}
	return n
}

// Failed returns the failing results.
func (s *EvalSuite) Failed() []EvalResult {
	var out []EvalResult
	for _, r := range s.Results {
		if !r.Pass {
			out = append(out, r)
		}
	}
	return out
}

// Summary returns a one-line human-readable summary.
func (s *EvalSuite) Summary() string {
	return fmt.Sprintf("[%s] %d/%d passed (%.1f%%), avg score: %.2f",
		s.Name, s.Passed(), len(s.Results), s.PassRate()*100, s.AvgScore())
}

// EvalReport aggregates multiple suites.
type EvalReport struct {
	Suites    []EvalSuite `json:"suites"`
	Generated time.Time   `json:"generated"`
}

// Add appends a suite to the report.
func (r *EvalReport) Add(s EvalSuite) {
	r.Suites = append(r.Suites, s)
}

// Summary returns a multi-line summary of all suites.
func (r *EvalReport) Summary() string {
	var b strings.Builder
	b.WriteString("=== Evaluation Report ===\n")
	totalPass, totalCount := 0, 0
	for _, s := range r.Suites {
		b.WriteString(s.Summary())
		b.WriteByte('\n')
		totalPass += s.Passed()
		totalCount += len(s.Results)
	}
	if totalCount > 0 {
		b.WriteString(fmt.Sprintf("--- Total: %d/%d passed (%.1f%%) ---\n",
			totalPass, totalCount, float64(totalPass)/float64(totalCount)*100))
	}
	return b.String()
}
