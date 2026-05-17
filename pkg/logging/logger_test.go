package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

func TestSetup_DebugLevel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	logger, cleanup, err := Setup(dir)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer cleanup()

	logger.Debug("debug message", "level", "debug")

	name := "saker-" + time.Now().Format("2006-01-02") + ".log"
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "debug message") {
		t.Fatal("debug-level message not written to log file")
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

func TestRotatingWriter_Basic(t *testing.T) {
	dir := t.TempDir()
	rw, err := newRotatingWriter(dir, 1024, 3)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	defer rw.Close()

	msg := []byte("hello rotating writer\n")
	n, err := rw.Write(msg)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(msg) {
		t.Fatalf("wrote %d bytes, want %d", n, len(msg))
	}

	name := fmt.Sprintf("saker-%s.log", time.Now().Format("2006-01-02"))
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "hello rotating writer") {
		t.Fatal("expected content not in log file")
	}
}

func TestRotatingWriter_Rotation(t *testing.T) {
	dir := t.TempDir()
	maxSize := int64(100) // tiny limit to trigger rotation quickly
	rw, err := newRotatingWriter(dir, maxSize, 5)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	defer rw.Close()

	chunk := strings.Repeat("x", 60) + "\n" // 61 bytes each
	// Write twice: first fits, second triggers rotation
	for i := 0; i < 3; i++ {
		if _, err := rw.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write #%d: %v", i, err)
		}
	}

	// Should have current file + at least one rotated .1 file
	base := fmt.Sprintf("saker-%s.log", time.Now().Format("2006-01-02"))
	if _, err := os.Stat(filepath.Join(dir, base)); err != nil {
		t.Fatalf("current log missing: %v", err)
	}
	rotated := base + ".1"
	if _, err := os.Stat(filepath.Join(dir, rotated)); err != nil {
		t.Fatalf("rotated log .1 missing: %v", err)
	}
}

func TestRotatingWriter_Prune(t *testing.T) {
	dir := t.TempDir()

	// Pre-create 5 old log files
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("saker-2025-01-%02d.log", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old"), 0o644); err != nil {
			t.Fatalf("create old log: %v", err)
		}
		// Stagger mod times so sort is deterministic
		ts := time.Date(2025, 1, i, 0, 0, 0, 0, time.UTC)
		os.Chtimes(filepath.Join(dir, name), ts, ts)
	}

	// Create writer with maxFiles=3 — should prune 3 oldest (5 old + 1 new = 6, keep 3)
	rw, err := newRotatingWriter(dir, defaultMaxSize, 3)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	rw.Close()

	entries, _ := os.ReadDir(dir)
	var logFiles []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "saker-") {
			logFiles = append(logFiles, e.Name())
		}
	}

	if len(logFiles) > 3 {
		t.Errorf("expected at most 3 log files after prune, got %d: %v", len(logFiles), logFiles)
	}
}
