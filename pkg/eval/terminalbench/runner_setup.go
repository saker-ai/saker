// runner_setup.go: Config + factories, mirror/proxy env, builtin tool registration.
package terminalbench

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/eval/dataset"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/sandbox/dockerenv"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	"github.com/cinience/saker/pkg/security"
	"github.com/cinience/saker/pkg/tool"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
)

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

	// UseACP routes agent execution through a full Saker Runtime via the
	// ACP protocol instead of the bare modelBridge + historyToolExecutor.
	// The Runtime brings middleware, compaction, prompt-cache, hooks, and
	// failover — the entire product stack. Tool calls are routed back to
	// the Docker container through ACP capability callbacks.
	UseACP bool
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
		// default in cmd/saker/eval_terminalbench.go. The previous 30 was an
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
