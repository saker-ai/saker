//go:build e2e

package suites

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cinience/saker/e2e"
	"github.com/cinience/saker/eval"
)

// serverURL returns the saker server URL from environment.
func serverURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("SAKER_SERVER_URL")
	if url == "" {
		t.Skip("SAKER_SERVER_URL not set")
	}
	return url
}

// videosDir returns the path to test video fixtures.
func videosDir() string {
	d := os.Getenv("E2E_VIDEOS_DIR")
	if d == "" {
		d = "/data/videos"
	}
	return d
}

// videoPath returns the full path to a test video file.
func videoPath(name string) string {
	return filepath.Join(videosDir(), name)
}

// newClient creates an e2e client connected to the test server.
func newClient(t *testing.T) *e2e.Client {
	t.Helper()
	c := e2e.NewClient(serverURL(t))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := c.WaitForHealthy(ctx, 30*time.Second); err != nil {
		t.Fatalf("server not healthy: %v", err)
	}
	return c
}

// newJudge creates a Judge for evaluating outputs.
func newJudge(t *testing.T) *e2e.Judge {
	t.Helper()
	return e2e.NewJudge(t)
}

// Baseline holds minimum thresholds for a test suite.
type Baseline struct {
	MinPassRate float64 `json:"min_pass_rate"`
	MinAvgScore float64 `json:"min_avg_score"`
}

// BaselineConfig maps suite names to their baselines.
type BaselineConfig struct {
	Version string              `json:"version"`
	Suites  map[string]Baseline `json:"suites"`
}

// loadBaseline reads the baseline.json file.
func loadBaseline(t *testing.T) BaselineConfig {
	t.Helper()
	path := os.Getenv("E2E_BASELINE_PATH")
	if path == "" {
		path = "/data/baseline.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Logf("baseline not found at %s, using defaults", path)
		return BaselineConfig{
			Suites: map[string]Baseline{
				"video_quick_e2e":    {MinPassRate: 0.60, MinAvgScore: 0.50},
				"video_deep_e2e":     {MinPassRate: 0.60, MinAvgScore: 0.50},
				"tool_selection_e2e": {MinPassRate: 0.75, MinAvgScore: 0.60},
			},
		}
	}
	var cfg BaselineConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse baseline: %v", err)
	}
	return cfg
}

// checkBaseline compares suite results against baseline thresholds.
func checkBaseline(t *testing.T, suite *eval.EvalSuite) {
	t.Helper()
	cfg := loadBaseline(t)
	bl, ok := cfg.Suites[suite.Name]
	if !ok {
		t.Logf("no baseline for suite %q, skipping threshold check", suite.Name)
		return
	}

	rate := suite.PassRate()
	avg := suite.AvgScore()

	// Allow 10% variance for LLM non-determinism
	if rate < bl.MinPassRate*0.9 {
		t.Errorf("pass rate %.1f%% below baseline %.1f%% (with 10%% tolerance)",
			rate*100, bl.MinPassRate*100)
	}
	if avg < bl.MinAvgScore*0.9 {
		t.Errorf("avg score %.2f below baseline %.2f (with 10%% tolerance)",
			avg, bl.MinAvgScore)
	}
}

// saveReport writes the suite results to the reports directory.
func saveReport(t *testing.T, suite *eval.EvalSuite) {
	t.Helper()
	dir := os.Getenv("E2E_REPORTS_DIR")
	if dir == "" {
		dir = "/data/reports"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("cannot create reports dir: %v", err)
		return
	}

	report := eval.EvalReport{
		Suites:    []eval.EvalSuite{*suite},
		Generated: time.Now(),
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Logf("marshal report: %v", err)
		return
	}

	filename := filepath.Join(dir, suite.Name+"_"+time.Now().Format("20060102-150405")+".json")
	if err := os.WriteFile(filename, data, 0o644); err != nil {
		t.Logf("write report: %v", err)
		return
	}
	t.Logf("report saved: %s", filename)
}
