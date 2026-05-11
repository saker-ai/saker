// bash_format.go: output spooling, combining, truncation, and TTL sweeping for bash command results.
package toolbuiltin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/tool"
)

func combineOutput(stdout, stderr string) string {
	stdout = strings.TrimRight(stdout, "\r\n")
	stderr = strings.TrimRight(stderr, "\r\n")
	switch {
	case stdout == "" && stderr == "":
		return ""
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + "\n" + stderr
	}
}

type bashOutputSpool struct {
	threshold  int
	outputPath string
	stdout     *tool.SpoolWriter
	stderr     *tool.SpoolWriter
}

func newBashOutputSpool(ctx context.Context, threshold int) *bashOutputSpool {
	sessionID := bashSessionID(ctx)
	dir := filepath.Join(bashOutputBaseDir(), sanitizePathComponent(sessionID))
	filename := bashOutputFilename()
	outputPath := filepath.Join(dir, filename)

	spool := &bashOutputSpool{
		threshold:  threshold,
		outputPath: outputPath,
	}
	spool.stdout = tool.NewSpoolWriter(threshold, func() (io.WriteCloser, string, error) {
		return openBashOutputFile(outputPath)
	})
	spool.stderr = tool.NewSpoolWriter(threshold, func() (io.WriteCloser, string, error) {
		if err := ensureBashOutputDir(dir); err != nil {
			return nil, "", err
		}
		f, err := os.CreateTemp(dir, "stderr-*.tmp")
		if err != nil {
			return nil, "", err
		}
		return f, f.Name(), nil
	})
	return spool
}

func (s *bashOutputSpool) StdoutWriter() io.Writer { return s.stdout }

func (s *bashOutputSpool) StderrWriter() io.Writer { return s.stderr }

func (s *bashOutputSpool) Append(text string, isStderr bool) error {
	if isStderr {
		_, err := s.stderr.WriteString(text)
		return err
	}
	_, err := s.stdout.WriteString(text)
	return err
}

func (s *bashOutputSpool) Finalize() (string, string, error) {
	if s == nil {
		return "", "", nil
	}
	stdoutCloseErr := s.stdout.Close()
	stderrCloseErr := s.stderr.Close()
	closeErr := errors.Join(stdoutCloseErr, stderrCloseErr)

	if s.stdout.Truncated() || s.stderr.Truncated() {
		combined := combineOutput(s.stdout.String(), s.stderr.String())
		return combined, "", closeErr
	}

	stdoutPath := s.stdout.Path()
	stderrPath := s.stderr.Path()
	defer func() {
		if stderrPath == "" {
			return
		}
		_ = os.Remove(stderrPath)
	}()

	if stdoutPath == "" && stderrPath == "" {
		combined := combineOutput(s.stdout.String(), s.stderr.String())
		if len(combined) <= s.threshold {
			return combined, "", closeErr
		}
		if err := ensureBashOutputDir(filepath.Dir(s.outputPath)); err != nil {
			return combined, "", errors.Join(closeErr, err)
		}
		if err := os.WriteFile(s.outputPath, []byte(combined), 0o600); err != nil {
			return combined, "", errors.Join(closeErr, err)
		}
		return formatBashOutputReference(s.outputPath), s.outputPath, closeErr
	}

	if stdoutPath == "" {
		if err := ensureBashOutputDir(filepath.Dir(s.outputPath)); err != nil {
			combined := combineOutput(s.stdout.String(), s.stderr.String())
			return combined, "", errors.Join(closeErr, err)
		}
		out, err := os.OpenFile(s.outputPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			combined := combineOutput(s.stdout.String(), s.stderr.String())
			return combined, "", errors.Join(closeErr, err)
		}
		if err := writeCombinedOutput(out, s.stdout.String(), stderrPath, s.stderr.String()); err != nil {
			_ = out.Close()
			return "", "", errors.Join(closeErr, err)
		}
		if err := out.Close(); err != nil {
			return "", "", errors.Join(closeErr, err)
		}
		return formatBashOutputReference(s.outputPath), s.outputPath, closeErr
	}

	out, err := os.OpenFile(s.outputPath, os.O_RDWR, 0)
	if err != nil {
		return "", "", errors.Join(closeErr, err)
	}
	stdoutLen, err := trimRightNewlinesInFile(out)
	if err != nil {
		_ = out.Close()
		return "", "", errors.Join(closeErr, err)
	}
	if err := appendStderr(out, stdoutLen, stderrPath, s.stderr.String()); err != nil {
		_ = out.Close()
		return "", "", errors.Join(closeErr, err)
	}
	if err := out.Close(); err != nil {
		return "", "", errors.Join(closeErr, err)
	}
	return formatBashOutputReference(s.outputPath), s.outputPath, closeErr
}

func writeCombinedOutput(out *os.File, stdoutText, stderrPath, stderrText string) error {
	stdoutTrim := strings.TrimRight(stdoutText, "\r\n")
	if stdoutTrim != "" {
		if _, err := out.WriteString(stdoutTrim); err != nil {
			return err
		}
	}
	return appendStderr(out, int64(len(stdoutTrim)), stderrPath, stderrText)
}

func appendStderr(out *os.File, stdoutLen int64, stderrPath, stderrText string) error {
	stderrTrim := strings.TrimRight(stderrText, "\r\n")
	hasStderr := stderrTrim != "" || stderrPath != ""
	if !hasStderr {
		return nil
	}
	stderrLen := int64(len(stderrTrim))
	if stderrPath != "" {
		f, err := os.Open(stderrPath)
		if err != nil {
			return err
		}
		defer f.Close()
		size, err := trimmedFileSize(f)
		if err != nil {
			return err
		}
		stderrLen = size
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if stdoutLen > 0 && stderrLen > 0 {
			if _, err := out.WriteString("\n"); err != nil {
				return err
			}
		}
		if stderrLen > 0 {
			if _, err := io.CopyN(out, f, stderrLen); err != nil {
				return err
			}
		}
		return nil
	}
	if stdoutLen > 0 && stderrLen > 0 {
		if _, err := out.WriteString("\n"); err != nil {
			return err
		}
	}
	if stderrLen > 0 {
		if _, err := out.WriteString(stderrTrim); err != nil {
			return err
		}
	}
	return nil
}

func bashOutputFilename() string {
	var randBuf [4]byte
	ts := time.Now().UnixNano()
	if _, err := rand.Read(randBuf[:]); err == nil {
		return fmt.Sprintf("%d-%s.txt", ts, hex.EncodeToString(randBuf[:]))
	}
	return fmt.Sprintf("%d.txt", ts)
}

func ensureBashOutputDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("output directory is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	maybeSweepBashOutputDir(dir)
	return nil
}

// bashOutputTTL is how long persisted bash background-output files survive on
// disk before the TTL sweep removes them. 24h is generous enough to cover
// long-running agents that re-fetch output the next morning, while still
// preventing /tmp/saker/bash-output from growing unboundedly across runs.
const bashOutputTTL = 24 * time.Hour

// bashOutputSweepInterval is the minimum gap between two TTL sweeps. Long-running
// servers should still get periodic cleanup without sweeping on every command.
const bashOutputSweepInterval = time.Hour

var (
	bashOutputSweepMu       sync.Mutex
	bashOutputLastSweepTime time.Time
)

// maybeSweepBashOutputDir runs the TTL sweep at most once per
// bashOutputSweepInterval. Sweep work happens on a background goroutine so the
// caller never blocks on IO.
func maybeSweepBashOutputDir(dir string) {
	bashOutputSweepMu.Lock()
	if !bashOutputLastSweepTime.IsZero() && time.Since(bashOutputLastSweepTime) < bashOutputSweepInterval {
		bashOutputSweepMu.Unlock()
		return
	}
	bashOutputLastSweepTime = time.Now()
	bashOutputSweepMu.Unlock()
	go sweepBashOutputDir(dir, bashOutputTTL)
}

// sweepBashOutputDir deletes files in dir older than ttl. Best-effort; logs
// nothing — failures are silently ignored so a permission glitch can't take
// down the bash tool.
func sweepBashOutputDir(dir string, ttl time.Duration) {
	cutoff := time.Now().Add(-ttl)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func openBashOutputFile(path string) (*os.File, string, error) {
	dir := filepath.Dir(path)
	if err := ensureBashOutputDir(dir); err != nil {
		return nil, "", err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, "", err
	}
	return f, path, nil
}

func formatBashOutputReference(path string) string {
	return fmt.Sprintf("[Output saved to: %s]", path)
}

func trimmedFileSize(f *os.File) (int64, error) {
	if f == nil {
		return 0, nil
	}
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if size == 0 {
		return 0, nil
	}

	const chunkSize int64 = 1024
	offset := size
	trimmed := size

	for offset > 0 {
		readSize := chunkSize
		if readSize > offset {
			readSize = offset
		}
		buf := make([]byte, readSize)
		if _, err := f.ReadAt(buf, offset-readSize); err != nil {
			return 0, err
		}
		i := len(buf) - 1
		for i >= 0 {
			if buf[i] != '\n' && buf[i] != '\r' {
				break
			}
			i--
		}
		trimmed = (offset - readSize) + int64(i+1)
		if i >= 0 {
			break
		}
		offset -= readSize
	}
	if trimmed < 0 {
		return 0, nil
	}
	return trimmed, nil
}

func trimRightNewlinesInFile(f *os.File) (int64, error) {
	if f == nil {
		return 0, nil
	}
	trimmed, err := trimmedFileSize(f)
	if err != nil {
		return 0, err
	}
	if err := f.Truncate(trimmed); err != nil {
		return 0, err
	}
	if _, err := f.Seek(trimmed, io.SeekStart); err != nil {
		return 0, err
	}
	return trimmed, nil
}
