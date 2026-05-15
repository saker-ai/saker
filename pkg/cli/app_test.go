package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
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

func newTestApp() *App {
	app := New()
	app.Version = "test"
	return app
}

func TestRunACPModeNoPrompt(t *testing.T) {
	app := newTestApp()
	called := false
	app.serveACPStdio = func(ctx context.Context, options api.Options, stdin io.Reader, stdout io.Writer) error {
		called = true
		return nil
	}

	if err := app.Run([]string{"--acp=true"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("run with --acp=true should not require prompt: %v", err)
	}
	if !called {
		t.Fatalf("expected ACP serve path to be called")
	}
}

func TestRunPrintsSharedEffectiveConfig(t *testing.T) {
	app := newTestApp()
	app.runtimeFactory = func(context.Context, api.Options) (RuntimeClient, error) {
		return &fakeRuntime{
			runFn: func(context.Context, api.Request) (*api.Response, error) {
				return &api.Response{Mode: api.ModeContext{EntryPoint: api.EntryPointCLI}, Result: &api.Result{Output: "ok"}}, nil
			},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := app.Run([]string{"--prompt", "hi", "--print-effective-config"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := stderr.String(); !strings.Contains(got, "effective-config (pre-runtime)") {
		t.Fatalf("expected shared config output, got: %s", got)
	}
}

func TestRunStreamUsesClikitRendererWhenEnabled(t *testing.T) {
	app := newTestApp()
	app.runtimeFactory = func(context.Context, api.Options) (RuntimeClient, error) {
		return &fakeRuntime{}, nil
	}
	called := false
	app.runStream = func(ctx context.Context, out, errOut io.Writer, eng streamEngine, sessionID, prompt string, timeoutMs int, verbose bool, waterfallMode string) error {
		called = true
		if prompt != "hi" {
			t.Fatalf("unexpected prompt: %q", prompt)
		}
		return nil
	}

	if err := app.Run([]string{"--prompt", "hi", "--stream", "--stream-format", "rendered"}, io.Discard, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !called {
		t.Fatalf("expected clikit stream renderer to be called")
	}
}

func TestRunStreamJSONRemainsMachineReadableByDefault(t *testing.T) {
	app := newTestApp()
	app.runtimeFactory = func(context.Context, api.Options) (RuntimeClient, error) {
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
	if err := app.Run([]string{"--prompt", "hi", "--stream"}, &stdout, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, `"type":"message_stop"`) {
		t.Fatalf("expected json stream output, got: %s", got)
	}
}

func TestRunGVisorHelperMode(t *testing.T) {
	app := newTestApp()
	called := false
	app.runGVisorHelper = func(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
		called = true
		_, _ = io.WriteString(stdout, `{"success":true}`+"\n")
		return nil
	}

	var stdout bytes.Buffer
	if err := app.Run([]string{"--saker-gvisor-helper"}, &stdout, io.Discard); err != nil {
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
	app := newTestApp()
	err := app.Run([]string{"--prompt", "hi", "--sandbox-backend=govm", "--sandbox-project-mount=invalid"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "sandbox-project-mount") {
		t.Fatalf("expected sandbox-project-mount error, got %v", err)
	}
}

func TestRunRejectsUnsupportedGovmPlatform(t *testing.T) {
	app := newTestApp()
	app.ValidateGovmPlatform = func() error {
		return errors.New("govm requires linux/amd64, linux/arm64, or darwin/arm64")
	}

	err := app.Run([]string{"--prompt", "hi", "--sandbox-backend=govm"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "govm requires") {
		t.Fatalf("expected govm platform error, got %v", err)
	}
}
