package dockerenv

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	sandboxenv "github.com/saker-ai/saker/pkg/sandbox/env"
)

// fakeCommander records each invocation and returns scripted results.
// Tests assert on the recorded argv and stdin payloads.
type fakeCommander struct {
	mu       sync.Mutex
	calls    []recordedCall
	handlers map[string]fakeHandler // matched by joined argv prefix
}

type recordedCall struct {
	Argv    []string
	Stdin   []byte
	Timeout time.Duration
}

type fakeHandler func(call recordedCall) (cmdResult, error)

func newFake() *fakeCommander {
	return &fakeCommander{handlers: map[string]fakeHandler{}}
}

func (f *fakeCommander) handle(prefix string, h fakeHandler) {
	f.handlers[prefix] = h
}

func (f *fakeCommander) Run(_ context.Context, argv []string, stdin io.Reader, timeout time.Duration) (cmdResult, error) {
	var stdinBytes []byte
	if stdin != nil {
		buf := new(bytes.Buffer)
		_, _ = io.Copy(buf, stdin)
		stdinBytes = buf.Bytes()
	}
	call := recordedCall{Argv: append([]string(nil), argv...), Stdin: stdinBytes, Timeout: timeout}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	if h := f.matchHandler(argv); h != nil {
		return h(call)
	}
	return cmdResult{}, nil
}

func (f *fakeCommander) Stream(ctx context.Context, argv []string, stdin io.Reader, timeout time.Duration, onStdout, onStderr func(string)) (cmdResult, error) {
	res, err := f.Run(ctx, argv, stdin, timeout)
	if onStdout != nil && res.Stdout != "" {
		onStdout(res.Stdout)
	}
	if onStderr != nil && res.Stderr != "" {
		onStderr(res.Stderr)
	}
	return res, err
}

func (f *fakeCommander) matchHandler(argv []string) fakeHandler {
	joined := strings.Join(argv, " ")
	// Pick the longest matching substring so more specific handlers win.
	// Substring (vs prefix) lets tests ignore container IDs that get spliced
	// into the middle of the docker exec argv.
	var best string
	for prefix := range f.handlers {
		if strings.Contains(joined, prefix) && len(prefix) > len(best) {
			best = prefix
		}
	}
	if best == "" {
		return nil
	}
	return f.handlers[best]
}

func (f *fakeCommander) callsFor(needle string) []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recordedCall
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c.Argv, " "), needle) {
			out = append(out, c)
		}
	}
	return out
}

func newTestEnv(t *testing.T, image string, opts ...func(*Config)) (*Environment, *fakeCommander) {
	t.Helper()
	cfg := Config{Image: image, NamePrefix: "tst", DefaultWorkdir: "/app"}
	for _, fn := range opts {
		fn(&cfg)
	}
	env := New(cfg)
	fake := newFake()
	env.withCommander(fake)
	return env, fake
}

func TestPrepareSession_PullsImageAndStartsContainer(t *testing.T) {
	t.Parallel()
	env, fake := newTestEnv(t, "ubuntu:24.04", func(c *Config) { c.PullPolicy = PullAlways })

	fake.handle("pull ubuntu:24.04", func(call recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0}, nil
	})
	fake.handle("run -d --rm --name tst-task1-", func(call recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "abc123\n", ExitCode: 0}, nil
	})
	fake.handle("exec -w / cid", func(call recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0}, nil
	})

	ps, err := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "task1"})
	if err != nil {
		t.Fatalf("PrepareSession: %v", err)
	}
	if ps.SandboxType != SandboxType {
		t.Fatalf("SandboxType = %q, want %q", ps.SandboxType, SandboxType)
	}
	if ps.GuestCwd != "/app" {
		t.Fatalf("GuestCwd = %q, want /app", ps.GuestCwd)
	}
	if got := ps.Meta["container_id"]; got != "abc123" {
		t.Fatalf("container_id = %v, want abc123", got)
	}
	// Cached on second call.
	ps2, err := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "task1"})
	if err != nil {
		t.Fatalf("PrepareSession second call: %v", err)
	}
	if ps2 != ps {
		t.Fatalf("second PrepareSession returned different session, want cached")
	}
	pulls := fake.callsFor("pull")
	if len(pulls) != 1 {
		t.Fatalf("docker pull invoked %d times, want 1 (PullAlways but cached session)", len(pulls))
	}
}

func TestPrepareSession_PopulatesImageDigestWhenRepoDigestPresent(t *testing.T) {
	t.Parallel()
	env, fake := newTestEnv(t, "ubuntu:24.04", func(c *Config) { c.PullPolicy = PullNever })
	digest := "ubuntu@sha256:abc1234567890def1234567890abc1234567890def1234567890abc1234567890"

	// `inspectImageDigest` issues the only inspect call carrying RepoDigests
	// in its --format string. Match by the unique substring so the
	// fake commander routes this argv specifically (vs the workdir inspect).
	fake.handle("RepoDigests", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0, Stdout: digest + "\n"}, nil
	})
	fake.handle("WorkingDir", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0, Stdout: "/work\n"}, nil
	})
	fake.handle("run -d --rm --name", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "cid\n"}, nil
	})
	fake.handle("exec -w / cid", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0}, nil
	})

	ps, err := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "digest-task"})
	if err != nil {
		t.Fatalf("PrepareSession: %v", err)
	}
	got, ok := ps.Meta["image_digest"].(string)
	if !ok {
		t.Fatalf("image_digest missing or wrong type: %v (%T)", ps.Meta["image_digest"], ps.Meta["image_digest"])
	}
	if got != digest {
		t.Fatalf("image_digest = %q, want %q", got, digest)
	}
}

func TestPrepareSession_OmitsImageDigestWhenInspectFails(t *testing.T) {
	t.Parallel()
	// Locally-built images have no RepoDigests; inspectImageDigest returns
	// "" silently and Meta should NOT carry the key (omitempty would
	// otherwise serialize it as `"image_digest": ""` which is misleading).
	env, fake := newTestEnv(t, "local:dev", func(c *Config) { c.PullPolicy = PullNever })
	fake.handle("RepoDigests", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0, Stdout: "\n"}, nil
	})
	fake.handle("WorkingDir", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0, Stdout: ""}, nil
	})
	fake.handle("run -d --rm --name", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "cid\n"}, nil
	})
	fake.handle("exec -w / cid", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0}, nil
	})

	ps, err := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "no-digest-task"})
	if err != nil {
		t.Fatalf("PrepareSession: %v", err)
	}
	if _, present := ps.Meta["image_digest"]; present {
		t.Fatalf("image_digest should be absent when RepoDigests is empty, got %v", ps.Meta["image_digest"])
	}
}

func TestPrepareSession_PullIfMissing_SkipsWhenInspectSucceeds(t *testing.T) {
	t.Parallel()
	env, fake := newTestEnv(t, "ubuntu:24.04", func(c *Config) { c.PullPolicy = PullIfMissing })

	fake.handle("image inspect ubuntu:24.04", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0, Stdout: "[]"}, nil
	})
	fake.handle("run -d --rm --name tst-", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "cid\n"}, nil
	})
	fake.handle("exec -w / cid", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0}, nil
	})

	if _, err := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "task2"}); err != nil {
		t.Fatalf("PrepareSession: %v", err)
	}
	if pulls := fake.callsFor("pull"); len(pulls) != 0 {
		t.Fatalf("docker pull invoked %d times, want 0 (image already present)", len(pulls))
	}
}

func TestRunCommand_BuildsExecArgvAndCapturesExit(t *testing.T) {
	t.Parallel()
	env, fake := newTestEnv(t, "ubuntu:24.04", func(c *Config) { c.PullPolicy = PullNever })
	fake.handle("run -d --rm --name tst-", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "cid\n"}, nil
	})
	// First exec call (mkdir workdir) returns 0, subsequent will be the test cmd.
	var execCount int
	fake.handle("exec -w / cid", func(call recordedCall) (cmdResult, error) {
		execCount++
		if execCount == 1 {
			return cmdResult{ExitCode: 0}, nil
		}
		return cmdResult{ExitCode: 0}, nil
	})
	fake.handle("exec -w /app --env FOO=bar cid", func(call recordedCall) (cmdResult, error) {
		// Verify env vars are passed
		joined := strings.Join(call.Argv, " ")
		if !strings.Contains(joined, "--env FOO=bar") {
			t.Errorf("argv missing --env FOO=bar: %s", joined)
		}
		if !strings.Contains(joined, "/bin/sh -lc echo hello") {
			t.Errorf("argv missing command: %s", joined)
		}
		return cmdResult{Stdout: "hello\n", ExitCode: 0, Duration: 10 * time.Millisecond}, nil
	})

	ps, err := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "t"})
	if err != nil {
		t.Fatalf("PrepareSession: %v", err)
	}
	res, err := env.RunCommand(context.Background(), ps, sandboxenv.CommandRequest{
		Command: "echo hello",
		Env:     map[string]string{"FOO": "bar"},
	})
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if res.Stdout != "hello\n" {
		t.Fatalf("Stdout = %q, want hello\\n", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
}

func TestWriteAndReadFile_RoundTripViaShellPipes(t *testing.T) {
	t.Parallel()
	env, fake := newTestEnv(t, "img", func(c *Config) { c.PullPolicy = PullNever })
	fake.handle("run -d --rm", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "cid\n"}, nil
	})

	// Capture WriteFile body in a side channel.
	var written []byte
	fake.handle("exec -i -w / cid sh -c cat >", func(call recordedCall) (cmdResult, error) {
		written = append([]byte(nil), call.Stdin...)
		return cmdResult{ExitCode: 0}, nil
	})
	// All `exec -w / mkdir`, `exec -w / cat` etc. fall back to default 0.
	fake.handle("exec -w / cid mkdir", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0}, nil
	})
	fake.handle("exec -w / cid cat -- /app/foo.txt", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: string(written), ExitCode: 0}, nil
	})

	ps, err := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "t"})
	if err != nil {
		t.Fatalf("PrepareSession: %v", err)
	}
	want := []byte("hello world\n")
	if err := env.WriteFile(context.Background(), ps, "foo.txt", want); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !bytes.Equal(written, want) {
		t.Fatalf("written = %q, want %q", written, want)
	}
	got, err := env.ReadFile(context.Background(), ps, "foo.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ReadFile = %q, want %q", got, want)
	}
}

func TestReadFile_RejectsBinary(t *testing.T) {
	t.Parallel()
	env, fake := newTestEnv(t, "img", func(c *Config) { c.PullPolicy = PullNever })
	fake.handle("run -d --rm", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "cid\n"}, nil
	})
	fake.handle("exec -w / cid mkdir", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0}, nil
	})
	fake.handle("exec -w / cid cat --", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "abc\x00def", ExitCode: 0}, nil
	})

	ps, _ := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "t"})
	if _, err := env.ReadFile(context.Background(), ps, "/tmp/bin"); err == nil {
		t.Fatal("ReadFile must reject NUL-containing data")
	}
}

func TestEditFile_ReplaceFirstAndAll(t *testing.T) {
	t.Parallel()
	env, fake := newTestEnv(t, "img", func(c *Config) { c.PullPolicy = PullNever })
	fake.handle("run -d --rm", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "cid\n"}, nil
	})
	fake.handle("exec -w / cid mkdir", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0}, nil
	})
	state := []byte("foo bar foo")
	fake.handle("exec -w / cid cat --", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: string(state), ExitCode: 0}, nil
	})
	fake.handle("exec -i -w / cid sh -c cat >", func(call recordedCall) (cmdResult, error) {
		state = append([]byte(nil), call.Stdin...)
		return cmdResult{ExitCode: 0}, nil
	})

	ps, _ := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "t"})

	if err := env.EditFile(context.Background(), ps, sandboxenv.EditRequest{Path: "x", OldText: "foo", NewText: "qux"}); err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	if string(state) != "qux bar foo" {
		t.Fatalf("after first replace = %q, want %q", state, "qux bar foo")
	}

	state = []byte("foo bar foo")
	if err := env.EditFile(context.Background(), ps, sandboxenv.EditRequest{Path: "x", OldText: "foo", NewText: "qux", ReplaceAll: true}); err != nil {
		t.Fatalf("EditFile replaceAll: %v", err)
	}
	if string(state) != "qux bar qux" {
		t.Fatalf("after replaceAll = %q, want %q", state, "qux bar qux")
	}
}

func TestGrep_ParsesPathLinePreview(t *testing.T) {
	t.Parallel()
	env, fake := newTestEnv(t, "img", func(c *Config) { c.PullPolicy = PullNever })
	fake.handle("run -d --rm", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "cid\n"}, nil
	})
	fake.handle("exec -w / cid mkdir", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0}, nil
	})
	fake.handle("exec -w / cid /bin/sh -c grep ", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "/app/a.txt:3:hello world\n/app/b.txt:42:another\n", ExitCode: 0}, nil
	})

	ps, _ := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "t"})
	matches, err := env.Grep(context.Background(), ps, sandboxenv.GrepRequest{Pattern: "hello", Path: "/app"})
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2", len(matches))
	}
	if matches[0].Path != "/app/a.txt" || matches[0].Line != 3 || matches[0].Preview != "hello world" {
		t.Fatalf("match[0] = %+v", matches[0])
	}
	if matches[1].Path != "/app/b.txt" || matches[1].Line != 42 {
		t.Fatalf("match[1] = %+v", matches[1])
	}
}

func TestGlob_PassesPatternAndParsesLines(t *testing.T) {
	t.Parallel()
	env, fake := newTestEnv(t, "img", func(c *Config) { c.PullPolicy = PullNever })
	fake.handle("run -d --rm", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "cid\n"}, nil
	})
	fake.handle("exec -w / cid mkdir", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0}, nil
	})
	var sawCmd string
	fake.handle("exec -w / cid /bin/sh -c find ", func(call recordedCall) (cmdResult, error) {
		sawCmd = call.Argv[len(call.Argv)-1]
		return cmdResult{Stdout: "/app/x.go\n/app/y.go\n", ExitCode: 0}, nil
	})

	ps, _ := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "t"})
	got, err := env.Glob(context.Background(), ps, "*.go")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(got) != 2 || got[0] != "/app/x.go" || got[1] != "/app/y.go" {
		t.Fatalf("got = %v", got)
	}
	if !strings.Contains(sawCmd, "/app") {
		t.Fatalf("find cmd missing workdir prefix: %q", sawCmd)
	}
}

func TestCloseSession_RemovesContainer(t *testing.T) {
	t.Parallel()
	env, fake := newTestEnv(t, "img", func(c *Config) { c.PullPolicy = PullNever })
	fake.handle("run -d --rm", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "cid\n"}, nil
	})
	fake.handle("exec -w / cid mkdir", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0}, nil
	})
	var rmCalled bool
	fake.handle("rm -f cid", func(recordedCall) (cmdResult, error) {
		rmCalled = true
		return cmdResult{ExitCode: 0}, nil
	})

	ps, _ := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "t"})
	if err := env.CloseSession(context.Background(), ps); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if !rmCalled {
		t.Fatal("docker rm -f not invoked")
	}
	// After close, RunCommand must fail with no session.
	if _, err := env.RunCommand(context.Background(), ps, sandboxenv.CommandRequest{Command: "true"}); err == nil {
		t.Fatal("RunCommand after CloseSession must error")
	}
}

func TestCopyArchiveTo_PipesStdinIntoTar(t *testing.T) {
	t.Parallel()
	env, fake := newTestEnv(t, "img", func(c *Config) { c.PullPolicy = PullNever })
	fake.handle("run -d --rm", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "cid\n"}, nil
	})
	fake.handle("exec -w / cid mkdir", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0}, nil
	})
	var stdin []byte
	fake.handle("exec -i -w / cid tar -xf - -C /tests", func(call recordedCall) (cmdResult, error) {
		stdin = append([]byte(nil), call.Stdin...)
		return cmdResult{ExitCode: 0}, nil
	})

	ps, _ := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "t"})
	tarPayload := []byte("FAKE-TAR-BYTES")
	if err := env.CopyArchiveTo(context.Background(), ps, "/tests", bytes.NewReader(tarPayload)); err != nil {
		t.Fatalf("CopyArchiveTo: %v", err)
	}
	if !bytes.Equal(stdin, tarPayload) {
		t.Fatalf("stdin = %q, want %q", stdin, tarPayload)
	}
}

func TestStartContainer_ReportsErrorOnNonZeroExit(t *testing.T) {
	t.Parallel()
	env, fake := newTestEnv(t, "img", func(c *Config) { c.PullPolicy = PullNever })
	fake.handle("run -d --rm", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 125, Stderr: "no such image"}, nil
	})
	_, err := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "t"})
	if err == nil || !strings.Contains(err.Error(), "no such image") {
		t.Fatalf("expected start error with stderr, got %v", err)
	}
}

func TestPullImage_PropagatesCommanderError(t *testing.T) {
	t.Parallel()
	env, fake := newTestEnv(t, "img", func(c *Config) { c.PullPolicy = PullAlways })
	wantErr := errors.New("dial fail")
	fake.handle("pull img", func(recordedCall) (cmdResult, error) {
		return cmdResult{}, wantErr
	})
	_, err := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "t"})
	if err == nil || !strings.Contains(err.Error(), "dial fail") {
		t.Fatalf("err = %v, want wrapped dial fail", err)
	}
}

func TestSanitizeName(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"", "session"},
		{"abc", "abc"},
		{"Foo Bar/Baz", "foo-bar-baz"},
		{"task#1", "task-1"},
		{strings.Repeat("a", 64), strings.Repeat("a", 32)},
	}
	for _, tc := range cases {
		if got := sandboxenv.SanitizeName(tc.in); got != tc.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeGuestPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path, workdir, want string
	}{
		{"foo.txt", "/app", "/app/foo.txt"},
		{"/etc/hosts", "/app", "/etc/hosts"},
		{"./bar", "/app", "/app/bar"},
		{"", "/app", "/app"},
	}
	for _, tc := range cases {
		if got := normalizeGuestPath(tc.path, tc.workdir); got != tc.want {
			t.Errorf("normalizeGuestPath(%q,%q) = %q, want %q", tc.path, tc.workdir, got, tc.want)
		}
	}
}

func TestGuestRootForPattern(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"/app/src/*.go", "/app/src"},
		{"/app/**/x", "/app"},
		{"foo*", ""},
		{"/", ""},
	}
	for _, tc := range cases {
		got := guestRootForPattern(tc.in)
		// Empty inputs collapse to "/".
		if tc.want == "" {
			if got != "/" && got != "" {
				t.Errorf("guestRootForPattern(%q) = %q, want / or empty", tc.in, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("guestRootForPattern(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Verifies the ExtraEnv map ends up on `docker run` argv as `-e KEY=VAL`,
// in deterministic (sorted) order. Order matters because we rely on it for
// stable container introspection / golden snapshots.
func TestPrepareSession_ExtraEnvAppearsOnRunArgv(t *testing.T) {
	t.Parallel()
	env, fake := newTestEnv(t, "ubuntu:24.04", func(c *Config) {
		c.PullPolicy = PullNever
		c.ExtraEnv = map[string]string{
			"PIP_INDEX_URL":            "https://mirrors.aliyun.com/pypi/simple/",
			"UV_PYTHON_INSTALL_MIRROR": "https://ghproxy.cn/...",
			"AAA_FIRST":                "1",
		}
	})
	fake.handle("run -d --rm --name tst-", func(recordedCall) (cmdResult, error) {
		return cmdResult{Stdout: "cid\n"}, nil
	})
	fake.handle("exec -w / cid", func(recordedCall) (cmdResult, error) {
		return cmdResult{ExitCode: 0}, nil
	})

	if _, err := env.PrepareSession(context.Background(), sandboxenv.SessionContext{SessionID: "envtask"}); err != nil {
		t.Fatalf("PrepareSession: %v", err)
	}
	runs := fake.callsFor("run -d --rm")
	if len(runs) != 1 {
		t.Fatalf("expected 1 docker run, got %d", len(runs))
	}
	got := strings.Join(runs[0].Argv, " ")
	want := "-e AAA_FIRST=1 -e PIP_INDEX_URL=https://mirrors.aliyun.com/pypi/simple/ -e UV_PYTHON_INSTALL_MIRROR=https://ghproxy.cn/..."
	if !strings.Contains(got, want) {
		t.Fatalf("docker run argv missing sorted -e flags\n  got:  %s\n  want substring: %s", got, want)
	}
}
