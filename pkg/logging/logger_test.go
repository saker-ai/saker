package logging

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSetup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	logger, cleanup, err := Setup(dir)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer cleanup()

	if logger == nil {
		t.Fatal("expected non-nil logger")
	}

	// Verify log file was created.
	name := "saker-" + time.Now().Format("2006-01-02") + ".log"
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("log file not found: %v", err)
	}

	// Write a log entry and verify it appears in the file.
	logger.Info("test message", "key", "value")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("log file is empty after writing")
	}
}

func TestSetupBadDir(t *testing.T) {
	_, _, err := Setup("/dev/null/impossible")
	if err == nil {
		t.Fatal("expected error for invalid directory")
	}
}

func TestWithLoggerAndFrom(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx := WithLogger(context.Background(), logger)
	got := From(ctx)
	if got != logger {
		t.Fatal("expected same logger from context")
	}
}

func TestFromNilContext(t *testing.T) {
	got := From(nil)
	if got == nil {
		t.Fatal("expected non-nil default logger")
	}
}

func TestFromEmptyContext(t *testing.T) {
	got := From(context.Background())
	if got == nil {
		t.Fatal("expected non-nil default logger")
	}
}

func TestWithLoggerNilContext(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := WithLogger(nil, logger)
	got := From(ctx)
	if got != logger {
		t.Fatal("expected same logger from nil-origin context")
	}
}
