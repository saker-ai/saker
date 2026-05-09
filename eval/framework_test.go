package eval

import (
	"testing"
	"time"
)

func TestEvalSuite_PassRate(t *testing.T) {
	t.Parallel()
	s := &EvalSuite{Name: "test"}
	s.Add(EvalResult{Name: "a", Pass: true, Score: 1.0})
	s.Add(EvalResult{Name: "b", Pass: false, Score: 0.0})
	s.Add(EvalResult{Name: "c", Pass: true, Score: 0.8})

	if got := s.PassRate(); got < 0.66 || got > 0.67 {
		t.Errorf("PassRate() = %f, want ~0.667", got)
	}
	if got := s.Passed(); got != 2 {
		t.Errorf("Passed() = %d, want 2", got)
	}
}

func TestEvalSuite_Empty(t *testing.T) {
	t.Parallel()
	s := &EvalSuite{Name: "empty"}
	if got := s.PassRate(); got != 0 {
		t.Errorf("empty PassRate() = %f, want 0", got)
	}
	if got := s.AvgScore(); got != 0 {
		t.Errorf("empty AvgScore() = %f, want 0", got)
	}
}

func TestEvalSuite_AvgScore(t *testing.T) {
	t.Parallel()
	s := &EvalSuite{Name: "scores"}
	s.Add(EvalResult{Name: "a", Score: 1.0, Pass: true})
	s.Add(EvalResult{Name: "b", Score: 0.5, Pass: false})
	avg := s.AvgScore()
	if avg < 0.74 || avg > 0.76 {
		t.Errorf("AvgScore() = %f, want 0.75", avg)
	}
}

func TestEvalSuite_Failed(t *testing.T) {
	t.Parallel()
	s := &EvalSuite{Name: "fail"}
	s.Add(EvalResult{Name: "ok", Pass: true})
	s.Add(EvalResult{Name: "bad", Pass: false})
	s.Add(EvalResult{Name: "worse", Pass: false})
	failures := s.Failed()
	if len(failures) != 2 {
		t.Errorf("Failed() count = %d, want 2", len(failures))
	}
}

func TestEvalSuite_Summary(t *testing.T) {
	t.Parallel()
	s := &EvalSuite{Name: "demo"}
	s.Add(EvalResult{Name: "a", Pass: true, Score: 1.0})
	s.Add(EvalResult{Name: "b", Pass: false, Score: 0.0})
	summary := s.Summary()
	if summary == "" {
		t.Error("Summary() returned empty string")
	}
}

func TestEvalSuite_AutoSetsSuiteName(t *testing.T) {
	t.Parallel()
	s := &EvalSuite{Name: "auto"}
	s.Add(EvalResult{Name: "case1"})
	if s.Results[0].Suite != "auto" {
		t.Errorf("Suite name not auto-set: got %q", s.Results[0].Suite)
	}
}

func TestEvalReport_Summary(t *testing.T) {
	t.Parallel()
	r := &EvalReport{}
	s1 := EvalSuite{Name: "s1"}
	s1.Add(EvalResult{Name: "a", Pass: true, Score: 1.0})
	s2 := EvalSuite{Name: "s2"}
	s2.Add(EvalResult{Name: "b", Pass: false, Score: 0.0})
	r.Add(s1)
	r.Add(s2)
	summary := r.Summary()
	if summary == "" {
		t.Error("Report Summary() returned empty string")
	}
}

func TestEvalResult_Duration(t *testing.T) {
	t.Parallel()
	r := EvalResult{
		Name:     "timed",
		Pass:     true,
		Duration: 150 * time.Millisecond,
	}
	if r.Duration != 150*time.Millisecond {
		t.Errorf("Duration = %v, want 150ms", r.Duration)
	}
}
