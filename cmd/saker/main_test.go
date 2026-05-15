package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/saker-ai/saker/pkg/api"
)

type fakeRuntime struct {
	runFn       func(context.Context, api.Request) (*api.Response, error)
	runStreamFn func(context.Context, api.Request) (<-chan api.StreamEvent, error)
	closeFn     func() error
}

func (f *fakeRuntime) Run(ctx context.Context, req api.Request) (*api.Response, error) {
	if f.runFn != nil {
		return f.runFn(ctx, req)
	}
	return &api.Response{Result: &api.Result{Output: "ok"}}, nil
}

func (f *fakeRuntime) RunStream(ctx context.Context, req api.Request) (<-chan api.StreamEvent, error) {
	if f.runStreamFn != nil {
		return f.runStreamFn(ctx, req)
	}
	ch := make(chan api.StreamEvent)
	close(ch)
	return ch, nil
}

func (f *fakeRuntime) Close() error {
	if f.closeFn != nil {
		return f.closeFn()
	}
	return nil
}

func TestRunACPModeNoPrompt(t *testing.T) {
	originalServe := serveACPStdio
	t.Cleanup(func() {
		serveACPStdio = originalServe
	})

	called := false
	serveACPStdio = func(ctx context.Context, options api.Options, stdin io.Reader, stdout io.Writer) error {
		called = true
		return nil
	}

	if err := run([]string{"--acp=true"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("run with --acp=true should not require prompt: %v", err)
	}
	if !called {
		t.Fatalf("expected ACP serve path to be called")
	}
}

func TestRunNonACPModeWithoutPromptDefaultsToInteractiveShell(t *testing.T) {
	if _, err := os.Open("/dev/tty"); err != nil {
		t.Skip("skipping: no TTY available (CI environment)")
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()

	originalStdin := os.Stdin
	os.Stdin = devNull
	t.Cleanup(func() {
		os.Stdin = originalStdin
	})

	origFactory := runtimeFactory
	origRunInteractive := clikitRunInteractiveShell
	t.Cleanup(func() {
		runtimeFactory = origFactory
		clikitRunInteractiveShell = origRunInteractive
	})
	runtimeFactory = func(context.Context, api.Options) (runtimeClient, error) {
		return &fakeRuntime{}, nil
	}
	called := false
	clikitRunInteractiveShell = func(ctx context.Context, in io.ReadCloser, out, errOut io.Writer, eng replEngine, timeoutMs int, verbose bool, waterfallMode string, initialSessionID string) error {
		called = true
		return nil
	}

	err = run(nil, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("expected interactive shell fallback, got: %v", err)
	}
	if !called {
		t.Fatal("expected interactive shell to be called")
	}
}

func TestRunPrintsSharedEffectiveConfig(t *testing.T) {
	origFactory := runtimeFactory
	t.Cleanup(func() {
		runtimeFactory = origFactory
	})
	runtimeFactory = func(context.Context, api.Options) (runtimeClient, error) {
		return &fakeRuntime{
			runFn: func(context.Context, api.Request) (*api.Response, error) {
				return &api.Response{Mode: api.ModeContext{EntryPoint: api.EntryPointCLI}, Result: &api.Result{Output: "ok"}}, nil
			},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run([]string{"--prompt", "hi", "--print-effective-config"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := stderr.String(); !strings.Contains(got, "effective-config (pre-runtime)") {
		t.Fatalf("expected shared config output, got: %s", got)
	}
}

func TestRunStreamUsesClikitRendererWhenEnabled(t *testing.T) {
	origFactory := runtimeFactory
	origRunStream := clikitRunStream
	t.Cleanup(func() {
		runtimeFactory = origFactory
		clikitRunStream = origRunStream
	})
	rt := &fakeRuntime{}
	runtimeFactory = func(context.Context, api.Options) (runtimeClient, error) {
		return rt, nil
	}
	called := false
	clikitRunStream = func(ctx context.Context, out, errOut io.Writer, eng streamEngine, sessionID, prompt string, timeoutMs int, verbose bool, waterfallMode string) error {
		called = true
		if prompt != "hi" {
			t.Fatalf("unexpected prompt: %q", prompt)
		}
		return nil
	}

	if err := run([]string{"--prompt", "hi", "--stream", "--stream-format", "rendered"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !called {
		t.Fatalf("expected clikit stream renderer to be called")
	}
}

func TestRunStreamJSONRemainsMachineReadableByDefault(t *testing.T) {
	origFactory := runtimeFactory
	t.Cleanup(func() {
		runtimeFactory = origFactory
	})
	runtimeFactory = func(context.Context, api.Options) (runtimeClient, error) {
		return &fakeRuntime{
			runStreamFn: func(context.Context, api.Request) (<-chan api.StreamEvent, error) {
				ch := make(chan api.StreamEvent, 1)
				ch <- api.StreamEvent{Type: api.EventMessageStop}
				close(ch)
				return ch, nil
			},
		}, nil
	}

	var stdout bytes.Buffer
	if err := run([]string{"--prompt", "hi", "--stream"}, &stdout, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, `"type":"message_stop"`) {
		t.Fatalf("expected json stream output, got: %s", got)
	}
}

func TestCLIReplUsesSharedBannerAndCommandLoop(t *testing.T) {
	if _, err := os.Open("/dev/tty"); err != nil {
		t.Skip("skipping: no TTY available (CI environment)")
	}
	origFactory := runtimeFactory
	origRunInteractive := clikitRunInteractiveShell
	t.Cleanup(func() {
		runtimeFactory = origFactory
		clikitRunInteractiveShell = origRunInteractive
	})
	runtimeFactory = func(context.Context, api.Options) (runtimeClient, error) {
		return &fakeRuntime{}, nil
	}
	called := false
	clikitRunInteractiveShell = func(ctx context.Context, in io.ReadCloser, out, errOut io.Writer, eng replEngine, timeoutMs int, verbose bool, waterfallMode string, initialSessionID string) error {
		called = true
		return nil
	}

	if err := run([]string{"--repl"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !called {
		t.Fatalf("expected clikit repl to be called")
	}
}

func TestCLIReplUsesInteractiveShell(t *testing.T) {
	if _, err := os.Open("/dev/tty"); err != nil {
		t.Skip("skipping: no TTY available (CI environment)")
	}
	origFactory := runtimeFactory
	origRunInteractive := clikitRunInteractiveShell
	t.Cleanup(func() {
		runtimeFactory = origFactory
		clikitRunInteractiveShell = origRunInteractive
	})
	runtimeFactory = func(context.Context, api.Options) (runtimeClient, error) {
		return &fakeRuntime{}, nil
	}

	called := false
	clikitRunInteractiveShell = func(ctx context.Context, in io.ReadCloser, out, errOut io.Writer, eng replEngine, timeoutMs int, verbose bool, waterfallMode string, initialSessionID string) error {
		called = true
		return nil
	}

	if err := run([]string{"--repl"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !called {
		t.Fatalf("expected interactive shell to be called")
	}
}

func TestRunGVisorHelperMode(t *testing.T) {
	orig := runGVisorHelper
	t.Cleanup(func() {
		runGVisorHelper = orig
	})
	called := false
	runGVisorHelper = func(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
		called = true
		_, _ = io.WriteString(stdout, `{"success":true}`+"\n")
		return nil
	}

	var stdout bytes.Buffer
	if err := run([]string{"--saker-gvisor-helper"}, &stdout, io.Discard); err != nil {
		t.Fatalf("run helper: %v", err)
	}
	if !called {
		t.Fatalf("expected helper path to be called")
	}
	if !strings.Contains(stdout.String(), `"success":true`) {
		t.Fatalf("unexpected helper stdout: %s", stdout.String())
	}
}

func TestRunRejectsInvalidSandboxProjectMount(t *testing.T) {
	err := run([]string{"--prompt", "hi", "--sandbox-backend=govm", "--sandbox-project-mount=invalid"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "sandbox-project-mount") {
		t.Fatalf("expected sandbox-project-mount error, got %v", err)
	}
}

func TestRunRejectsUnsupportedGovmPlatform(t *testing.T) {
	origPlatformCheck := validateGovmPlatform
	t.Cleanup(func() {
		validateGovmPlatform = origPlatformCheck
	})
	validateGovmPlatform = func() error {
		return errors.New("govm requires linux/amd64, linux/arm64, or darwin/arm64")
	}

	err := run([]string{"--prompt", "hi", "--sandbox-backend=govm"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "govm requires") {
		t.Fatalf("expected govm platform error, got %v", err)
	}
}
