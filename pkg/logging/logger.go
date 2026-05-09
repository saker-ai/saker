// Package logging provides structured logging utilities for saker server mode.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

type loggerKey struct{}

// WithLogger stores a *slog.Logger in the context.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, loggerKey{}, l)
}

// From retrieves the *slog.Logger from the context.
// Returns slog.Default() if none is set.
func From(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// Setup creates a JSON logger that writes to both stderr and a date-stamped
// log file under dir. It returns the logger and a cleanup function that closes
// the file handle. The log file is named saker-YYYY-MM-DD.log.
func Setup(dir string) (*slog.Logger, func(), error) {
	return setupLogger(dir, true)
}

// SetupCLI creates a JSON logger that writes only to a date-stamped log file
// (no stderr output), and sets it as slog.SetDefault so all global slog calls
// are captured without polluting the terminal. Returns the logger, a cleanup
// function, and any error.
func SetupCLI(dir string) (*slog.Logger, func(), error) {
	logger, cleanup, err := setupLogger(dir, false)
	if err != nil {
		return nil, nil, err
	}
	slog.SetDefault(logger)
	return logger, cleanup, nil
}

func setupLogger(dir string, stderr bool) (*slog.Logger, func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("logging: mkdir %s: %w", dir, err)
	}

	name := fmt.Sprintf("saker-%s.log", time.Now().Format("2006-01-02"))
	path := filepath.Join(dir, name)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("logging: open %s: %w", path, err)
	}

	var w io.Writer = f
	if stderr {
		w = io.MultiWriter(os.Stderr, f)
	}
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler)

	cleanup := func() { f.Close() }
	return logger, cleanup, nil
}
