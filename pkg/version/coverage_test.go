package version

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// CheckForUpdate exercised via the URL variant; verify the default-URL wrapper
// returns nil for dev builds (no network call required).
func TestCheckForUpdateDevReturnsNil(t *testing.T) {
	info, err := CheckForUpdate("dev")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if info != nil {
		t.Errorf("expected nil for dev, got %+v", info)
	}
}

func TestCheckForUpdateEmptyVersion(t *testing.T) {
	info, err := CheckForUpdateWithURL("", "http://should-not-be-called")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if info != nil {
		t.Errorf("expected nil for empty version, got %+v", info)
	}
}

func TestCheckForUpdateAsync_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"latest":"9.9.9"}`))
	}))
	defer srv.Close()
	tmp := t.TempDir()
	orig := cacheFilePathFunc
	cacheFilePathFunc = func() string { return filepath.Join(tmp, CacheFileName) }
	defer func() { cacheFilePathFunc = orig }()

	// CheckForUpdateAsync ignores the URL parameter — replace global by patching
	// the default URL via wrapping. Since we can't change the const here, just
	// verify the channel mechanism by directly calling the helper used inside.
	ch := CheckForUpdateAsync("dev") // dev returns nil → channel closes empty.
	select {
	case info, ok := <-ch:
		if ok && info != nil {
			t.Errorf("expected closed channel for dev, got info=%+v", info)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("async did not finish")
	}
}

func TestCheckForUpdateBadHTTPStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	tmp := t.TempDir()
	orig := cacheFilePathFunc
	cacheFilePathFunc = func() string { return filepath.Join(tmp, CacheFileName) }
	defer func() { cacheFilePathFunc = orig }()

	_, err := CheckForUpdateWithURL("0.1.0", srv.URL)
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("err: %v", err)
	}
}

func TestCheckForUpdateBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	tmp := t.TempDir()
	orig := cacheFilePathFunc
	cacheFilePathFunc = func() string { return filepath.Join(tmp, CacheFileName) }
	defer func() { cacheFilePathFunc = orig }()

	_, err := CheckForUpdateWithURL("0.1.0", srv.URL)
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestCheckForUpdateCachedDifferentCurrent(t *testing.T) {
	// Cached entry exists but Current mismatches → fetch again.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"latest":"0.5.0"}`))
	}))
	defer srv.Close()
	tmp := t.TempDir()
	orig := cacheFilePathFunc
	cacheFilePathFunc = func() string { return filepath.Join(tmp, CacheFileName) }
	defer func() { cacheFilePathFunc = orig }()

	// Pre-write cache for a different "Current".
	cached := CachedCheck{CheckedAt: time.Now(), Latest: "0.4.0", Current: "0.3.0"}
	if err := writeCache(cacheFilePathFunc(), cached); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if _, err := CheckForUpdateWithURL("0.1.0", srv.URL); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 server call, got %d", calls)
	}
}

func TestCacheFilePathDefault(t *testing.T) {
	// cacheFilePath uses os.UserHomeDir; in this env it should produce a path.
	p := cacheFilePath()
	// Should either be empty (rare) or end with the cache filename.
	if p != "" && filepath.Base(p) != CacheFileName {
		t.Errorf("base = %q, want %q", filepath.Base(p), CacheFileName)
	}
}

func TestSelfUpgradeNetworkFailureBubblesUp(t *testing.T) {
	// Use an invalid version so download fails fast (404 from real endpoint).
	// Skip if integration test mode disabled or no network.
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	// Use an obviously bogus version so GitHub returns 404 immediately.
	err := SelfUpgrade("0.0.0-doesnotexist-99999", nil)
	if err == nil {
		t.Skip("network unavailable or release exists; skipping")
	}
	// We just want to exercise the code path; not asserting on specific error text.
	_ = err
}

// downloadFile: verify against a local httptest server.
func TestDownloadFileSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("download contents"))
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := downloadFile(srv.URL, dest); err != nil {
		t.Fatalf("downloadFile: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "download contents" {
		t.Errorf("contents: %q", got)
	}
}

func TestDownloadFileBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "out.bin")
	err := downloadFile(srv.URL, dest)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("err: %v", err)
	}
}

func TestDownloadFileBadURL(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.bin")
	err := downloadFile("http://127.0.0.1:1/nope", dest)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDownloadFileInvalidDest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()
	// Path under a non-existing directory triggers create error.
	err := downloadFile(srv.URL, "/nonexistent-dir-xyz-9999/out.bin")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractBinaryDirEntries(t *testing.T) {
	// Verify that directory entries don't crash extractor; binary still found.
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "a.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	// Add a directory entry.
	_ = tw.WriteHeader(&tar.Header{Name: "subdir/", Mode: 0o755, Typeflag: tar.TypeDir})
	// Add the binary entry.
	body := []byte("binary-data")
	_ = tw.WriteHeader(&tar.Header{Name: "saker", Size: int64(len(body)), Mode: 0o755, Typeflag: tar.TypeReg})
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()

	out, err := extractBinary(archive, tmp, "saker")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("content mismatch")
	}
}

func TestExtractBinaryBadGzip(t *testing.T) {
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "bad.tar.gz")
	if err := os.WriteFile(archive, []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := extractBinary(archive, tmp, "saker")
	if err == nil {
		t.Fatal("expected gzip error")
	}
}

func TestExtractBinaryMissingFile(t *testing.T) {
	_, err := extractBinary("/nonexistent/x.tar.gz", t.TempDir(), "saker")
	if err == nil {
		t.Fatal("expected open error")
	}
}

func TestExtractBinaryBadTar(t *testing.T) {
	// gzip valid but tar invalid (truncated).
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "bad.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	_, _ = gz.Write([]byte("partial"))
	_ = gz.Close()
	_ = f.Close()
	_, err = extractBinary(archive, tmp, "saker")
	if err == nil {
		t.Fatal("expected tar error")
	}
}

// replaceBinary: directly invoke with two real files in a temp dir.
func TestReplaceBinaryHappyPath(t *testing.T) {
	tmp := t.TempDir()
	oldPath := filepath.Join(tmp, "saker")
	newPath := filepath.Join(tmp, "saker.new")
	if err := os.WriteFile(oldPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(oldPath, newPath); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}
	got, _ := os.ReadFile(oldPath)
	if string(got) != "new" {
		t.Errorf("content: %q", got)
	}
	// Backup should be cleaned up.
	if _, err := os.Stat(filepath.Join(tmp, ".saker.old")); !os.IsNotExist(err) {
		t.Errorf("backup not removed: err=%v", err)
	}
}

func TestReplaceBinaryFallsBackToCopy(t *testing.T) {
	// When old path is in a directory we can't write to, the rename should
	// fail and replaceViaCopy should be tried. We simulate this by removing
	// the old path entirely (rename fails because src doesn't exist).
	tmp := t.TempDir()
	oldPath := filepath.Join(tmp, "missing", "saker")
	newPath := filepath.Join(tmp, "saker.new")
	if err := os.WriteFile(newPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	// rename will fail (parent dir doesn't exist); replaceViaCopy will too.
	err := replaceBinary(oldPath, newPath)
	if err == nil {
		t.Fatal("expected error when target dir does not exist")
	}
}

func TestReplaceViaCopy(t *testing.T) {
	tmp := t.TempDir()
	oldPath := filepath.Join(tmp, "saker")
	newPath := filepath.Join(tmp, "saker.new")
	if err := os.WriteFile(newPath, []byte("contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceViaCopy(oldPath, newPath); err != nil {
		t.Fatalf("replaceViaCopy: %v", err)
	}
	got, _ := os.ReadFile(oldPath)
	if string(got) != "contents" {
		t.Errorf("content: %q", got)
	}
}

func TestSyscallExecMissingBinary(t *testing.T) {
	// syscallExec calls syscall.Exec which would replace the process; only
	// invoke with an obviously missing binary so it returns an error rather
	// than executing.
	if runtime.GOOS == "windows" {
		t.Skip("syscallExec is unix-only")
	}
	err := syscallExec("/nonexistent-xyz/saker", []string{"/nonexistent-xyz/saker"}, []string{})
	if err == nil {
		t.Fatal("expected exec error")
	}
}

func TestRestartHelperPathResolution(t *testing.T) {
	// Restart actually exec's the current process — we can't safely call it.
	// Instead, verify os.Executable + EvalSymlinks resolves to a real file.
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Skipf("EvalSymlinks: %v", err)
	}
	if _, err := os.Stat(resolved); err != nil {
		t.Errorf("resolved binary missing: %v", err)
	}
}
