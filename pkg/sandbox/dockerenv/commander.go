package dockerenv

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

// commander abstracts docker CLI invocation so tests can stub the binary
// without spinning up real containers. Production code uses execCommander.
type commander interface {
	// Run executes argv with optional stdin and returns stdout/stderr/exit.
	// timeout=0 means no extra deadline (caller's ctx still applies).
	Run(ctx context.Context, argv []string, stdin io.Reader, timeout time.Duration) (cmdResult, error)

	// Stream executes argv and forwards stdout/stderr line-by-line.
	Stream(ctx context.Context, argv []string, stdin io.Reader, timeout time.Duration, onStdout, onStderr func(string)) (cmdResult, error)
}

type cmdResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

type execCommander struct {
	binary string
}

func (c *execCommander) Run(ctx context.Context, argv []string, stdin io.Reader, timeout time.Duration) (cmdResult, error) {
	if len(argv) == 0 {
		return cmdResult{}, errors.New("dockerenv: empty argv")
	}
	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	start := time.Now()
	cmd := exec.CommandContext(runCtx, c.binary, argv...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := cmdResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Duration: time.Since(start),
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			// Non-zero exit is not a Go error for our purposes — command ran.
			return res, nil
		}
		return res, err
	}
	return res, nil
}

func (c *execCommander) Stream(ctx context.Context, argv []string, stdin io.Reader, timeout time.Duration, onStdout, onStderr func(string)) (cmdResult, error) {
	if len(argv) == 0 {
		return cmdResult{}, errors.New("dockerenv: empty argv")
	}
	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	start := time.Now()
	cmd := exec.CommandContext(runCtx, c.binary, argv...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return cmdResult{}, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return cmdResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return cmdResult{}, err
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	done := make(chan struct{}, 2)
	go pumpLines(stdoutPipe, &stdoutBuf, onStdout, done)
	go pumpLines(stderrPipe, &stderrBuf, onStderr, done)
	<-done
	<-done

	res := cmdResult{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		ExitCode: 0,
		Duration: time.Since(start),
	}
	err = cmd.Wait()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			res.Duration = time.Since(start)
			return res, nil
		}
		return res, err
	}
	res.Duration = time.Since(start)
	return res, nil
}

// pumpLines copies r into buf and invokes cb per line. Always signals done.
func pumpLines(r io.Reader, buf *bytes.Buffer, cb func(string), done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	tmp := make([]byte, 4096)
	var pending bytes.Buffer
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
			if cb != nil {
				pending.Write(tmp[:n])
				flushLines(&pending, cb)
			}
		}
		if err != nil {
			if cb != nil && pending.Len() > 0 {
				cb(pending.String())
			}
			return
		}
	}
}

func flushLines(pending *bytes.Buffer, cb func(string)) {
	for {
		idx := bytes.IndexByte(pending.Bytes(), '\n')
		if idx < 0 {
			return
		}
		line := pending.Next(idx + 1)
		cb(string(line))
	}
}
