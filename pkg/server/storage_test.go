package server

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/config"
	storagecfg "github.com/cinience/saker/pkg/storage"
)

// TestStorageConfigFromSettings_NilDefaults asserts that the converter never
// panics on a nil Settings or nil Storage block, and instead produces the
// zero Config that storage.Open will fill with osfs defaults.
func TestStorageConfigFromSettings_NilDefaults(t *testing.T) {
	t.Parallel()
	got := storageConfigFromSettings(nil)
	if got.Backend != "" {
		t.Fatalf("nil settings: backend=%q", got.Backend)
	}
	got = storageConfigFromSettings(&config.Settings{})
	if got.Backend != "" {
		t.Fatalf("nil storage: backend=%q", got.Backend)
	}
}

// TestStorageConfigFromSettings_FullMapping covers the happy path where every
// nested config block is populated; ensures field-by-field translation is
// stable so settings.json edits land in the runtime Config.
func TestStorageConfigFromSettings_FullMapping(t *testing.T) {
	t.Parallel()
	in := &config.Settings{
		Storage: &config.StorageConfig{
			Backend:       "s3",
			PublicBaseURL: "https://cdn.example.com",
			TenantPrefix:  "tenant-x",
			OSFS:          &config.StorageOSFSConfig{Root: "/var/media"},
			Embedded: &config.StorageEmbeddedConfig{
				Addr:      "127.0.0.1:9100",
				Root:      "/var/embedded",
				Bucket:    "media",
				AccessKey: "ak",
				SecretKey: "sk",
			},
			S3: &config.StorageS3Config{
				Endpoint:        "https://oss-cn-hangzhou.aliyuncs.com",
				Region:          "cn-hangzhou",
				Bucket:          "my-bucket",
				AccessKeyID:     "AK",
				SecretAccessKey: "SK",
				UsePathStyle:    true,
				PublicBaseURL:   "https://my-bucket.oss-cn-hangzhou.aliyuncs.com",
			},
		},
	}
	got := storageConfigFromSettings(in)
	if got.Backend != "s3" || got.PublicBaseURL != "https://cdn.example.com" || got.TenantPrefix != "tenant-x" {
		t.Fatalf("top-level mismatch: %+v", got)
	}
	if got.OSFS.Root != "/var/media" {
		t.Fatalf("osfs root: %q", got.OSFS.Root)
	}
	if got.Embedded.Addr != "127.0.0.1:9100" || got.Embedded.AccessKey != "ak" {
		t.Fatalf("embedded block: %+v", got.Embedded)
	}
	if got.S3.Endpoint == "" || got.S3.Bucket != "my-bucket" || !got.S3.UsePathStyle {
		t.Fatalf("s3 block: %+v", got.S3)
	}
}

// TestCacheMediaToStore_DataURL_RoundTripsViaMemFS exercises the end-to-end
// flow without network: a data URL is decoded, written through s2.Storage
// (memfs backend), then served back out via handleMediaServe.
func TestCacheMediaToStore_DataURL_RoundTripsViaMemFS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	st, _, err := storagecfg.Open(ctx, storagecfg.Config{Backend: storagecfg.BackendMemFS}, t.TempDir())
	if err != nil {
		t.Fatalf("open memfs: %v", err)
	}
	cfg := storagecfg.Config{Backend: storagecfg.BackendMemFS, PublicBaseURL: "/media"}

	body := []byte("\x89PNG\r\n\x1a\nfake-png-bytes-for-test")
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(body)

	url, err := cacheMediaToStore(ctx, st, cfg, "proj-42", dataURL, "image")
	if err != nil {
		t.Fatalf("cacheMediaToStore: %v", err)
	}
	if !strings.HasPrefix(url, "/media/proj-42/image/") || !strings.HasSuffix(url, ".png") {
		t.Fatalf("unexpected url shape: %q", url)
	}

	// Drive handleMediaServe directly: build a minimal Server that holds
	// just the handler with objectStore set, and dispatch a GET against
	// the URL we got back from cacheMediaToStore.
	h := &Handler{}
	h.SetStorage(st, cfg)
	srv := &Server{handler: h, logger: slog.Default()}

	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	callGinHandler(rec, req, srv.handleMediaServe)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type=%q", ct)
	}
	got, _ := io.ReadAll(rec.Body)
	if string(got) != string(body) {
		t.Fatalf("body mismatch: got=%d bytes want=%d", len(got), len(body))
	}
}

// TestHandleMediaServe_NotFound asserts the 404 path when an unknown key is
// requested. Important so the route doesn't accidentally swallow the error
// and reply 200 with empty body.
func TestHandleMediaServe_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, _, err := storagecfg.Open(ctx, storagecfg.Config{Backend: storagecfg.BackendMemFS}, t.TempDir())
	if err != nil {
		t.Fatalf("open memfs: %v", err)
	}
	h := &Handler{}
	h.SetStorage(st, storagecfg.Config{Backend: storagecfg.BackendMemFS, PublicBaseURL: "/media"})
	srv := &Server{handler: h, logger: slog.Default()}

	req := httptest.NewRequest(http.MethodGet, "/media/nope/missing.png", nil)
	rec := httptest.NewRecorder()
	callGinHandler(rec, req, srv.handleMediaServe)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// TestHandleMediaServe_NoStoreConfigured proves the handler refuses to serve
// when SetStorage was never called — required because we register the route
// only when a store is wired, but a defensive check inside is still cheap.
func TestHandleMediaServe_NoStoreConfigured(t *testing.T) {
	t.Parallel()
	srv := &Server{handler: &Handler{}, logger: slog.Default()}
	req := httptest.NewRequest(http.MethodGet, "/media/whatever.png", nil)
	rec := httptest.NewRecorder()
	callGinHandler(rec, req, srv.handleMediaServe)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
