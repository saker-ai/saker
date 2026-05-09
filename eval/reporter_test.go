package eval

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestReporter_WriteJSON(t *testing.T) {
	t.Parallel()
	r := Reporter{}
	report := &EvalReport{}
	s := EvalSuite{Name: "test"}
	s.Add(EvalResult{Name: "case1", Pass: true, Score: 1.0})
	s.Add(EvalResult{Name: "case2", Pass: false, Score: 0.3, Expected: "foo", Got: "bar"})
	report.Add(s)

	var buf bytes.Buffer
	if err := r.WriteJSON(&buf, report); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var decoded EvalReport
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Suites) != 1 {
		t.Fatalf("expected 1 suite, got %d", len(decoded.Suites))
	}
	if len(decoded.Suites[0].Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(decoded.Suites[0].Results))
	}
}

func TestReporter_WriteText(t *testing.T) {
	t.Parallel()
	r := Reporter{}
	report := &EvalReport{}
	s := EvalSuite{Name: "demo"}
	s.Add(EvalResult{Name: "pass", Pass: true, Score: 1.0})
	s.Add(EvalResult{Name: "fail", Pass: false, Score: 0.0, Expected: "x", Got: "y"})
	report.Add(s)

	var buf bytes.Buffer
	if err := r.WriteText(&buf, report); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	out := buf.String()
	if out == "" {
		t.Error("WriteText produced empty output")
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"short", 10, "short"},
		{"a long string here", 10, "a long..."}, // 7 chars + "..."
		{"exact", 5, "exact"},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.n)
		if len(got) > tt.n {
			t.Errorf("truncate(%q, %d) = %q (len %d > %d)", tt.input, tt.n, got, len(got), tt.n)
		}
	}
}
