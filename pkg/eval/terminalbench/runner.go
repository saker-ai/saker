// runner.go: Runner type, constructor, Run() entry, shared constants/types.
//
// Package terminalbench orchestrates a Terminal-Bench 2 evaluation run.
//
// Per-task pipeline (one Docker container per task):
//
//  1. EnvFactory builds an ExecutionEnvironment for task.DockerImage
//  2. PrepareSession starts the container; CloseSession tears it down
//  3. CopyArchiveTo unpacks environment.tar into /app (when present)
//  4. Builtin file/bash tools are bound to the per-task env via SetEnvironment
//  5. agent.New(modelBridge, historyToolExecutor, ...) runs until Done /
//     MaxIterations / ctx timeout, all tool ops execute inside the container
//  6. CopyArchiveTo unpacks tests.tar into /tests, then RunCommand executes
//     `bash test.sh` (or task.TestSh override) with terminal_timeout x2
//  7. ReadFile /logs/verifier/reward.txt → score (Pass when score >= 1.0)
//
// A worker pool drives Concurrency tasks at once. Results are streamed to a
// JSONL file as they finish so a SIGINT mid-run still leaves partial output
// on disk; the final aggregate Report is written once Run completes.
package terminalbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/cinience/saker/pkg/eval/dataset"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
)

const (
	// defaultGuestWorkdir is only used as a last-resort fallback when the
	// docker image declares no WORKDIR; the runner now lets dockerenv
	// auto-detect the image's own WORKDIR (TB2 tasks pin theirs to
	// /workspace, /build, /root, …) so the agent's tools land where the
	// task files actually live.
	defaultGuestWorkdir   = "/app"
	defaultTestsDir       = "/tests"
	defaultLogsDir        = "/logs/verifier"
	defaultRewardFilename = "reward.txt"
	// defaultVerifierCmd intentionally does NOT cd anywhere — it lets test.sh
	// inherit the container's WORKDIR (image-declared, e.g. /workspace).
	// Upstream TB2 tests use relative paths like os.path.exists("plus_comm.v")
	// which assume cwd = image WORKDIR; cd'ing to /tests broke that contract.
	defaultVerifierCmd = "bash /tests/test.sh"
	defaultPassScore   = 1.0
)

// archiveUploader is implemented by environments that can stream a tar
// archive into the guest. dockerenv.Environment satisfies it; tests provide
// their own stubs.
type archiveUploader interface {
	CopyArchiveTo(ctx context.Context, ps *sandboxenv.PreparedSession, destDir string, archive io.Reader) error
}

// Runner owns one full evaluation pass.
type Runner struct {
	cfg   Config
	tasks []dataset.Task
}

// New constructs a Runner. It loads/filters the dataset eagerly so callers
// hit "tasks not found" errors before spinning up any container.
func New(cfg Config) (*Runner, error) {
	if cfg.ModelFactory == nil {
		return nil, errors.New("terminalbench: ModelFactory is required")
	}
	cfg.applyDefaults()

	tasks := cfg.Tasks
	if len(tasks) == 0 {
		if strings.TrimSpace(cfg.DatasetRoot) == "" {
			return nil, errors.New("terminalbench: DatasetRoot or Tasks is required")
		}
		loaded, err := dataset.LoadFiltered(cfg.DatasetRoot, cfg.Include, cfg.Exclude)
		if err != nil {
			return nil, err
		}
		tasks = loaded
	} else if len(cfg.Include) > 0 || len(cfg.Exclude) > 0 {
		filtered, err := dataset.Filter(tasks, cfg.Include, cfg.Exclude)
		if err != nil {
			return nil, err
		}
		tasks = filtered
	}
	if len(tasks) == 0 {
		return nil, errors.New("terminalbench: no tasks selected after filtering")
	}
	return &Runner{cfg: cfg, tasks: tasks}, nil
}

// Tasks returns the resolved task list (post-filtering).
func (r *Runner) Tasks() []dataset.Task { return append([]dataset.Task(nil), r.tasks...) }

// Run executes the full evaluation. The returned Report is also written to
// disk under cfg.OutputDir as report.json + results.jsonl + summary.txt.
func (r *Runner) Run(ctx context.Context) (*Report, error) {
	if r == nil {
		return nil, errors.New("terminalbench: runner is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	outputDir := r.cfg.OutputDir
	if strings.TrimSpace(outputDir) == "" {
		dir, err := os.MkdirTemp("", "tb2-eval-*")
		if err != nil {
			return nil, fmt.Errorf("terminalbench: create output dir: %w", err)
		}
		outputDir = dir
	} else if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("terminalbench: ensure output dir: %w", err)
	}
	r.cfg.OutputDir = outputDir

	jsonlPath := filepath.Join(outputDir, "results.jsonl")
	jsonlFile, err := os.Create(jsonlPath)
	if err != nil {
		return nil, fmt.Errorf("terminalbench: open results.jsonl: %w", err)
	}
	defer jsonlFile.Close()

	startedAt := time.Now()

	g, gctx := errgroup.WithContext(ctx)
	if r.cfg.Concurrency > 0 {
		g.SetLimit(r.cfg.Concurrency)
	}

	results := make([]TaskResult, len(r.tasks))
	var (
		writeMu  sync.Mutex
		writeErr error
	)
	streamResult := func(idx int, res TaskResult) {
		results[idx] = res
		line, _ := json.Marshal(res)
		writeMu.Lock()
		defer writeMu.Unlock()
		if writeErr != nil {
			return
		}
		if _, err := jsonlFile.Write(append(line, '\n')); err != nil {
			writeErr = err
		}
	}

	for i := range r.tasks {
		i, task := i, r.tasks[i]
		g.Go(func() error {
			res := r.runOne(gctx, task)
			streamResult(i, res)
			return nil
		})
	}
	_ = g.Wait()

	finishedAt := time.Now()
	if writeErr != nil {
		return nil, fmt.Errorf("terminalbench: stream results: %w", writeErr)
	}

	report := &Report{
		Dataset:    r.cfg.DatasetRoot,
		OutputDir:  outputDir,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Duration:   finishedAt.Sub(startedAt),
		Results:    results,
		Aggregate:  aggregate(results),
		Categories: byCategory(results),
		Config: ConfigSnapshot{
			Provider:        r.cfg.ProviderName,
			Model:           r.cfg.ModelName,
			Concurrency:     r.cfg.Concurrency,
			MaxIterations:   r.cfg.MaxIterations,
			MaxTokens:       r.cfg.MaxTokens,
			MaxBudgetUSD:    r.cfg.MaxBudgetUSD,
			TaskTimeout:     r.cfg.TaskTimeout,
			TerminalTimeout: r.cfg.TerminalTimeout,
			PullPolicy:      string(r.cfg.PullPolicy),
			NetworkMode:     r.cfg.NetworkMode,
			SakerCommit:     buildCommit(),
			GoVersion:       runtime.Version(),
			WithMirror:      len(r.cfg.MirrorEnv) > 0,
			VerifierMirr:    len(r.cfg.VerifierEnv) > 0,
			ProxyURL:        r.cfg.ProxyURL,
			UseACP:          r.cfg.UseACP,
		},
	}

	if err := writeReport(outputDir, report); err != nil {
		return report, err
	}
	return report, nil
}

// buildCommit returns the VCS revision burned into the binary by `go build`.
// Empty string when not available (e.g. `go run`, vendored deps with stale
// .git, or build flags that strip vcs info).
func buildCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			rev := s.Value
			if len(rev) > 12 {
				rev = rev[:12]
			}
			return rev
		}
	}
	return ""
}
