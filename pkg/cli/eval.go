package cli

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

	"github.com/saker-ai/saker/pkg/eval/terminalbench"
	modelpkg "github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/provider"
	"github.com/saker-ai/saker/pkg/sandbox/dockerenv"
	"github.com/joho/godotenv"
)

func runEvalCommand(stdout, stderr io.Writer, args []string) error {
	if len(args) == 0 {
		printEvalUsage(stderr)
		return fmt.Errorf("eval: missing subcommand")
	}
	switch strings.ToLower(args[0]) {
	case "terminalbench", "tb2":
		return runEvalTerminalBench(stdout, stderr, args[1:])
	case "analyze", "diff":
		return terminalbench.RunAnalyze(stdout, stderr, args[1:])
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
	terminalTimeout := fs.Duration("terminal-timeout", 15*time.Minute, "Per-command verifier cap")
	providerName := fs.String("provider", "", "Model provider (anthropic|openai)")
	modelName := fs.String("model", "", "Model name override")
	systemPrompt := fs.String("system", "", "System prompt override")
	pull := fs.String("pull", "if-missing", "Docker image pull policy: always|if-missing|never")
	network := fs.String("network", "host", "docker run --network mode (default: host for internet access)")
	dockerBinary := fs.String("docker-binary", "", "docker CLI path override")
	skipIncompat := fs.Bool("skip-incompatible", false, "Skip tasks whose task.json declares skip_reason")
	verbose := fs.Bool("verbose", false, "Print per-task progress to stderr")
	repeatThreshold := fs.Int("repeat-threshold", 0, "Identical consecutive tool calls before agent aborts (0=default 5, -1=disabled)")
	noTranscripts := fs.Bool("no-transcripts", false, "Skip writing per-task conversation dumps")
	withMirror := fs.Bool("with-mirror", false, "Inject China-friendly mirror env vars into containers")
	noVerifierMirror := fs.Bool("no-verifier-mirror", false, "Disable per-call mirror env injection during verifier execution")
	var mirrorOverrides evalMultiValue
	fs.Var(&mirrorOverrides, "mirror", "Add a mirror env var as KEY=VAL (repeatable)")
	proxyURL := fs.String("proxy", "", "HTTP(S) proxy URL routed into containers")
	useACP := fs.Bool("acp", false, "Use full Saker Runtime via ACP protocol")
	envFile := fs.String("env", "", "Path to .env file for model/provider configuration")

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

	if ef := strings.TrimSpace(*envFile); ef != "" {
		if err := godotenv.Overload(ef); err != nil {
			return fmt.Errorf("eval terminalbench: load --env %q: %w", ef, err)
		}
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
		UseACP:              *useACP,
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
