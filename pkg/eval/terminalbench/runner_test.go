package terminalbench

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/eval/dataset"
	"github.com/cinience/saker/pkg/model"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
)

// stubEnv is a fake ExecutionEnvironment that records every call so tests can
// assert the runner pipeline (PrepareSession → CopyArchiveTo /app →
// agent loop → CopyArchiveTo /tests → RunCommand verifier → ReadFile reward
// → CloseSession).
type stubEnv struct {
	mu sync.Mutex

	prepareErr error
	uploadErr  error
	runErr     error
	readErr    error
	reward     string
	verifyOut  string
	verifyExit int

	prepared bool
	uploads  []uploadCall
	commands []sandboxenv.CommandRequest
	reads    []string
	writes   map[string][]byte
	closed   bool
}

type uploadCall struct {
	dest string
	body []byte
}

func newStubEnv(reward string) *stubEnv {
	return &stubEnv{reward: reward, writes: map[string][]byte{}}
}

func (s *stubEnv) PrepareSession(_ context.Context, sess sandboxenv.SessionContext) (*sandboxenv.PreparedSession, error) {
	if s.prepareErr != nil {
		return nil, s.prepareErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prepared = true
	return &sandboxenv.PreparedSession{
		SessionID:   sess.SessionID,
		GuestCwd:    "/app",
		SandboxType: "stub",
		Meta:        map[string]any{},
	}, nil
}

func (s *stubEnv) RunCommand(_ context.Context, _ *sandboxenv.PreparedSession, req sandboxenv.CommandRequest) (*sandboxenv.CommandResult, error) {
	if s.runErr != nil {
		return nil, s.runErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, req)
	return &sandboxenv.CommandResult{
		Stdout:   s.verifyOut,
		ExitCode: s.verifyExit,
		Duration: 5 * time.Millisecond,
	}, nil
}

func (s *stubEnv) ReadFile(_ context.Context, _ *sandboxenv.PreparedSession, path string) ([]byte, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = append(s.reads, path)
	if data, ok := s.writes[path]; ok {
		return data, nil
	}
	if filepath.Base(path) == defaultRewardFilename {
		return []byte(s.reward), nil
	}
	return nil, fmt.Errorf("stubEnv: no file at %s", path)
}

func (s *stubEnv) WriteFile(_ context.Context, _ *sandboxenv.PreparedSession, path string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes[path] = append([]byte(nil), data...)
	return nil
}

func (s *stubEnv) EditFile(_ context.Context, _ *sandboxenv.PreparedSession, _ sandboxenv.EditRequest) error {
	return nil
}

func (s *stubEnv) Glob(_ context.Context, _ *sandboxenv.PreparedSession, _ string) ([]string, error) {
	return nil, nil
}

func (s *stubEnv) Grep(_ context.Context, _ *sandboxenv.PreparedSession, _ sandboxenv.GrepRequest) ([]sandboxenv.GrepMatch, error) {
	return nil, nil
}

func (s *stubEnv) CloseSession(_ context.Context, _ *sandboxenv.PreparedSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// CopyArchiveTo satisfies the runner's archiveUploader interface.
func (s *stubEnv) CopyArchiveTo(_ context.Context, _ *sandboxenv.PreparedSession, destDir string, archive io.Reader) error {
	if s.uploadErr != nil {
		return s.uploadErr
	}
	body, _ := io.ReadAll(archive)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploads = append(s.uploads, uploadCall{dest: destDir, body: body})
	return nil
}

// stubModel returns a single canned response: assistant text, no tool calls,
// then Done. Keeps tests focused on the runner orchestration.
type stubModel struct {
	mu    sync.Mutex
	calls int
	reply string
}

func (m *stubModel) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	return nil, errors.New("stubModel: Complete not used")
}

func (m *stubModel) CompleteStream(_ context.Context, _ model.Request, cb model.StreamHandler) error {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	resp := &model.Response{
		Message:    model.Message{Role: "assistant", Content: m.reply},
		Usage:      model.Usage{InputTokens: 10, OutputTokens: 7, TotalTokens: 17},
		StopReason: "end_turn",
	}
	return cb(model.StreamResult{Final: true, Response: resp})
}

func writeTaskFixture(t *testing.T, root, name, instruction string) {
	t.Helper()
	dir := filepath.Join(root, "tasks", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	taskJSON := fmt.Sprintf(`{
		"name": %q,
		"category": "smoke",
		"instruction": %q,
		"docker_image": "stub:latest",
		"environment_tar": "environment.tar",
		"tests_tar": "tests.tar"
	}`, name, instruction)
	if err := os.WriteFile(filepath.Join(dir, "task.json"), []byte(taskJSON), 0o644); err != nil {
		t.Fatalf("write task.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "environment.tar"), []byte("ENV-TAR"), 0o644); err != nil {
		t.Fatalf("write environment.tar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests.tar"), []byte("TESTS-TAR"), 0o644); err != nil {
		t.Fatalf("write tests.tar: %v", err)
	}
}

func TestRunner_RunOne_PassesPipeline(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTaskFixture(t, root, "smoke-pass", "Print hello world.")

	env := newStubEnv("1.0\n")
	env.verifyOut = "PASS\n"

	cfg := Config{
		DatasetRoot:     root,
		OutputDir:       filepath.Join(root, "out"),
		Concurrency:     1,
		MaxIterations:   3,
		TaskTimeout:     30 * time.Second,
		TerminalTimeout: 5 * time.Second,
		ProviderName:    "stub",
		ModelName:       "stub-model",
		ModelFactory:    func(_ context.Context) (model.Model, error) { return &stubModel{reply: "done"}, nil },
		EnvFactory:      func(_ dataset.Task) (sandboxenv.ExecutionEnvironment, error) { return env, nil },
	}

	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	report, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if report.Aggregate.Total != 1 || report.Aggregate.Passed != 1 || report.Aggregate.PassRate != 1.0 {
		t.Fatalf("aggregate = %+v, want total=1 passed=1 rate=1.0", report.Aggregate)
	}
	if !env.prepared {
		t.Fatal("expected PrepareSession to be invoked")
	}
	if !env.closed {
		t.Fatal("expected CloseSession to be invoked")
	}
	if len(env.uploads) != 2 {
		t.Fatalf("expected 2 uploads, got %d", len(env.uploads))
	}
	if env.uploads[0].dest != defaultGuestWorkdir {
		t.Fatalf("first upload dest = %q, want %s", env.uploads[0].dest, defaultGuestWorkdir)
	}
	if env.uploads[1].dest != defaultTestsDir {
		t.Fatalf("second upload dest = %q, want %s", env.uploads[1].dest, defaultTestsDir)
	}
	if string(env.uploads[0].body) != "ENV-TAR" {
		t.Fatalf("env upload body = %q, want ENV-TAR", env.uploads[0].body)
	}
	if string(env.uploads[1].body) != "TESTS-TAR" {
		t.Fatalf("tests upload body = %q, want TESTS-TAR", env.uploads[1].body)
	}
	if len(env.commands) < 2 {
		t.Fatalf("expected >=2 RunCommand calls, got %d", len(env.commands))
	}
	if !findCommandContaining(env.commands, defaultLogsDir) {
		t.Fatalf("expected a command that ensures the verifier dir (%q) in %v", defaultLogsDir, commandStrings(env.commands))
	}
	if !findCommandContaining(env.commands, "test.sh") {
		t.Fatalf("expected a command that runs test.sh in %v", commandStrings(env.commands))
	}

	if len(report.Results) != 1 {
		t.Fatalf("Results len = %d, want 1", len(report.Results))
	}
	res := report.Results[0]
	if !res.Pass || res.Score != 1.0 {
		t.Fatalf("result = %+v, want pass score=1.0", res)
	}
	if res.InputTokens != 10 || res.OutputTokens != 7 {
		t.Fatalf("usage = (%d,%d), want (10,7)", res.InputTokens, res.OutputTokens)
	}
	if res.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q, want end_turn", res.StopReason)
	}
	if res.VerifierLog != "PASS" {
		t.Fatalf("VerifierLog = %q, want PASS", res.VerifierLog)
	}

	for _, name := range []string{"report.json", "summary.txt", "results.jsonl"} {
		if _, err := os.Stat(filepath.Join(cfg.OutputDir, name)); err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
	}
}

func TestRunner_RunOne_FailingReward(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTaskFixture(t, root, "smoke-fail", "Do something.")

	env := newStubEnv("0.0\n")
	cfg := Config{
		DatasetRoot:  root,
		OutputDir:    filepath.Join(root, "out"),
		ModelFactory: func(_ context.Context) (model.Model, error) { return &stubModel{reply: "done"}, nil },
		EnvFactory:   func(_ dataset.Task) (sandboxenv.ExecutionEnvironment, error) { return env, nil },
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	report, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Aggregate.Failed != 1 || report.Aggregate.Passed != 0 {
		t.Fatalf("aggregate = %+v, want failed=1 passed=0", report.Aggregate)
	}
	if report.Results[0].Pass {
		t.Fatal("expected pass=false for reward=0.0")
	}
}

func TestRunner_RunOne_PrepareError_RecordedAsErrored(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTaskFixture(t, root, "smoke-err", "Do something.")

	env := newStubEnv("")
	env.prepareErr = errors.New("boom")

	cfg := Config{
		DatasetRoot:  root,
		OutputDir:    filepath.Join(root, "out"),
		ModelFactory: func(_ context.Context) (model.Model, error) { return &stubModel{reply: "done"}, nil },
		EnvFactory:   func(_ dataset.Task) (sandboxenv.ExecutionEnvironment, error) { return env, nil },
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	report, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Aggregate.Errored != 1 {
		t.Fatalf("Errored = %d, want 1 (agg=%+v)", report.Aggregate.Errored, report.Aggregate)
	}
	if report.Results[0].Stage != "prepare-session" {
		t.Fatalf("Stage = %q, want prepare-session", report.Results[0].Stage)
	}
	if !strings.Contains(report.Results[0].ErrorMsg, "boom") {
		t.Fatalf("ErrorMsg = %q, want it to contain 'boom'", report.Results[0].ErrorMsg)
	}
}

func TestRunner_RunOne_SkipIncompatible(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "tasks", "skip-me")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{
		"docker_image": "x:latest",
		"tests_tar": "tests.tar",
		"environment_tar": "environment.tar",
		"skip_reason": "requires-gpu"
	}`
	if err := os.WriteFile(filepath.Join(dir, "task.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write task.json: %v", err)
	}
	_ = os.WriteFile(filepath.Join(dir, "environment.tar"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "tests.tar"), []byte("x"), 0o644)

	env := newStubEnv("1.0")
	cfg := Config{
		DatasetRoot:      root,
		OutputDir:        filepath.Join(root, "out"),
		SkipIncompatible: true,
		ModelFactory:     func(_ context.Context) (model.Model, error) { return &stubModel{reply: "done"}, nil },
		EnvFactory:       func(_ dataset.Task) (sandboxenv.ExecutionEnvironment, error) { return env, nil },
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	report, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Aggregate.Skipped != 1 {
		t.Fatalf("Skipped = %d, want 1 (agg=%+v)", report.Aggregate.Skipped, report.Aggregate)
	}
	if env.prepared {
		t.Fatal("PrepareSession should NOT be invoked for skipped task")
	}
}

// TestRunner_RunOne_RecordsDuration is a regression guard for a real bug:
// runOne used `defer func() { res.Duration = ... }()` with an *unnamed* return
// value, so every `return res` copied the struct before the defer fired. The
// resulting report.json had duration_ns=0 on every task, making the analyze
// diff's duration column useless. Now that the function uses a named return
// (`(res TaskResult)`), the deferred mutation is observable to callers.
//
// We assert across pass / fail / error / skip code paths because each one is
// a separate `return res` statement; the bug would silently come back if any
// of them were rewritten to a non-named return.
func TestRunner_RunOne_RecordsDuration(t *testing.T) {
	t.Parallel()

	type variant struct {
		name      string
		fixture   string
		reward    string
		prepareEr error
		skipReas  string
	}
	variants := []variant{
		{name: "pass", fixture: "dur-pass", reward: "1.0\n"},
		{name: "fail", fixture: "dur-fail", reward: "0.0\n"},
		{name: "error", fixture: "dur-err", reward: "1.0\n", prepareEr: errors.New("prepare-boom")},
		{name: "skip", fixture: "dur-skip", reward: "1.0\n", skipReas: "requires-gpu"},
	}
	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tc.skipReas != "" {
				dir := filepath.Join(root, "tasks", tc.fixture)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				body := fmt.Sprintf(`{"docker_image":"x:latest","tests_tar":"tests.tar","environment_tar":"environment.tar","skip_reason":%q}`, tc.skipReas)
				_ = os.WriteFile(filepath.Join(dir, "task.json"), []byte(body), 0o644)
				_ = os.WriteFile(filepath.Join(dir, "environment.tar"), []byte("x"), 0o644)
				_ = os.WriteFile(filepath.Join(dir, "tests.tar"), []byte("x"), 0o644)
			} else {
				writeTaskFixture(t, root, tc.fixture, "instruction")
			}

			env := newStubEnv(tc.reward)
			env.prepareErr = tc.prepareEr

			cfg := Config{
				DatasetRoot:      root,
				OutputDir:        filepath.Join(root, "out"),
				SkipIncompatible: tc.skipReas != "",
				ModelFactory:     func(_ context.Context) (model.Model, error) { return &stubModel{reply: "done"}, nil },
				EnvFactory:       func(_ dataset.Task) (sandboxenv.ExecutionEnvironment, error) { return env, nil },
			}
			r, err := New(cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			report, err := r.Run(context.Background())
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(report.Results) != 1 {
				t.Fatalf("Results len = %d, want 1", len(report.Results))
			}
			res := report.Results[0]
			if res.Duration <= 0 {
				t.Fatalf("Duration = %v, want > 0 (%s path) — defer-on-unnamed-return regression", res.Duration, tc.name)
			}
			if res.StartedAt.IsZero() {
				t.Fatalf("StartedAt is zero (%s path); duration math would be meaningless", tc.name)
			}
		})
	}
}

func TestNew_RequiresModelFactory(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{DatasetRoot: "x"}); err == nil {
		t.Fatal("expected error when ModelFactory is nil")
	}
}

func TestNew_RequiresTasksOrRoot(t *testing.T) {
	t.Parallel()
	_, err := New(Config{
		ModelFactory: func(_ context.Context) (model.Model, error) { return &stubModel{}, nil },
	})
	if err == nil {
		t.Fatal("expected error when neither DatasetRoot nor Tasks is provided")
	}
}

func TestRunner_WritesTranscriptByDefault(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTaskFixture(t, root, "with-transcript", "Just succeed.")

	env := newStubEnv("1.0\n")
	cfg := Config{
		DatasetRoot:  root,
		OutputDir:    filepath.Join(root, "out"),
		ModelFactory: func(_ context.Context) (model.Model, error) { return &stubModel{reply: "done"}, nil },
		EnvFactory:   func(_ dataset.Task) (sandboxenv.ExecutionEnvironment, error) { return env, nil },
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	report, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	res := report.Results[0]
	if res.TranscriptPath == "" {
		t.Fatal("TranscriptPath should be set when transcripts enabled")
	}
	data, err := os.ReadFile(res.TranscriptPath)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	// At minimum we expect the user prompt and the assistant reply.
	if !strings.Contains(string(data), `"role":"user"`) ||
		!strings.Contains(string(data), `"role":"assistant"`) {
		t.Fatalf("transcript missing user/assistant roles:\n%s", data)
	}
}

func TestRunner_DisableTranscripts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTaskFixture(t, root, "no-transcript", "Just succeed.")

	env := newStubEnv("1.0\n")
	cfg := Config{
		DatasetRoot:        root,
		OutputDir:          filepath.Join(root, "out"),
		DisableTranscripts: true,
		ModelFactory:       func(_ context.Context) (model.Model, error) { return &stubModel{reply: "done"}, nil },
		EnvFactory:         func(_ dataset.Task) (sandboxenv.ExecutionEnvironment, error) { return env, nil },
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	report, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Results[0].TranscriptPath != "" {
		t.Fatalf("TranscriptPath should be empty when DisableTranscripts=true, got %q", report.Results[0].TranscriptPath)
	}
	if _, err := os.Stat(filepath.Join(cfg.OutputDir, "transcripts")); !os.IsNotExist(err) {
		t.Fatalf("transcripts dir should not exist when disabled, got err=%v", err)
	}
}

func TestSanitizeTranscriptFilename(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"hello-world":     "hello-world",
		"path/with/slash": "path_with_slash",
		"weird:chars*?":   "weird_chars__",
		"":                "task",
		"v1.2_alpha":      "v1.2_alpha",
	}
	for in, want := range cases {
		if got := sanitizeTranscriptFilename(in); got != want {
			t.Errorf("sanitizeTranscriptFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

// findCommandContaining returns true if any recorded command contains the given
// substring. Used so tests don't break when setup steps (apt-mirror, etc.) are
// inserted at the front of the command stream.
func findCommandContaining(cmds []sandboxenv.CommandRequest, substr string) bool {
	for _, c := range cmds {
		if strings.Contains(c.Command, substr) {
			return true
		}
	}
	return false
}

func commandStrings(cmds []sandboxenv.CommandRequest) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.Command
	}
	return out
}

func TestParseReward(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw    string
		want   float64
		hasErr bool
	}{
		{"1.0\n", 1.0, false},
		{"0", 0.0, false},
		{"0.75", 0.75, false},
		{"  1  ", 1.0, false},
		{"", 0, true},
		{"nope", 0, true},
	}
	for _, tc := range cases {
		got, err := parseReward(tc.raw)
		if tc.hasErr {
			if err == nil {
				t.Errorf("parseReward(%q) want error, got %v", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseReward(%q) error: %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseReward(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestConfigApplyDefaultsAlignsWithCLI(t *testing.T) {
	t.Parallel()

	// Empty config should land on the same iteration cap that the CLI flag
	// default (cmd/cli/eval_terminalbench.go:--max-iters) and
	// agent.DefaultSubagentMaxIterations both use. Guards against a silent
	// regression to the old 30 (which mismatched every public surface and
	// caused premature aborts on multi-step TB2 tasks).
	cfg := &Config{}
	cfg.applyDefaults()
	if cfg.MaxIterations != 50 {
		t.Fatalf("MaxIterations default = %d, want 50", cfg.MaxIterations)
	}
	if cfg.Concurrency != 1 {
		t.Fatalf("Concurrency default = %d, want 1", cfg.Concurrency)
	}
	if cfg.TaskTimeout != 30*time.Minute {
		t.Fatalf("TaskTimeout default = %s, want 30m", cfg.TaskTimeout)
	}
	if cfg.TerminalTimeout != 15*time.Minute {
		t.Fatalf("TerminalTimeout default = %s, want 15m", cfg.TerminalTimeout)
	}
}

func TestProxyEnvFor_DisabledByEmptyURL(t *testing.T) {
	t.Parallel()
	env, args, err := proxyEnvFor("", DefaultMirrorEnv)
	if err != nil {
		t.Fatalf("proxyEnvFor empty: %v", err)
	}
	if env != nil || args != nil {
		t.Fatalf("empty url should yield zero env/args; got env=%v args=%v", env, args)
	}
	env, args, err = proxyEnvFor("   ", nil)
	if err != nil || env != nil || args != nil {
		t.Fatalf("whitespace-only url should be a no-op; got env=%v args=%v err=%v", env, args, err)
	}
}

func TestProxyEnvFor_LoopbackRewriteAndAddHost(t *testing.T) {
	t.Parallel()
	env, args, err := proxyEnvFor("http://127.0.0.1:7890", nil)
	if err != nil {
		t.Fatalf("proxyEnvFor: %v", err)
	}
	want := "http://host.docker.internal:7890"
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if env[k] != want {
			t.Errorf("env[%s] = %q, want %q", k, env[k], want)
		}
	}
	if env["NO_PROXY"] == "" || env["no_proxy"] == "" {
		t.Errorf("NO_PROXY/no_proxy should be populated; env=%v", env)
	}
	if len(args) != 1 || args[0] != "--add-host=host.docker.internal:host-gateway" {
		t.Errorf("expected --add-host wired; got %v", args)
	}
}

func TestProxyEnvFor_BareHostPortAcceptsScheme(t *testing.T) {
	t.Parallel()
	env, _, err := proxyEnvFor("127.0.0.1:7890", nil)
	if err != nil {
		t.Fatalf("bare host:port should parse: %v", err)
	}
	if env["HTTP_PROXY"] != "http://host.docker.internal:7890" {
		t.Errorf("bare host:port not auto-prefixed with http://; got %q", env["HTTP_PROXY"])
	}
}

func TestProxyEnvFor_RemoteHostNoAddHost(t *testing.T) {
	t.Parallel()
	env, args, err := proxyEnvFor("http://proxy.corp.example:8080", nil)
	if err != nil {
		t.Fatalf("proxyEnvFor: %v", err)
	}
	if env["HTTP_PROXY"] != "http://proxy.corp.example:8080" {
		t.Errorf("non-loopback host should pass through verbatim; got %q", env["HTTP_PROXY"])
	}
	if len(args) != 0 {
		t.Errorf("non-loopback host should not need --add-host; got %v", args)
	}
}

func TestProxyEnvFor_NoProxyContainsMirrorHosts(t *testing.T) {
	t.Parallel()
	mirror := map[string]string{
		"PIP_INDEX_URL":            "https://mirrors.aliyun.com/pypi/simple/",
		"PIP_TRUSTED_HOST":         "mirrors.aliyun.com",
		"UV_PYTHON_INSTALL_MIRROR": "https://mirror.nju.edu.cn/github-release/x/y",
		"HF_ENDPOINT":              "https://hf-mirror.com",
	}
	env, _, err := proxyEnvFor("http://127.0.0.1:7890", mirror)
	if err != nil {
		t.Fatalf("proxyEnvFor: %v", err)
	}
	noProxy := env["NO_PROXY"]
	for _, host := range []string{"localhost", "127.0.0.1", "::1", "mirrors.aliyun.com", "mirror.nju.edu.cn", "hf-mirror.com"} {
		if !strings.Contains(noProxy, host) {
			t.Errorf("NO_PROXY missing %q; got %q", host, noProxy)
		}
	}
	if env["NO_PROXY"] != env["no_proxy"] {
		t.Errorf("NO_PROXY and no_proxy should be identical; %q vs %q", env["NO_PROXY"], env["no_proxy"])
	}
}

func TestNormalizeProxyURL_LoopbackVariants(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in       string
		out      string
		rewrote  bool
		wantsErr bool
	}{
		{"http://localhost:7890", "http://host.docker.internal:7890", true, false},
		{"http://127.0.0.1:7890", "http://host.docker.internal:7890", true, false},
		{"http://[::1]:7890", "http://host.docker.internal:7890", true, false},
		{"http://0.0.0.0:7890", "http://host.docker.internal:7890", true, false},
		{"http://10.0.0.5:3128", "http://10.0.0.5:3128", false, false},
		{"https://proxy.corp:443", "https://proxy.corp:443", false, false},
		{"127.0.0.1:7890", "http://host.docker.internal:7890", true, false}, // bare → http scheme added
		{"socks5://127.0.0.1:1080", "socks5://host.docker.internal:1080", true, false},
	} {
		got, rewrote, err := normalizeProxyURL(tc.in)
		if tc.wantsErr {
			if err == nil {
				t.Errorf("normalizeProxyURL(%q): want err, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeProxyURL(%q): unexpected err %v", tc.in, err)
			continue
		}
		if got != tc.out {
			t.Errorf("normalizeProxyURL(%q) = %q, want %q", tc.in, got, tc.out)
		}
		if rewrote != tc.rewrote {
			t.Errorf("normalizeProxyURL(%q) rewrote = %v, want %v", tc.in, rewrote, tc.rewrote)
		}
	}
}

func TestProxyEnvFor_InvalidURLErrors(t *testing.T) {
	t.Parallel()
	if _, _, err := proxyEnvFor("http://", nil); err == nil {
		t.Error("expected error for proxy URL with no host")
	}
}

func TestConfigApplyDefaultsPreservesExplicitValues(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		MaxIterations:   7,
		MaxBudgetUSD:    1.5,
		MaxTokens:       100_000,
		Concurrency:     4,
		TaskTimeout:     time.Minute,
		TerminalTimeout: 30 * time.Second,
	}
	cfg.applyDefaults()
	if cfg.MaxIterations != 7 {
		t.Fatalf("explicit MaxIterations was overridden: %d", cfg.MaxIterations)
	}
	if cfg.MaxBudgetUSD != 1.5 {
		t.Fatalf("explicit MaxBudgetUSD was overridden: %v", cfg.MaxBudgetUSD)
	}
	if cfg.MaxTokens != 100_000 {
		t.Fatalf("explicit MaxTokens was overridden: %d", cfg.MaxTokens)
	}
	if cfg.Concurrency != 4 {
		t.Fatalf("explicit Concurrency was overridden: %d", cfg.Concurrency)
	}
}

func TestConfigApplyDefaults_VerifierEnvFillsFromDefaultMirror(t *testing.T) {
	t.Parallel()

	cfg := &Config{}
	cfg.applyDefaults()
	if len(cfg.VerifierEnv) == 0 {
		t.Fatal("VerifierEnv should auto-fill from DefaultMirrorEnv when nil")
	}
	for k, v := range DefaultMirrorEnv {
		if cfg.VerifierEnv[k] != v {
			t.Errorf("VerifierEnv[%s] = %q, want %q", k, cfg.VerifierEnv[k], v)
		}
	}
	// Mutating must not poison the package-level default map.
	cfg.VerifierEnv["PIP_INDEX_URL"] = "tampered"
	if DefaultMirrorEnv["PIP_INDEX_URL"] == "tampered" {
		t.Fatal("applyDefaults leaked DefaultMirrorEnv by reference")
	}
}

func TestConfigApplyDefaults_VerifierEnvExplicitEmptyPreserved(t *testing.T) {
	t.Parallel()

	cfg := &Config{VerifierEnv: map[string]string{}}
	cfg.applyDefaults()
	if cfg.VerifierEnv == nil {
		t.Fatal("explicit empty (non-nil) VerifierEnv must not be replaced by defaults")
	}
	if len(cfg.VerifierEnv) != 0 {
		t.Fatalf("explicit empty VerifierEnv was repopulated: %v", cfg.VerifierEnv)
	}
}

func TestParsePytestSummary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		log        string
		wantPassed int
		wantTotal  int
	}{
		{
			name:       "all passed",
			log:        "============================== 9 passed in 1.27s ==============================",
			wantPassed: 9,
			wantTotal:  9,
		},
		{
			name:       "failed and passed",
			log:        "========================= 1 failed, 8 passed in 1.27s ==========================",
			wantPassed: 8,
			wantTotal:  9,
		},
		{
			name:       "failed, passed, skipped — skipped excluded from total",
			log:        "==================== 2 failed, 7 passed, 1 skipped in 0.42s ====================",
			wantPassed: 7,
			wantTotal:  9,
		},
		{
			name:       "errors counted as not-passed",
			log:        "==================== 1 error in 0.10s ====================",
			wantPassed: 0,
			wantTotal:  1,
		},
		{
			name:       "multiple summary lines — last wins (verifier reruns)",
			log:        "===== 5 failed, 0 passed in 0.50s =====\n…retry…\n===== 0 failed, 5 passed in 0.55s =====",
			wantPassed: 5,
			wantTotal:  5,
		},
		{
			name:       "no pytest footer",
			log:        "PASS\n",
			wantPassed: 0,
			wantTotal:  0,
		},
		{
			name:       "empty",
			log:        "",
			wantPassed: 0,
			wantTotal:  0,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			passed, total := parsePytestSummary(tc.log)
			if passed != tc.wantPassed || total != tc.wantTotal {
				t.Errorf("parsePytestSummary(%q) = (%d, %d), want (%d, %d)",
					tc.log, passed, total, tc.wantPassed, tc.wantTotal)
			}
		})
	}
}

// aggregate must classify as "errored" only when the verifier never ran.
// A task whose agent loop hit a deadline but whose verifier still produced
// reward.txt is a real failure (partial credit), not infrastructure noise.
func TestAggregate_VerifierRanGatesErrored(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		results     []TaskResult
		wantPassed  int
		wantFailed  int
		wantErrored int
		wantSkipped int
	}{
		{
			name: "verifier ran + pass → passed",
			results: []TaskResult{
				{Pass: true, VerifierRan: true},
			},
			wantPassed: 1,
		},
		{
			name: "verifier ran + tests failed → failed (not errored)",
			results: []TaskResult{
				{Pass: false, VerifierRan: true, ErrorMsg: "agent: max iterations"},
			},
			wantFailed: 1,
		},
		{
			name: "verifier never ran → errored",
			results: []TaskResult{
				{Pass: false, VerifierRan: false, ErrorMsg: "prepare-session: boom"},
			},
			wantErrored: 1,
		},
		{
			name: "skipped never reaches errored/failed buckets",
			results: []TaskResult{
				{Skipped: true},
			},
			wantSkipped: 1,
		},
		{
			name: "mixed: 2 pass, 1 fail (verifier ran), 1 err (no verifier), 1 skip",
			results: []TaskResult{
				{Pass: true, VerifierRan: true},
				{Pass: true, VerifierRan: true},
				{Pass: false, VerifierRan: true},
				{Pass: false, VerifierRan: false},
				{Skipped: true},
			},
			wantPassed: 2, wantFailed: 1, wantErrored: 1, wantSkipped: 1,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			agg := aggregate(tc.results)
			if agg.Passed != tc.wantPassed ||
				agg.Failed != tc.wantFailed ||
				agg.Errored != tc.wantErrored ||
				agg.Skipped != tc.wantSkipped {
				t.Errorf("aggregate = {pass:%d fail:%d err:%d skip:%d}, want {pass:%d fail:%d err:%d skip:%d}",
					agg.Passed, agg.Failed, agg.Errored, agg.Skipped,
					tc.wantPassed, tc.wantFailed, tc.wantErrored, tc.wantSkipped)
			}
		})
	}
}

func TestRunVerifier_PassesEnvOnlyToVerifierCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTaskFixture(t, root, "verifier-env-task", "noop")

	tasks, err := dataset.Load(root)
	if err != nil {
		t.Fatalf("dataset.Load: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}

	env := newStubEnv("1.0\n")
	verifierEnv := map[string]string{
		"PIP_INDEX_URL":            "https://mirrors.aliyun.com/pypi/simple/",
		"UV_PYTHON_INSTALL_MIRROR": "https://mirror.nju.edu.cn/x/y",
	}

	res := &TaskResult{}
	ps, err := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "verify-env"})
	if err != nil {
		t.Fatalf("PrepareSession: %v", err)
	}
	if err := runVerifier(context.Background(), env, env, ps, tasks[0], res, time.Second, root, verifierEnv); err != nil {
		t.Fatalf("runVerifier: %v", err)
	}

	if len(env.commands) < 2 {
		t.Fatalf("expected at least 2 RunCommand calls (mkdir + verifier), got %d", len(env.commands))
	}
	for i, cmd := range env.commands {
		if len(cmd.Env) != len(verifierEnv) {
			t.Fatalf("commands[%d].Env len = %d, want %d (got %v)", i, len(cmd.Env), len(verifierEnv), cmd.Env)
		}
		for k, v := range verifierEnv {
			if cmd.Env[k] != v {
				t.Errorf("commands[%d].Env[%s] = %q, want %q", i, k, cmd.Env[k], v)
			}
		}
	}
}
