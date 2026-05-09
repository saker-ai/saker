// Package main: saker eval subcommand. Today only "terminalbench" is wired,
// but the dispatch is deliberately split out so other benchmarks (SWE-Bench,
// HumanEval, …) can grow alongside it without bloating run().
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cinience/saker/pkg/eval/terminalbench"
	modelpkg "github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/provider"
	"github.com/cinience/saker/pkg/sandbox/dockerenv"
)

// runEvalCommand is the entry point for `saker eval <subcommand>`.
func runEvalCommand(stdout, stderr io.Writer, args []string) error {
	if len(args) == 0 {
		printEvalUsage(stderr)
		return fmt.Errorf("eval: missing subcommand")
	}
	switch strings.ToLower(args[0]) {
	case "terminalbench", "tb2":
		return runEvalTerminalBench(stdout, stderr, args[1:])
	case "analyze", "diff":
		return runEvalAnalyze(stdout, stderr, args[1:])
	case "help", "-h", "--help":
		printEvalUsage(stdout)
		return nil
	default:
		printEvalUsage(stderr)
		return fmt.Errorf("eval: unknown subcommand %q", args[0])
	}
}

func printEvalUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: saker eval <subcommand> [flags]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "subcommands:")
	fmt.Fprintln(out, "  terminalbench   Run the Terminal-Bench 2 evaluation suite (alias: tb2)")
	fmt.Fprintln(out, "  analyze         Diff two report.json files (alias: diff)")
	fmt.Fprintln(out, "  help            Show this help message")
}

// runEvalTerminalBench is the implementation of `saker eval terminalbench`.
//
// Required flags:
//
//	--dataset <dir>   Path to a TB2 dataset root containing tasks/<name>/...
//
// Common knobs:
//
//	--output <dir>           Where report.json + summary.txt + results.jsonl land
//	--filter <glob>          Repeatable: include only matching task names
//	--exclude <glob>         Repeatable: skip matching task names
//	--concurrency <N>        Parallel tasks (default 1)
//	--max-iters <N>          Per-task agent loop cap (default 50)
//	--max-budget-usd <F>     Per-task USD budget cap (default 0 = disabled)
//	--max-tokens <N>         Per-task token cap (input+output, default 0 = disabled)
//	--task-timeout <dur>     Per-task wall clock cap (default 30m)
//	--terminal-timeout <dur> Per-command/verifier cap (default 5m)
//	--provider <name>        Model provider (anthropic|openai), defaults to env
//	--model <name>           Model name override
//	--system <prompt>        Override system prompt
//	--pull <policy>          Docker image pull policy: always|if-missing|never
//	--network <mode>         docker run --network value (default unset)
//	--docker-binary <path>   Override docker CLI path
//	--skip-incompatible      Skip tasks whose task.json sets a skip_reason
//	--verbose                Print per-task progress to stderr
//	--repeat-threshold <N>   Identical-call abort threshold (default 5, -1 disables)
//	--no-transcripts         Skip per-task conversation dumps
//	--with-mirror            Inject China-friendly mirror env vars into the
//	                         container (visible to the agent's shell). Off by
//	                         default — eval baseline = bare saker.
//	--mirror KEY=VAL         Add an agent-visible mirror env var (repeatable).
//	                         With --with-mirror also active, KEY= deletes a default.
//	--no-verifier-mirror     Disable the per-call mirror env injected ONLY
//	                         into verifier RunCommand calls. On by default
//	                         because the verifier itself runs uv/pip and
//	                         needs reachable indexes, but it's fully isolated
//	                         from the agent (per-call docker exec -e, not
//	                         container-level).
//	--proxy <URL>            HTTP(S) proxy URL injected into containers (e.g. http://127.0.0.1:7890)
func runEvalTerminalBench(stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("saker eval terminalbench", flag.ContinueOnError)
	fs.SetOutput(stderr)

	dataset := fs.String("dataset", "", "Path to TB2 dataset root (required)")
	output := fs.String("output", "", "Output directory (default: temp dir under $TMPDIR)")
	concurrency := fs.Int("concurrency", 1, "Parallel tasks")
	maxIters := fs.Int("max-iters", 50, "Per-task agent loop iteration cap")
	maxBudgetUSD := fs.Float64("max-budget-usd", 0, "Per-task USD budget cap (0 disables; requires --provider/--model with known pricing)")
	maxTokens := fs.Int("max-tokens", 0, "Per-task cumulative input+output token cap (0 disables)")
	taskTimeout := fs.Duration("task-timeout", 30*time.Minute, "Per-task wall-clock cap")
	terminalTimeout := fs.Duration("terminal-timeout", 15*time.Minute, "Per-command verifier cap (15m default leaves headroom for apt-get install of large packages)")
	providerName := fs.String("provider", "", "Model provider (anthropic|openai)")
	modelName := fs.String("model", "", "Model name override")
	systemPrompt := fs.String("system", "", "System prompt override")
	pull := fs.String("pull", "if-missing", "Docker image pull policy: always|if-missing|never")
	network := fs.String("network", "", "docker run --network mode")
	dockerBinary := fs.String("docker-binary", "", "docker CLI path override")
	skipIncompat := fs.Bool("skip-incompatible", false, "Skip tasks whose task.json declares skip_reason")
	verbose := fs.Bool("verbose", false, "Print per-task progress to stderr")
	repeatThreshold := fs.Int("repeat-threshold", 0, "Identical consecutive tool calls before agent aborts (0=default 5, -1=disabled)")
	noTranscripts := fs.Bool("no-transcripts", false, "Skip writing per-task <task>.jsonl conversation dumps under <output>/transcripts/")
	withMirror := fs.Bool("with-mirror", false, "Opt in to inject China-friendly mirror env vars (PIP_INDEX_URL, UV_*, HF_ENDPOINT, ...) from terminalbench.DefaultMirrorEnv into containers. Off by default — the eval baseline is bare saker")
	noVerifierMirror := fs.Bool("no-verifier-mirror", false, "Disable per-call mirror env injection during verifier execution. On by default; verifier env is isolated from the agent's shell (docker exec -e), so it doesn't pollute the eval signal")
	var mirrorOverrides evalMultiValue
	fs.Var(&mirrorOverrides, "mirror", "Add a mirror env var as KEY=VAL (repeatable). With --with-mirror, KEY= drops a default key")
	proxyURL := fs.String("proxy", "", "HTTP(S) proxy URL routed into containers (e.g. http://127.0.0.1:7890 for local Clash). Loopback hosts are auto-rewritten to host.docker.internal and --add-host is wired to host-gateway. Mirror domains are appended to NO_PROXY automatically.")

	var includes evalMultiValue
	var excludes evalMultiValue
	fs.Var(&includes, "filter", "Include only task names matching this glob (repeatable)")
	fs.Var(&excludes, "exclude", "Exclude task names matching this glob (repeatable)")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*dataset) == "" {
		fs.Usage()
		return fmt.Errorf("eval terminalbench: --dataset is required")
	}
	pullPolicy, err := parsePullPolicy(*pull)
	if err != nil {
		return err
	}

	prov, resolvedModel := provider.Detect(*providerName, *modelName, *systemPrompt)
	if prov == nil {
		return fmt.Errorf("eval terminalbench: failed to resolve model provider (set ANTHROPIC_API_KEY or pass --provider/--model)")
	}

	mirrorEnv, err := buildMirrorEnv(*withMirror, []string(mirrorOverrides))
	if err != nil {
		return err
	}
	var verifierEnv map[string]string
	if *noVerifierMirror {
		// Explicit empty (non-nil) suppresses applyDefaults' auto-fill.
		verifierEnv = map[string]string{}
	}

	cfg := terminalbench.Config{
		DatasetRoot:         mustAbs(*dataset),
		OutputDir:           mustAbs(*output),
		Include:             []string(includes),
		Exclude:             []string(excludes),
		Concurrency:         *concurrency,
		MaxIterations:       *maxIters,
		MaxBudgetUSD:        *maxBudgetUSD,
		MaxTokens:           *maxTokens,
		TaskTimeout:         *taskTimeout,
		TerminalTimeout:     *terminalTimeout,
		SystemPrompt:        *systemPrompt,
		SkipIncompatible:    *skipIncompat,
		Verbose:             *verbose,
		ProviderName:        strings.TrimSpace(*providerName),
		ModelName:           strings.TrimSpace(resolvedModel),
		ModelFactory:        providerToFactory(prov),
		DockerBinary:        strings.TrimSpace(*dockerBinary),
		NetworkMode:         strings.TrimSpace(*network),
		PullPolicy:          pullPolicy,
		RepeatLoopThreshold: *repeatThreshold,
		DisableTranscripts:  *noTranscripts,
		MirrorEnv:           mirrorEnv,
		VerifierEnv:         verifierEnv,
		ProxyURL:            strings.TrimSpace(*proxyURL),
	}

	runner, err := terminalbench.New(cfg)
	if err != nil {
		return err
	}

	tasks := runner.Tasks()
	fmt.Fprintf(stdout, "terminalbench: %d task(s) selected, output dir = %s\n", len(tasks), valueOr(cfg.OutputDir, "<temp>"))
	if *verbose {
		for _, task := range tasks {
			fmt.Fprintf(stderr, "  - %s (image=%s)\n", task.Name, task.DockerImage)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	report, err := runner.Run(ctx)
	if err != nil {
		// We still surface the partial report on disk; surfacing the error so
		// the caller's exit code reflects the failure.
		if report != nil {
			fmt.Fprintf(stdout, "partial report at %s\n", report.OutputDir)
		}
		return fmt.Errorf("eval terminalbench: %w", err)
	}

	agg := report.Aggregate
	denom := agg.Total - agg.Skipped
	fmt.Fprintf(stdout, "\n=== Terminal-Bench 2 results ===\n")
	fmt.Fprintf(stdout, "total=%d  passed=%d  failed=%d  errored=%d  skipped=%d\n",
		agg.Total, agg.Passed, agg.Failed, agg.Errored, agg.Skipped)
	if denom > 0 {
		fmt.Fprintf(stdout, "pass-rate=%.2f%% (%d / %d)\n", agg.PassRate*100, agg.Passed, denom)
	}
	fmt.Fprintf(stdout, "duration=%s  output=%s\n", report.Duration.Round(time.Second), report.OutputDir)
	for _, cat := range report.Categories {
		fmt.Fprintf(stdout, "  [%s] passed %d/%d (%.0f%%)\n", cat.Category, cat.Passed, cat.Total, cat.PassRate*100)
	}
	return nil
}

// providerToFactory bridges modelpkg.Provider into terminalbench.ModelFactory.
// Each task gets a fresh model.Model instance returned by the provider — which
// is fine, the underlying HTTP client is reused via the provider's own cache.
func providerToFactory(p modelpkg.Provider) terminalbench.ModelFactory {
	return func(ctx context.Context) (modelpkg.Model, error) {
		return p.Model(ctx)
	}
}

func parsePullPolicy(raw string) (dockerenv.PullPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "if-missing", "missing":
		return dockerenv.PullIfMissing, nil
	case "always":
		return dockerenv.PullAlways, nil
	case "never", "none":
		return dockerenv.PullNever, nil
	default:
		return "", fmt.Errorf("eval terminalbench: invalid --pull %q (expected always|if-missing|never)", raw)
	}
}

// mustAbs converts a relative path to absolute when non-empty. An empty
// string passes through so downstream defaults (temp dir, etc.) stay intact.
func mustAbs(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

func valueOr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// buildMirrorEnv resolves the final container env var map.
//
// Default (neither flag set) → nil: no mirror env vars injected, so the
// agent runs against bare saker (the eval baseline).
//
//	`--with-mirror`     → seed from terminalbench.DefaultMirrorEnv
//	`--mirror KEY=VAL`  → add/override a single env var
//	`--mirror KEY=`     → only meaningful with --with-mirror; drops a default
//	invalid form        → error before run starts
//
// Note: anything injected here is visible to the agent inside the container
// (e.g. `echo $PIP_INDEX_URL`), which is why both paths require an explicit
// flag.
func buildMirrorEnv(withDefaults bool, overrides []string) (map[string]string, error) {
	if !withDefaults && len(overrides) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(terminalbench.DefaultMirrorEnv)+len(overrides))
	if withDefaults {
		for k, v := range terminalbench.DefaultMirrorEnv {
			out[k] = v
		}
	}
	for _, raw := range overrides {
		eq := strings.IndexByte(raw, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("eval terminalbench: --mirror %q must be KEY=VAL", raw)
		}
		key := strings.TrimSpace(raw[:eq])
		val := raw[eq+1:]
		if key == "" {
			return nil, fmt.Errorf("eval terminalbench: --mirror %q has empty key", raw)
		}
		if val == "" {
			if !withDefaults {
				return nil, fmt.Errorf("eval terminalbench: --mirror %q (KEY= delete form) requires --with-mirror", raw)
			}
			delete(out, key)
			continue
		}
		out[key] = val
	}
	return out, nil
}

// evalMultiValue is the same shape as cmd/cli/main.go::multiValue but kept
// local so the eval subcommand can be plucked out without touching main.go's
// internals.
type evalMultiValue []string

func (m *evalMultiValue) String() string { return strings.Join(*m, ",") }

func (m *evalMultiValue) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if t := strings.TrimSpace(p); t != "" {
			*m = append(*m, t)
		}
	}
	return nil
}
