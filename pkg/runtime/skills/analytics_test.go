package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewSkillTrackerEmpty(t *testing.T) {
	t.Parallel()
	tr := NewSkillTracker(filepath.Join(t.TempDir(), "analytics.json"))
	stats := tr.GetStats()
	if len(stats) != 0 {
		t.Fatalf("expected empty stats, got %d", len(stats))
	}
}

func TestNewSkillTrackerLoadsExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "analytics.json")

	data := SkillAnalytics{
		Skills: map[string]*SkillStats{
			"test-skill": {
				Name:            "test-skill",
				ActivationCount: 5,
				BySource:        map[string]int{"keywords": 3, "forced": 2},
			},
		},
	}
	raw, _ := json.Marshal(data)
	os.WriteFile(path, raw, 0600)

	tr := NewSkillTracker(path)
	stats := tr.GetStats()
	if stats["test-skill"] == nil {
		t.Fatal("expected test-skill in loaded stats")
	}
	if stats["test-skill"].ActivationCount != 5 {
		t.Fatalf("expected 5 activations, got %d", stats["test-skill"].ActivationCount)
	}
}

func TestRecord(t *testing.T) {
	t.Parallel()
	tr := NewSkillTracker(filepath.Join(t.TempDir(), "analytics.json"))

	tr.Record(SkillActivationRecord{
		Skill: "s1", Scope: "repo", Source: "keywords",
		Score: 0.8, Success: true, DurationMs: 100, TokenUsage: 50,
		Timestamp: time.Now(),
	})
	tr.Record(SkillActivationRecord{
		Skill: "s1", Scope: "repo", Source: "forced",
		Score: 0.0, Success: false, Error: "timeout", DurationMs: 200, TokenUsage: 30,
		Timestamp: time.Now(),
	})

	stats := tr.GetStats()
	s := stats["s1"]
	if s == nil {
		t.Fatal("expected s1 stats")
	}
	if s.ActivationCount != 2 {
		t.Fatalf("expected 2 activations, got %d", s.ActivationCount)
	}
	if s.SuccessCount != 1 {
		t.Fatalf("expected 1 success, got %d", s.SuccessCount)
	}
	if s.FailCount != 1 {
		t.Fatalf("expected 1 fail, got %d", s.FailCount)
	}
	if s.TotalTokens != 80 {
		t.Fatalf("expected 80 total tokens, got %d", s.TotalTokens)
	}
	if s.BySource["keywords"] != 1 || s.BySource["forced"] != 1 {
		t.Fatalf("unexpected by_source: %v", s.BySource)
	}
}

func TestHistoryRingBuffer(t *testing.T) {
	t.Parallel()
	tr := NewSkillTracker(filepath.Join(t.TempDir(), "analytics.json"))

	for i := 0; i < maxHistorySize+50; i++ {
		tr.Record(SkillActivationRecord{
			Skill: "s1", Source: "keywords", Success: true,
			Timestamp: time.Now(),
		})
	}

	history := tr.GetHistory("", 0)
	if len(history) != maxHistorySize {
		t.Fatalf("expected %d history entries, got %d", maxHistorySize, len(history))
	}
}

func TestFlush(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "analytics.json")
	tr := NewSkillTracker(path)

	tr.Record(SkillActivationRecord{
		Skill: "s1", Source: "forced", Success: true,
		Timestamp: time.Now(),
	})

	if err := tr.Flush(); err != nil {
		t.Fatalf("flush error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	var loaded SkillAnalytics
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if loaded.Skills["s1"] == nil {
		t.Fatal("expected s1 in flushed file")
	}

	// second flush with no changes should be no-op
	if err := tr.Flush(); err != nil {
		t.Fatalf("second flush error: %v", err)
	}
}

func TestGetHistory(t *testing.T) {
	t.Parallel()
	tr := NewSkillTracker(filepath.Join(t.TempDir(), "analytics.json"))

	tr.Record(SkillActivationRecord{Skill: "a", Source: "keywords", Success: true, Timestamp: time.Now()})
	tr.Record(SkillActivationRecord{Skill: "b", Source: "forced", Success: true, Timestamp: time.Now()})
	tr.Record(SkillActivationRecord{Skill: "a", Source: "forced", Success: true, Timestamp: time.Now()})

	h := tr.GetHistory("a", 0)
	if len(h) != 2 {
		t.Fatalf("expected 2 records for skill a, got %d", len(h))
	}
	// most recent first
	if h[0].Source != "forced" {
		t.Fatalf("expected most recent first, got source=%s", h[0].Source)
	}

	h2 := tr.GetHistory("a", 1)
	if len(h2) != 1 {
		t.Fatalf("expected 1 record with limit=1, got %d", len(h2))
	}
}

func TestParseSource(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		{"forced", "forced"},
		{"mentioned", "mentioned"},
		{"path", "path"},
		{"always", "always"},
		{"keywords|hit=deploy", "keywords"},
		{"keywords|all=2|hit=build", "keywords"},
		{"tags|require=3", "tags"},
		{"traits:2/3", "traits"},
		{"", "unknown"},
		{"custom", "custom"},
	}
	for _, tc := range cases {
		got := ParseSource(tc.input)
		if got != tc.want {
			t.Errorf("ParseSource(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
