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
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/cinience/saker/pkg/agent"
	"github.com/cinience/saker/pkg/eval/dataset"
	"github.com/cinience/saker/pkg/message"
	"github.com/cinience/saker/pkg/middleware"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/sandbox/dockerenv"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	"github.com/cinience/saker/pkg/security"
	"github.com/cinience/saker/pkg/tool"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
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

// ModelFactory builds a fresh model.Model for one task. Each task gets an
// isolated model so per-call state (token counters, retry counters, …) does
// not leak across tasks. Implementations may return the same backing client
// — they must just be safe to use concurrently.
type ModelFactory func(ctx context.Context) (model.Model, error)

// EnvFactory builds the ExecutionEnvironment used to run one task. Defaults
// to dockerenv.New; tests inject a stub that does not require Docker.
type EnvFactory func(task dataset.Task) (sandboxenv.ExecutionEnvironment, error)

// Config bundles every knob the runner exposes. Required: ModelFactory.
type Config struct {
	// DatasetRoot is the directory that contains tasks/. If Tasks is non-empty
	// it overrides the on-disk dataset (used by tests).
	DatasetRoot string
	Tasks       []dataset.Task
	Include     []string
	Exclude     []string

	// OutputDir is where report.json, results.jsonl and per-task logs land.
	// Empty means "scratch directory under os.TempDir()".
	OutputDir string

	Concurrency      int
	MaxIterations    int
	SystemPrompt     string
	TaskTimeout      time.Duration // cap per task (overrides task.AgentTimeout when smaller)
	TerminalTimeout  time.Duration // verifier-side cap (overrides task.TerminalTimeout when smaller)
	SkipIncompatible bool
	Verbose          bool

	ModelName    string
	ProviderName string
	ModelFactory ModelFactory
	EnvFactory   EnvFactory

	// DockerBinary / NetworkMode / PullPolicy are forwarded to the default
	// dockerenv-backed EnvFactory. Ignored when a custom EnvFactory is set.
	DockerBinary string
	NetworkMode  string
	PullPolicy   dockerenv.PullPolicy
	ContainerTTL time.Duration

	// RepeatLoopThreshold caps how many identical consecutive tool calls the
	// agent tolerates before aborting a task. Zero applies the agent default
	// (5); negative disables loop detection.
	RepeatLoopThreshold int
	// MaxBudgetUSD aborts a single task with StopReason "max_budget" once
	// cumulative estimated cost crosses this dollar value. 0 disables the
	// guard. Pricing comes from model.EstimateCost; unknown models silently
	// disable the cap.
	MaxBudgetUSD float64
	// MaxTokens aborts a single task with StopReason "max_tokens" once
	// cumulative input+output tokens cross this value. 0 disables.
	MaxTokens int
	// DisableTranscripts skips writing per-task <task>.jsonl conversation
	// dumps under <OutputDir>/transcripts/. Default false (transcripts on).
	DisableTranscripts bool

	// MirrorEnv is injected as container-level env vars (`docker run -e ...`).
	// Default empty: bare-saker baseline. Callers explicitly opt in (e.g.,
	// CLI's `--with-mirror`) to populate from DefaultMirrorEnv when network
	// constraints (China firewall) demand it. Note: anything set here is
	// visible to the agent via shell (`echo $PIP_INDEX_URL`), so treat
	// non-empty values as a deliberate concession, not a default.
	MirrorEnv map[string]string

	// VerifierEnv is injected ONLY into the verifier's RunCommand calls via
	// `docker exec -e ...`. The agent's shell never sees these vars (they're
	// per-call, not container-level), so they don't pollute the eval signal.
	// Defaults to a copy of DefaultMirrorEnv so the verifier (which itself
	// runs `uv`/`pip` to fetch tarballs from GitHub/PyPI) doesn't time out
	// when the host is on a constrained network. Pass an explicit empty
	// non-nil map to disable.
	VerifierEnv map[string]string

	// ProxyURL routes outbound HTTP(S) traffic from inside the container
	// through a host-side proxy (typically a Clash/Mihomo HTTP listener on
	// 127.0.0.1:7890). When set, defaultEnvFactory:
	//   - rewrites loopback hosts (127.0.0.1, localhost, ::1) to
	//     host.docker.internal so the container can actually reach the
	//     listener on the host;
	//   - injects HTTP_PROXY / HTTPS_PROXY / NO_PROXY (plus the lowercase
	//     forms) so curl, pip, apt, uv all honour it;
	//   - adds `--add-host=host.docker.internal:host-gateway` to docker run
	//     so the rewritten hostname resolves on Linux (Docker Desktop sets
	//     this automatically; Linux engines don't).
	// Mirror domains in MirrorEnv are auto-appended to NO_PROXY so the
	// proxy never short-circuits a fast direct path to aliyun/nju.
	ProxyURL string
}

// DefaultMirrorEnv is the canned mirror set callers may opt into when
// network constraints require it (China firewall, slow GH/PyPI from CN).
// NOT applied automatically — Config.MirrorEnv defaults to empty so the
// agent runs against bare saker. Aliyun hosts the bulk PyPI mirror; NJU's
// github-release proxy fronts the cpython tarballs that `uv` pulls when
// bootstrapping a managed interpreter. NJU is preferred over generic
// *.proxy.cn fronts because (a) it serves the asset directly (no redirect)
// and (b) its DNS resolves reliably from inside minimal container resolvers
// — gh-proxy.cn-style hosts often fail with "Name or service not known"
// inside docker default DNS.
var DefaultMirrorEnv = map[string]string{
	// pip / uv package index — aliyun PyPI mirror.
	"PIP_INDEX_URL":    "https://mirrors.aliyun.com/pypi/simple/",
	"PIP_TRUSTED_HOST": "mirrors.aliyun.com",
	"UV_DEFAULT_INDEX": "https://mirrors.aliyun.com/pypi/simple/",
	"UV_INDEX_URL":     "https://mirrors.aliyun.com/pypi/simple/",
	// uv-managed cpython tarballs come from python-build-standalone GH
	// releases. NJU mirrors them at /github-release/<owner>/<repo>/<tag>/<file>;
	// uv composes the URL by appending /<tag>/<file> to this prefix, which
	// matches that layout exactly.
	"UV_PYTHON_INSTALL_MIRROR": "https://mirror.nju.edu.cn/github-release/astral-sh/python-build-standalone",
	// huggingface — aliyun mirror is unreliable; use the official China
	// endpoint hf-mirror.com (community-run, but stable).
	"HF_ENDPOINT": "https://hf-mirror.com",
}

func (c *Config) applyDefaults() {
	if c.Concurrency <= 0 {
		c.Concurrency = 1
	}
	if c.MaxIterations <= 0 {
		// 50 mirrors agent.DefaultSubagentMaxIterations and the CLI flag
		// default in cmd/cli/eval_terminalbench.go. The previous 30 was an
		// orphan — every public surface (CLI, agent subagents, README) was
		// already aligned on 50, so this just removes the inconsistency.
		c.MaxIterations = 50
	}
	if c.TaskTimeout <= 0 {
		c.TaskTimeout = 30 * time.Minute
	}
	if c.TerminalTimeout <= 0 {
		// 15m (was 5m) so apt-get install of large packages (r-base,
		// build-essential, …) finishes in one shot. Below ~10m, a single
		// `apt-get install` gets killed mid-flight, leaves dpkg-lock held,
		// and the agent burns iterations on retry → repeat-loop abort.
		c.TerminalTimeout = 15 * time.Minute
	}
	if c.PullPolicy == "" {
		c.PullPolicy = dockerenv.PullIfMissing
	}
	if c.ContainerTTL <= 0 {
		c.ContainerTTL = c.TaskTimeout + c.TerminalTimeout + 5*time.Minute
	}
	if c.VerifierEnv == nil {
		// Verifier runs uv/pip on the host's behalf — eval-side infrastructure,
		// not agent capability. Default-fill so it works on China networks
		// without the user having to know about uv's GitHub fetch.
		c.VerifierEnv = make(map[string]string, len(DefaultMirrorEnv))
		for k, v := range DefaultMirrorEnv {
			c.VerifierEnv[k] = v
		}
	}
	if c.EnvFactory == nil {
		c.EnvFactory = c.defaultEnvFactory()
	}
}

func (c *Config) defaultEnvFactory() EnvFactory {
	cfg := *c
	proxyEnv, extraRunArgs, proxyErr := proxyEnvFor(cfg.ProxyURL, cfg.MirrorEnv)
	return func(task dataset.Task) (sandboxenv.ExecutionEnvironment, error) {
		if proxyErr != nil {
			return nil, fmt.Errorf("terminalbench: task %s: %w", task.Name, proxyErr)
		}
		image := strings.TrimSpace(task.DockerImage)
		if image == "" {
			return nil, fmt.Errorf("terminalbench: task %s: docker_image is empty", task.Name)
		}
		// Merge mirror + proxy env. Proxy keys win on collision so an
		// explicit --proxy can override a stale HTTP_PROXY in MirrorEnv.
		mergedEnv := make(map[string]string, len(cfg.MirrorEnv)+len(proxyEnv))
		for k, v := range cfg.MirrorEnv {
			mergedEnv[k] = v
		}
		for k, v := range proxyEnv {
			mergedEnv[k] = v
		}
		// Leave DefaultWorkdir empty: dockerenv inspects the image and uses
		// the WORKDIR baked into it (TB2 tasks each pin their own —
		// /workspace, /build, /root, …). Hard-coding /app here would
		// land the agent in an empty directory for most tasks.
		return dockerenv.New(dockerenv.Config{
			Image:          image,
			DockerBinary:   cfg.DockerBinary,
			NamePrefix:     "saker-tb2",
			NetworkMode:    cfg.NetworkMode,
			PullPolicy:     cfg.PullPolicy,
			ContainerTTL:   cfg.ContainerTTL,
			DefaultTimeout: cfg.TerminalTimeout,
			ExtraEnv:       mergedEnv,
			ExtraRunArgs:   extraRunArgs,
		}), nil
	}
}

// proxyEnvFor builds the env-var map and docker-run argv tail for routing
// container traffic through a host-side HTTP proxy.
//
// Returns (env, extraRunArgs, err):
//   - env: HTTP_PROXY/HTTPS_PROXY/NO_PROXY pairs (upper + lowercase forms)
//   - extraRunArgs: ["--add-host=host.docker.internal:host-gateway"] when the
//     URL points at a loopback host; empty otherwise.
//   - err: parse failure on a malformed URL.
//
// rawURL == "" disables proxying (returns zero values, no error).
func proxyEnvFor(rawURL string, mirror map[string]string) (map[string]string, []string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, nil, nil
	}
	normalized, rewrote, err := normalizeProxyURL(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid proxy url %q: %w", rawURL, err)
	}
	noProxy := buildNoProxy(mirror)
	env := map[string]string{
		"HTTP_PROXY":  normalized,
		"HTTPS_PROXY": normalized,
		"http_proxy":  normalized,
		"https_proxy": normalized,
		"NO_PROXY":    noProxy,
		"no_proxy":    noProxy,
	}
	var extraRunArgs []string
	if rewrote {
		// Linux engines don't auto-publish host.docker.internal the way
		// Docker Desktop does; --add-host wires it to the bridge gateway
		// so the container can reach the host's proxy listener.
		extraRunArgs = []string{"--add-host=host.docker.internal:host-gateway"}
	}
	return env, extraRunArgs, nil
}

// normalizeProxyURL rewrites loopback hosts (127.0.0.1, localhost, ::1) to
// host.docker.internal so a container's outbound HTTP_PROXY actually targets
// the host. Returns (normalizedURL, rewroteHost, err).
//
// Accepts bare host:port (no scheme) for ergonomics ("127.0.0.1:7890" →
// "http://host.docker.internal:7890"); url.Parse alone treats those as a
// relative path and silently produces a useless value.
func normalizeProxyURL(raw string) (string, bool, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, err
	}
	if u.Host == "" {
		return "", false, fmt.Errorf("missing host")
	}
	host := u.Hostname()
	rewrote := false
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1", "0.0.0.0":
		port := u.Port()
		if port == "" {
			u.Host = "host.docker.internal"
		} else {
			u.Host = "host.docker.internal:" + port
		}
		rewrote = true
	}
	return u.String(), rewrote, nil
}

// buildNoProxy returns a comma-separated NO_PROXY list seeded with the
// loopback addresses (so the container's own localhost stays direct) and any
// hostnames found in mirror env values. This keeps China-friendly mirrors on
// the fast direct path even when --proxy routes everything else abroad.
func buildNoProxy(mirror map[string]string) string {
	parts := []string{"localhost", "127.0.0.1", "::1"}
	seen := map[string]struct{}{}
	for _, p := range parts {
		seen[p] = struct{}{}
	}
	keys := make([]string, 0, len(mirror))
	for k := range mirror {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := mirror[k]
		host := hostFromMaybeURL(v)
		if host == "" {
			continue
		}
		if _, dup := seen[host]; dup {
			continue
		}
		seen[host] = struct{}{}
		parts = append(parts, host)
	}
	return strings.Join(parts, ",")
}

// hostFromMaybeURL extracts the hostname from a value that might be a URL or
// already a bare hostname (PIP_TRUSTED_HOST is the latter). Returns "" when
// the input has no usable host.
func hostFromMaybeURL(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.Contains(v, "://") {
		// Bare host, possibly with a path suffix.
		return strings.SplitN(v, "/", 2)[0]
	}
	u, err := url.Parse(v)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// TaskResult is one task's outcome.
type TaskResult struct {
	Name                string        `json:"name"`
	Category            string        `json:"category,omitempty"`
	Pass                bool          `json:"pass"`
	Score               float64       `json:"score"`
	Skipped             bool          `json:"skipped,omitempty"`
	SkipReason          string        `json:"skip_reason,omitempty"`
	Iterations          int           `json:"iterations,omitempty"`
	StartedAt           time.Time     `json:"started_at"`
	Duration            time.Duration `json:"duration_ns"`
	Stage               string        `json:"stage,omitempty"`
	ErrorMsg            string        `json:"error,omitempty"`
	VerifierLog         string        `json:"verifier_log,omitempty"`
	VerifierLogPath     string        `json:"verifier_log_path,omitempty"`
	VerifierRan         bool          `json:"verifier_ran,omitempty"`
	TestsPassed         int           `json:"tests_passed,omitempty"`
	TestsTotal          int           `json:"tests_total,omitempty"`
	InputTokens         int           `json:"input_tokens,omitempty"`
	OutputTokens        int           `json:"output_tokens,omitempty"`
	CacheReadTokens     int           `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int           `json:"cache_creation_tokens,omitempty"`
	IterationTokens     []TokenSample `json:"iteration_tokens,omitempty"`
	StopReason          string        `json:"stop_reason,omitempty"`
	ImageDigest         string        `json:"image_digest,omitempty"`
	TranscriptPath      string        `json:"transcript_path,omitempty"`
}

// TokenSample is one model.Generate call's contribution to cumulative usage.
// Captured per iteration so a regression analyst can spot context-window
// runaway, cache breakage, or a single unusually-large turn without rerunning.
type TokenSample struct {
	Iter          int `json:"iter"`
	Input         int `json:"input"`
	Output        int `json:"output"`
	CacheRead     int `json:"cache_read,omitempty"`
	CacheCreation int `json:"cache_creation,omitempty"`
}

// verifierLogInlineCap is the maximum bytes of verifier output we embed in
// results.jsonl. The full untruncated stream is persisted to
// <output>/transcripts/<task>.verifier.log via writeVerifierLog so we never
// lose pytest output to truncation again. 32 KiB keeps results.jsonl
// human-skimmable while still capturing the tail of most pytest sessions.
const verifierLogInlineCap = 32 * 1024

// CategoryScore aggregates results within one category.
type CategoryScore struct {
	Category string  `json:"category"`
	Total    int     `json:"total"`
	Passed   int     `json:"passed"`
	Skipped  int     `json:"skipped"`
	PassRate float64 `json:"pass_rate"`
}

// AggregateScore is the dataset-wide summary.
type AggregateScore struct {
	Total    int     `json:"total"`
	Passed   int     `json:"passed"`
	Failed   int     `json:"failed"`
	Errored  int     `json:"errored"`
	Skipped  int     `json:"skipped"`
	PassRate float64 `json:"pass_rate"`
}

// ConfigSnapshot is the subset of Config we persist alongside the report.
type ConfigSnapshot struct {
	Provider        string        `json:"provider,omitempty"`
	Model           string        `json:"model,omitempty"`
	Concurrency     int           `json:"concurrency"`
	MaxIterations   int           `json:"max_iterations"`
	MaxTokens       int           `json:"max_tokens,omitempty"`
	MaxBudgetUSD    float64       `json:"max_budget_usd,omitempty"`
	TaskTimeout     time.Duration `json:"task_timeout_ns"`
	TerminalTimeout time.Duration `json:"terminal_timeout_ns"`
	PullPolicy      string        `json:"pull_policy,omitempty"`
	NetworkMode     string        `json:"network_mode,omitempty"`
	// Build-identity fields. Help baseline diffs distinguish "agent code
	// changed" from "agent code identical, model behavior drifted".
	SakerCommit  string `json:"saker_commit,omitempty"`
	GoVersion    string `json:"go_version,omitempty"`
	WithMirror   bool   `json:"with_mirror,omitempty"`
	VerifierMirr bool   `json:"verifier_mirror,omitempty"`
	ProxyURL     string `json:"proxy_url,omitempty"`
}

// Report is the structured output written to report.json.
type Report struct {
	Dataset    string          `json:"dataset"`
	OutputDir  string          `json:"output_dir,omitempty"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	Duration   time.Duration   `json:"duration_ns"`
	Aggregate  AggregateScore  `json:"aggregate"`
	Categories []CategoryScore `json:"categories"`
	Results    []TaskResult    `json:"results"`
	Config     ConfigSnapshot  `json:"config"`
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
		},
	}

	if err := writeReport(outputDir, report); err != nil {
		return report, err
	}
	return report, nil
}

// runOne executes the per-task pipeline. It NEVER returns an error: every
// failure is folded into TaskResult so the worker pool can keep going and the
// JSONL stream stays well-formed.
func (r *Runner) runOne(ctx context.Context, task dataset.Task) (res TaskResult) {
	res = TaskResult{
		Name:      task.Name,
		Category:  task.Category,
		StartedAt: time.Now(),
	}
	// Named return is load-bearing: the deferred Duration mutation is only
	// observable to callers because `res` is the actual return slot. With an
	// unnamed return, `return res` would copy the struct before the defer
	// fired, leaving Duration=0 in the report — that's how this stayed broken.
	defer func() { res.Duration = time.Since(res.StartedAt) }()

	if strings.TrimSpace(task.SkipReason) != "" && r.cfg.SkipIncompatible {
		res.Skipped = true
		res.SkipReason = task.SkipReason
		res.Stage = "skip"
		return res
	}

	taskCtx, cancel := context.WithTimeout(ctx, r.taskCap(task))
	defer cancel()

	// All builtin tools (bash/file/grep/glob) call PrepareSession with the
	// session id derived from ctx. Without this, they fall back to
	// "default" and dockerenv spawns a SECOND container — agent edits land
	// there while the verifier runs in the runner's task container, so
	// changes never reach test.sh. Pin the session id to task.Name so the
	// dockerenv cache returns the SAME container across runner + tools.
	taskCtx = context.WithValue(taskCtx, middleware.SessionIDContextKey, task.Name)

	env, err := r.cfg.EnvFactory(task)
	if err != nil {
		res.Stage = "env-init"
		res.ErrorMsg = err.Error()
		return res
	}

	ps, err := env.PrepareSession(taskCtx, sandboxenv.SessionContext{SessionID: task.Name})
	if err != nil {
		res.Stage = "prepare-session"
		res.ErrorMsg = err.Error()
		return res
	}
	// dockerenv populates Meta["image_digest"] when the local image has a
	// RepoDigest (i.e. it was pulled from a registry). Locally-built images
	// have no digest — we leave the field empty, which still serializes
	// cleanly thanks to omitempty.
	if ps != nil && ps.Meta != nil {
		if d, ok := ps.Meta["image_digest"].(string); ok {
			res.ImageDigest = d
		}
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 1*time.Minute)
		_ = env.CloseSession(closeCtx, ps)
		closeCancel()
	}()

	uploader, _ := env.(archiveUploader)
	if uploader == nil {
		res.Stage = "env-upload-capability"
		res.ErrorMsg = "execution environment does not support tar uploads (CopyArchiveTo)"
		return res
	}

	// guestRoot is the in-container cwd we treat as "the agent's workspace".
	// dockerenv now publishes the image-declared WORKDIR via PreparedSession,
	// so we trust that over the legacy /app fallback. environment.tar must
	// land here too — TB2 tasks expect the tarball contents at the same
	// place the test.sh later treats as cwd.
	guestRoot := defaultGuestWorkdir
	if ps != nil && strings.TrimSpace(ps.GuestCwd) != "" {
		guestRoot = ps.GuestCwd
	}

	if task.EnvironmentTar != "" {
		envFile, openErr := task.OpenEnvironment()
		if openErr != nil {
			res.Stage = "open-environment-tar"
			res.ErrorMsg = openErr.Error()
			return res
		}
		if envFile != nil {
			uploadErr := uploader.CopyArchiveTo(taskCtx, ps, guestRoot, envFile)
			envFile.Close()
			if uploadErr != nil {
				res.Stage = "upload-environment"
				res.ErrorMsg = uploadErr.Error()
				return res
			}
		}
	}

	registry := tool.NewRegistry()
	if err := registerBuiltinTools(registry, env, guestRoot); err != nil {
		res.Stage = "register-tools"
		res.ErrorMsg = err.Error()
		return res
	}
	exec := tool.NewExecutor(registry, nil)

	history := message.NewHistory()
	mdl, err := r.cfg.ModelFactory(taskCtx)
	if err != nil {
		res.Stage = "model-init"
		res.ErrorMsg = err.Error()
		return res
	}

	bridge := newModelBridge(mdl, history, r.cfg.SystemPrompt, task.Instruction, availableTools(registry))
	toolExec := newHistoryToolExecutor(exec, history, guestRoot)

	ag, err := agent.New(bridge, toolExec, agent.Options{
		MaxIterations:       r.cfg.MaxIterations,
		Timeout:             r.taskAgentCap(task),
		RepeatLoopThreshold: r.cfg.RepeatLoopThreshold,
		MaxBudgetUSD:        r.cfg.MaxBudgetUSD,
		MaxTokens:           r.cfg.MaxTokens,
		ModelName:           r.cfg.ModelName,
	})
	if err != nil {
		res.Stage = "agent-init"
		res.ErrorMsg = err.Error()
		return res
	}

	agentCtx := agent.NewContext()
	finalOut, runErr := ag.Run(taskCtx, agentCtx)
	if runErr != nil {
		res.Stage = "agent-run"
		res.ErrorMsg = runErr.Error()
		// Fall through — partial completion can still pass tests.
	}
	res.Iterations = agentCtx.Iteration + 1
	usage := bridge.Usage()
	res.InputTokens = usage.InputTokens
	res.OutputTokens = usage.OutputTokens
	res.CacheReadTokens = usage.CacheReadTokens
	res.CacheCreationTokens = usage.CacheCreationTokens
	if calls := bridge.PerCallUsage(); len(calls) > 0 {
		res.IterationTokens = make([]TokenSample, len(calls))
		for i, u := range calls {
			res.IterationTokens[i] = TokenSample{
				Iter:          i + 1,
				Input:         u.InputTokens,
				Output:        u.OutputTokens,
				CacheRead:     u.CacheReadTokens,
				CacheCreation: u.CacheCreationTokens,
			}
		}
	}
	// Prefer the agent-level structured StopReason when set (max_budget,
	// max_tokens, max_iterations, repeat_loop, aborted_*). It carries the
	// loop's own decision, which is more actionable than the model's
	// "end_turn"/"stop" string. Fall back to the model-level reason.
	if finalOut != nil && finalOut.StopReason != "" && finalOut.StopReason != agent.StopReasonCompleted {
		res.StopReason = string(finalOut.StopReason)
	} else {
		res.StopReason = bridge.StopReason()
	}
	// Capture the verbatim provider failure into the transcript so post-mortem
	// analysis doesn't have to cross-reference results.jsonl. Stored as a
	// synthetic "system" entry to keep the file replayable.
	if runErr != nil {
		if lastErr := bridge.LastError(); lastErr != nil {
			history.Append(message.Message{
				Role:    "system",
				Content: fmt.Sprintf("[runner] model.Generate failed: %s", lastErr.Error()),
			})
		} else {
			history.Append(message.Message{
				Role:    "system",
				Content: fmt.Sprintf("[runner] agent loop aborted: %s (stop_reason=%s)", runErr.Error(), res.StopReason),
			})
		}
	}
	if !r.cfg.DisableTranscripts {
		if path, terr := writeTranscript(r.cfg.OutputDir, task.Name, history.All()); terr != nil && res.ErrorMsg == "" {
			res.Stage = "write-transcript"
			res.ErrorMsg = terr.Error()
		} else if path != "" {
			res.TranscriptPath = path
		}
	}

	if verifyErr := runVerifier(taskCtx, env, uploader, ps, task, &res, r.cfg.TerminalTimeout, r.cfg.OutputDir, r.cfg.VerifierEnv); verifyErr != nil && res.ErrorMsg == "" {
		res.Stage = "verify"
		res.ErrorMsg = verifyErr.Error()
	}
	return res
}

// writeTranscript dumps the conversation history of one task into
// <outputDir>/transcripts/<task>.jsonl. Returns the absolute path on
// success, "" when there is nothing to persist.
func writeTranscript(outputDir, taskName string, msgs []message.Message) (string, error) {
	if strings.TrimSpace(outputDir) == "" || strings.TrimSpace(taskName) == "" || len(msgs) == 0 {
		return "", nil
	}
	dir := filepath.Join(outputDir, "transcripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("transcripts dir: %w", err)
	}
	path := filepath.Join(dir, sanitizeTranscriptFilename(taskName)+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create transcript: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i, msg := range msgs {
		entry := map[string]any{
			"index":   i,
			"role":    msg.Role,
			"content": msg.Content,
		}
		if msg.ReasoningContent != "" {
			entry["reasoning_content"] = msg.ReasoningContent
		}
		if len(msg.ToolCalls) > 0 {
			entry["tool_calls"] = msg.ToolCalls
		}
		if err := enc.Encode(entry); err != nil {
			return "", fmt.Errorf("encode transcript line %d: %w", i, err)
		}
	}
	return path, nil
}

// writeVerifierLog persists the *full* untruncated verifier output (stdout
// merged with stderr) to <outputDir>/transcripts/<task>.verifier.log. Empty
// inputs return ("", nil) so callers can ignore the result without
// branching. Errors are returned but the runner deliberately treats them as
// non-fatal — losing a debug log shouldn't fail an otherwise passing task.
func writeVerifierLog(outputDir, taskName, full string) (string, error) {
	if strings.TrimSpace(outputDir) == "" || strings.TrimSpace(taskName) == "" || full == "" {
		return "", nil
	}
	dir := filepath.Join(outputDir, "transcripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("transcripts dir: %w", err)
	}
	path := filepath.Join(dir, sanitizeTranscriptFilename(taskName)+".verifier.log")
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		return "", fmt.Errorf("write verifier log: %w", err)
	}
	return path, nil
}

// tailBytes returns the last `cap` bytes of s, prefixed with a `...truncated
// (N bytes omitted)...\n` marker when truncation actually happens. Returns s
// unchanged when within the cap or when cap <= 0.
func tailBytes(s string, cap int) string {
	if cap <= 0 || len(s) <= cap {
		return s
	}
	skipped := len(s) - cap
	return fmt.Sprintf("...truncated (%d bytes omitted, see verifier_log_path for full output)...\n%s",
		skipped, s[skipped:])
}

// sanitizeTranscriptFilename replaces filesystem-hostile chars in a task name
// so it can be used as a flat filename. Keeps it readable for humans.
func sanitizeTranscriptFilename(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "task"
	}
	return string(out)
}

// taskCap is the wall-clock bound for one task: model loop + verifier.
func (r *Runner) taskCap(task dataset.Task) time.Duration {
	cap := r.cfg.TaskTimeout
	if task.AgentTimeout > 0 && task.AgentTimeout < cap {
		cap = task.AgentTimeout
	}
	terminal := r.cfg.TerminalTimeout
	if task.TerminalTimeout > 0 && task.TerminalTimeout < terminal {
		terminal = task.TerminalTimeout
	}
	return cap + 2*terminal
}

func (r *Runner) taskAgentCap(task dataset.Task) time.Duration {
	cap := r.cfg.TaskTimeout
	if task.AgentTimeout > 0 && task.AgentTimeout < cap {
		cap = task.AgentTimeout
	}
	return cap
}

// runVerifier uploads tests.tar, executes the verifier command and reads
// /logs/verifier/reward.txt. Records score/pass/log on res.
//
// verifierEnv is injected via `docker exec -e ...` (per-call), so the agent's
// shell never observes these vars — keeps the eval signal honest while still
// letting the verifier's uv/pip reach mirrors on a constrained network.
func runVerifier(ctx context.Context, env sandboxenv.ExecutionEnvironment, uploader archiveUploader, ps *sandboxenv.PreparedSession, task dataset.Task, res *TaskResult, terminalTimeout time.Duration, outputDir string, verifierEnv map[string]string) error {
	if task.TestsTar == "" {
		return errors.New("terminalbench: tests_tar missing")
	}
	testsFile, err := task.OpenTests()
	if err != nil {
		return fmt.Errorf("open tests_tar: %w", err)
	}
	defer testsFile.Close()

	if err := uploader.CopyArchiveTo(ctx, ps, defaultTestsDir, testsFile); err != nil {
		return fmt.Errorf("upload tests: %w", err)
	}

	if _, err := env.RunCommand(ctx, ps, sandboxenv.CommandRequest{
		Command: "mkdir -p " + defaultLogsDir,
		Timeout: 30 * time.Second,
		Env:     verifierEnv,
	}); err != nil {
		return fmt.Errorf("ensure verifier dir: %w", err)
	}

	verifierCmd := strings.TrimSpace(task.TestSh)
	if verifierCmd == "" || verifierCmd == "bash test.sh" {
		verifierCmd = defaultVerifierCmd
	}

	verifyTimeout := terminalTimeout * 2
	if task.TerminalTimeout > 0 {
		candidate := task.TerminalTimeout * 2
		if candidate < verifyTimeout {
			verifyTimeout = candidate
		}
	}

	verResult, err := env.RunCommand(ctx, ps, sandboxenv.CommandRequest{
		Command: verifierCmd,
		Timeout: verifyTimeout,
		Env:     verifierEnv,
	})
	if err != nil {
		return fmt.Errorf("run verifier: %w", err)
	}
	if verResult != nil {
		full := verResult.Stdout + verResult.Stderr
		// pytest results land at the *tail*; keep the last verifierLogInlineCap
		// bytes so apt-install spam doesn't crowd out the assertion summary.
		res.VerifierLog = tailBytes(strings.TrimSpace(full), verifierLogInlineCap)
		if path, lerr := writeVerifierLog(outputDir, task.Name, full); lerr == nil && path != "" {
			res.VerifierLogPath = path
		}
		passed, total := parsePytestSummary(full)
		res.TestsPassed = passed
		res.TestsTotal = total
	}

	rewardPath := filepath.Join(defaultLogsDir, defaultRewardFilename)
	rewardData, err := env.ReadFile(ctx, ps, rewardPath)
	if err != nil {
		return fmt.Errorf("read reward: %w", err)
	}
	score, err := parseReward(string(rewardData))
	if err != nil {
		return err
	}
	res.Score = score
	res.Pass = score >= defaultPassScore
	res.VerifierRan = true
	return nil
}

// pytestSummaryRE matches pytest's footer line, e.g.:
//
//	========================= 1 failed, 8 passed in 1.27s ==========================
//	========================= 9 passed in 1.27s ==========================
//	==================== 2 failed, 7 passed, 1 skipped in 0.42s ====================
//
// Captures (failed?, errors?, passed?, skipped?). Best-effort: tasks that
// don't use pytest leave TestsPassed/TestsTotal at zero.
var pytestSummaryRE = regexp.MustCompile(`(?:(\d+) failed)?(?:, )?(?:(\d+) errors?)?(?:, )?(?:(\d+) passed)?(?:, )?(?:(\d+) skipped)?\s+in\s+[\d.]+s`)

// parsePytestSummary scans `log` for pytest's summary footer and returns
// (passed, total). total = passed + failed + errors (skipped excluded so the
// ratio reflects actionable signal). Returns (0, 0) if not parseable.
func parsePytestSummary(log string) (passed, total int) {
	matches := pytestSummaryRE.FindAllStringSubmatch(log, -1)
	if len(matches) == 0 {
		return 0, 0
	}
	last := matches[len(matches)-1]
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	failed := atoi(last[1])
	errored := atoi(last[2])
	passed = atoi(last[3])
	if failed+errored+passed == 0 {
		return 0, 0
	}
	return passed, failed + errored + passed
}

// parseReward accepts "1.0", "1", "0.75", "0.0\n", etc.
func parseReward(raw string) (float64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, errors.New("terminalbench: reward.txt is empty")
	}
	score, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, fmt.Errorf("terminalbench: parse reward %q: %w", trimmed, err)
	}
	return score, nil
}

// registerBuiltinTools wires the standard set of file/bash tools, each bound
// to the per-task ExecutionEnvironment so all calls execute inside the
// container rather than on the evaluator host.
func registerBuiltinTools(reg *tool.Registry, env sandboxenv.ExecutionEnvironment, root string) error {
	sandbox := security.NewDisabledSandbox()

	bash := toolbuiltin.NewBashToolWithSandbox(root, sandbox)
	bash.SetEnvironment(env)
	bash.AllowShellMetachars(true)
	if err := reg.Register(bash); err != nil {
		return err
	}

	read := toolbuiltin.NewReadToolWithSandbox(root, sandbox)
	read.SetEnvironment(env)
	if err := reg.Register(read); err != nil {
		return err
	}

	write := toolbuiltin.NewWriteToolWithSandbox(root, sandbox)
	write.SetEnvironment(env)
	if err := reg.Register(write); err != nil {
		return err
	}

	edit := toolbuiltin.NewEditToolWithSandbox(root, sandbox)
	edit.SetEnvironment(env)
	if err := reg.Register(edit); err != nil {
		return err
	}

	grep := toolbuiltin.NewGrepToolWithSandbox(root, sandbox)
	grep.SetEnvironment(env)
	if err := reg.Register(grep); err != nil {
		return err
	}

	glob := toolbuiltin.NewGlobToolWithSandbox(root, sandbox)
	glob.SetEnvironment(env)
	if err := reg.Register(glob); err != nil {
		return err
	}
	return nil
}

func availableTools(reg *tool.Registry) []model.ToolDefinition {
	if reg == nil {
		return nil
	}
	tools := reg.List()
	defs := make([]model.ToolDefinition, 0, len(tools))
	for _, impl := range tools {
		if impl == nil {
			continue
		}
		schema := impl.Schema()
		params := map[string]any{}
		if schema != nil {
			if schema.Type != "" {
				params["type"] = schema.Type
			}
			if len(schema.Properties) > 0 {
				params["properties"] = schema.Properties
			}
			if len(schema.Required) > 0 {
				params["required"] = append([]string(nil), schema.Required...)
			}
		}
		defs = append(defs, model.ToolDefinition{
			Name:        impl.Name(),
			Description: impl.Description(),
			Parameters:  params,
		})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

// aggregate computes the dataset-wide pass/fail/error/skip counts.
//
// Classification:
//   - Skipped: task.json declared a skip_reason
//   - Passed:  verifier ran and score >= passScore
//   - Errored: infrastructure failure — verifier never produced a reward
//     (sandbox start, dataset upload, verifier crash). The agent loop hitting
//     a deadline or model_error is NOT errored if the verifier still ran;
//     that's just a "failed" run with partial work.
//   - Failed:  verifier ran but did not pass (any partial credit, any 0)
func aggregate(results []TaskResult) AggregateScore {
	agg := AggregateScore{Total: len(results)}
	for _, r := range results {
		switch {
		case r.Skipped:
			agg.Skipped++
		case r.Pass:
			agg.Passed++
		case !r.VerifierRan:
			agg.Errored++
		default:
			agg.Failed++
		}
	}
	denom := agg.Total - agg.Skipped
	if denom > 0 {
		agg.PassRate = float64(agg.Passed) / float64(denom)
	}
	return agg
}

// byCategory bins results by task category and reports per-bin pass rates.
func byCategory(results []TaskResult) []CategoryScore {
	buckets := map[string]*CategoryScore{}
	for _, r := range results {
		key := strings.TrimSpace(r.Category)
		if key == "" {
			key = "uncategorized"
		}
		bucket, ok := buckets[key]
		if !ok {
			bucket = &CategoryScore{Category: key}
			buckets[key] = bucket
		}
		bucket.Total++
		if r.Skipped {
			bucket.Skipped++
			continue
		}
		if r.Pass {
			bucket.Passed++
		}
	}
	out := make([]CategoryScore, 0, len(buckets))
	for _, b := range buckets {
		denom := b.Total - b.Skipped
		if denom > 0 {
			b.PassRate = float64(b.Passed) / float64(denom)
		}
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Category < out[j].Category })
	return out
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
