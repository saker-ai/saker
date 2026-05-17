// Package logging provides structured logging utilities for saker server mode.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
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

const (
	defaultMaxSize  int64 = 50 * 1024 * 1024 // 50 MB per file
	defaultMaxFiles       = 7                 // keep at most 7 log files
	logPrefix             = "saker"
)

func setupLogger(dir string, stderr bool) (*slog.Logger, func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("logging: mkdir %s: %w", dir, err)
	}

	rw, err := newRotatingWriter(dir, defaultMaxSize, defaultMaxFiles)
	if err != nil {
		return nil, nil, err
	}

	var w io.Writer = rw
	if stderr {
		w = io.MultiWriter(os.Stderr, rw)
	}
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey:
				a.Key = "ts"
			case slog.SourceKey:
				if src, ok := a.Value.Any().(*slog.Source); ok {
					a = slog.String("caller", src.File+":"+strconv.Itoa(src.Line))
				}
			}
			return a
		},
	})
	logger := slog.New(handler)

	cleanup := func() { rw.Close() }
	return logger, cleanup, nil
}

// rotatingWriter is an io.WriteCloser that writes to date-stamped log files
// with automatic rotation when a file exceeds maxSize, and prunes old files
// beyond maxFiles.
type rotatingWriter struct {
	dir      string
	maxSize  int64
	maxFiles int

	mu   sync.Mutex
	file *os.File
	size int64
}

func newRotatingWriter(dir string, maxSize int64, maxFiles int) (*rotatingWriter, error) {
	rw := &rotatingWriter{
		dir:      dir,
		maxSize:  maxSize,
		maxFiles: maxFiles,
	}
	if err := rw.openCurrent(); err != nil {
		return nil, err
	}
	rw.prune()
	return rw, nil
}

func (rw *rotatingWriter) currentName() string {
	return fmt.Sprintf("%s-%s.log", logPrefix, time.Now().Format("2006-01-02"))
}

func (rw *rotatingWriter) openCurrent() error {
	path := filepath.Join(rw.dir, rw.currentName())
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("logging: open %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("logging: stat %s: %w", path, err)
	}
	rw.file = f
	rw.size = info.Size()
	return nil
}

func (rw *rotatingWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if rw.size+int64(len(p)) > rw.maxSize {
		if err := rw.rotate(); err != nil {
			return 0, err
		}
	}

	n, err := rw.file.Write(p)
	rw.size += int64(n)
	return n, err
}

func (rw *rotatingWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.file != nil {
		return rw.file.Close()
	}
	return nil
}

func (rw *rotatingWriter) rotate() error {
	if rw.file != nil {
		rw.file.Close()
	}

	base := filepath.Join(rw.dir, rw.currentName())

	// Shift existing rotated files: .3 → .4, .2 → .3, .1 → .2
	for i := rw.maxFiles - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", base, i)
		dst := fmt.Sprintf("%s.%d", base, i+1)
		os.Rename(src, dst)
	}
	// Current file becomes .1
	os.Rename(base, base+".1")

	return rw.openCurrent()
}

// prune removes the oldest log files when total count exceeds maxFiles.
func (rw *rotatingWriter) prune() {
	entries, err := os.ReadDir(rw.dir)
	if err != nil {
		return
	}

	var logFiles []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, logPrefix+"-") && strings.Contains(name, ".log") {
			logFiles = append(logFiles, e)
		}
	}

	if len(logFiles) <= rw.maxFiles {
		return
	}

	// Sort by modification time, oldest first.
	sort.Slice(logFiles, func(i, j int) bool {
		fi, _ := logFiles[i].Info()
		fj, _ := logFiles[j].Info()
		if fi == nil || fj == nil {
			return logFiles[i].Name() < logFiles[j].Name()
		}
		return fi.ModTime().Before(fj.ModTime())
	})

	toRemove := len(logFiles) - rw.maxFiles
	for i := 0; i < toRemove; i++ {
		os.Remove(filepath.Join(rw.dir, logFiles[i].Name()))
	}
}
