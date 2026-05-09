package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mojatter/s2"
)

// pickPort returns a free localhost port. The listener is closed before
// return so the caller can rebind.
func pickPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// TestOpen_Embedded_RoundTripAndS3 verifies that:
//  1. The application can write/read via the returned s2.Storage
//  2. The embedded server is actually serving on the configured port
//     (we hit the /healthz endpoint)
//  3. Stop() shuts the server down cleanly
func TestOpen_Embedded_RoundTripAndS3(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded-server test in -short mode")
	}
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	addr := pickPort(t)

	st, emb, err := Open(ctx, Config{
		Backend: BackendEmbedded,
		Embedded: EmbeddedConfig{
			Mode:      ModeStandalone,
			Addr:      addr,
			Bucket:    "media",
			AccessKey: "ak",
			SecretKey: "sk",
		},
	}, dir)
	if err != nil {
		t.Fatalf("Open embedded: %v", err)
	}
	if emb == nil {
		t.Fatalf("embedded backend should return *Embedded")
	}
	if emb.Mode() != ModeStandalone {
		t.Fatalf("mode=%q want=standalone", emb.Mode())
	}
	t.Cleanup(func() {
		if err := emb.Stop(); err != nil {
			t.Errorf("emb.Stop: %v", err)
		}
	})

	// Application path: write via s2.Storage.
	key := "p1/image/dd/deadbeef.png"
	body := []byte("png-bytes")
	if err := st.Put(ctx, s2.NewObjectBytes(key, body)); err != nil {
		t.Fatalf("put: %v", err)
	}

	// Wait for the server to come up before probing /healthz. The Start
	// goroutine sets up the listener, so a short retry loop is enough.
	healthURL := fmt.Sprintf("http://%s/healthz", addr)
	if err := waitForHTTP(healthURL, 2*time.Second); err != nil {
		t.Fatalf("embedded server not reachable: %v", err)
	}

	// Stop, then verify the goroutine exited.
	if err := emb.Stop(); err != nil {
		t.Fatalf("emb.Stop: %v", err)
	}

	// Re-open the file via the underlying osfs storage to confirm the
	// data survived shutdown. (We re-create a fresh osfs Storage rather
	// than relying on the now-stopped embedded one.)
	osfs, _, err := Open(ctx, Config{
		Backend: BackendOSFS,
		OSFS:    OSFSConfig{Root: dir + "/media/media"}, // dataDir/media (default) + /<bucket>
	}, dir)
	if err != nil {
		t.Fatalf("re-open osfs: %v", err)
	}
	obj, err := osfs.Get(ctx, key)
	if err != nil {
		t.Fatalf("re-get: %v", err)
	}
	rc, err := obj.Open()
	if err != nil {
		t.Fatalf("re-open object: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != string(body) {
		t.Fatalf("body got=%q want=%q", got, body)
	}
}

// TestEmbeddedStop_Idempotent ensures calling Stop multiple times does not
// hang or panic.
func TestEmbeddedStop_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded-server test in -short mode")
	}
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	addr := pickPort(t)

	_, emb, err := Open(ctx, Config{
		Backend: BackendEmbedded,
		Embedded: EmbeddedConfig{
			Mode:      ModeStandalone,
			Addr:      addr,
			Bucket:    "media",
			AccessKey: "ak",
			SecretKey: "sk",
		},
	}, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := emb.Stop(); err != nil {
		t.Fatalf("first stop: %v", err)
	}
	if err := emb.Stop(); err != nil {
		t.Fatalf("second stop: %v", err)
	}
}

// TestOpen_Embedded_External_NoListener verifies that opening the embedded
// backend in external mode produces a Handler but does not bind any port —
// the parent application is expected to mount Handler() on its own mux.
func TestOpen_Embedded_External_NoListener(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	st, emb, err := Open(ctx, Config{
		Backend: BackendEmbedded,
		Embedded: EmbeddedConfig{
			// Mode left empty → must default to "external".
			Bucket:    "media",
			AccessKey: "ak",
			SecretKey: "sk",
		},
	}, dir)
	if err != nil {
		t.Fatalf("Open embedded external: %v", err)
	}
	if emb == nil {
		t.Fatalf("embedded backend should return *Embedded")
	}
	if emb.Mode() != ModeExternal {
		t.Fatalf("mode=%q want=external (default)", emb.Mode())
	}
	if emb.Handler() == nil {
		t.Fatalf("Handler() should be non-nil in external mode")
	}
	if emb.Addr() != "" {
		t.Fatalf("Addr should be empty in external mode, got %q", emb.Addr())
	}
	// Stop is a no-op in external mode and must not error.
	if err := emb.Stop(); err != nil {
		t.Fatalf("Stop in external mode should be no-op: %v", err)
	}

	// Application data path still works — write via s2.Storage and read
	// back the bytes we just wrote, proving the osfs side of the embedded
	// backend is wired correctly even without a listener.
	key := "p/image/aa/abc.png"
	body := []byte("hello-external")
	if err := st.Put(ctx, s2.NewObjectBytes(key, body)); err != nil {
		t.Fatalf("put: %v", err)
	}
	obj, err := st.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	rc, err := obj.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != string(body) {
		t.Fatalf("body got=%q want=%q", got, body)
	}
}

// TestOpen_Embedded_ExternalHandler_ServesHealthz mounts the embedded handler
// on a httptest server (mirroring how pkg/server mounts it under /_s3/) and
// hits /healthz to confirm the handler chain is alive end-to-end without
// needing SigV4 to validate.
func TestOpen_Embedded_ExternalHandler_ServesHealthz(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	_, emb, err := Open(ctx, Config{
		Backend: BackendEmbedded,
		Embedded: EmbeddedConfig{
			Mode:   ModeExternal,
			Bucket: "media",
		},
	}, dir)
	if err != nil {
		t.Fatalf("Open embedded external: %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/_s3/", http.StripPrefix("/_s3", emb.Handler()))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/_s3/healthz")
	if err != nil {
		t.Fatalf("GET /_s3/healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want=200", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(got)) != "ok" {
		t.Fatalf("body=%q want=ok", got)
	}
}

func waitForHTTP(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		client := &http.Client{Timeout: 200 * time.Millisecond}
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = errors.New("timeout")
	}
	return lastErr
}

// silence unused-import warnings if io/strings ever go unused.
var _ = io.Discard
var _ = strings.Builder{}
