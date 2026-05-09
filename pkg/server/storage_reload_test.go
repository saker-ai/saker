package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cinience/saker/pkg/api"
	storagecfg "github.com/cinience/saker/pkg/storage"
)

// writeSettings rewrites the project's settings.local.json with the supplied
// JSON body and reloads the runtime so subsequent reloadObjectStore calls
// observe the new shape.
func writeSettings(t *testing.T, rt *api.Runtime, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".saker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.local.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rt.ReloadSettings(); err != nil {
		t.Fatalf("reload settings: %v", err)
	}
}

// newReloadServer builds a Server with the minimal pieces reloadObjectStore
// touches: a runtime that holds the settings file, a handler that exposes the
// SetStorage/SetStorageReloader hooks, plus an embeddedMu so the swap path
// has a real lock to grab.
func newReloadServer(t *testing.T) (*Server, *api.Runtime, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".saker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot:  root,
		Model:        noopModel{},
		SystemPrompt: "test",
	})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	t.Cleanup(func() { rt.Close() })

	h := &Handler{
		runtime: rt,
		logger:  slog.Default(),
	}
	srv := &Server{
		runtime: rt,
		handler: h,
		logger:  slog.Default(),
		opts:    Options{DataDir: t.TempDir()},
	}
	return srv, rt, root
}

// TestServer_ReloadObjectStore_SwapBackend verifies a hot reload from the
// default osfs to the in-process embedded backend swaps the live store and
// the embedded /_s3/ handler becomes available without rebuilding the mux.
func TestServer_ReloadObjectStore_SwapBackend(t *testing.T) {
	t.Parallel()
	srv, rt, root := newReloadServer(t)

	// Initial open: defaults to osfs at <DataDir>/media.
	if err := srv.reloadObjectStore(context.Background()); err != nil {
		t.Fatalf("initial reload (osfs): %v", err)
	}
	store1, _ := srv.handler.objectStoreSnapshot()
	if store1 == nil {
		t.Fatalf("expected osfs store after initial open")
	}
	if h := srv.embeddedHandler(); h != nil {
		t.Fatalf("osfs path should not expose embedded handler")
	}

	// Switch to embedded-external. The runtime needs the new settings on
	// disk before we ask it to reload.
	writeSettings(t, rt, root, `{"storage":{"backend":"embedded","embedded":{"mode":"external","bucket":"media","accessKey":"ak","secretKey":"sk"}}}`)
	if err := srv.reloadObjectStore(context.Background()); err != nil {
		t.Fatalf("reload to embedded: %v", err)
	}

	store2, cfg2 := srv.handler.objectStoreSnapshot()
	if store2 == nil || store2 == store1 {
		t.Fatalf("expected new store after reload (got same=%v)", store2 == store1)
	}
	if cfg2.Backend != storagecfg.BackendEmbedded {
		t.Fatalf("config backend after reload = %q want=%q", cfg2.Backend, storagecfg.BackendEmbedded)
	}
	if h := srv.embeddedHandler(); h == nil {
		t.Fatalf("embedded handler should be live after reload")
	}

	// Drive a request through the lazy /_s3/ wrapper: hit /healthz against
	// the swapped-in embedded handler. Mirrors the production mount.
	mux := http.NewServeMux()
	mux.Handle("/_s3/", http.StripPrefix("/_s3", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := srv.embeddedHandler()
		if h == nil {
			http.Error(w, "no s3", http.StatusNotFound)
			return
		}
		h.ServeHTTP(w, r)
	})))
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
}

// TestServer_ReloadObjectStore_FailureKeepsOld asserts an open error from a
// bad backend leaves the previously-installed store intact, so a typo in the
// admin form doesn't take media offline.
func TestServer_ReloadObjectStore_FailureKeepsOld(t *testing.T) {
	t.Parallel()
	srv, rt, root := newReloadServer(t)

	if err := srv.reloadObjectStore(context.Background()); err != nil {
		t.Fatalf("initial reload (osfs): %v", err)
	}
	good, _ := srv.handler.objectStoreSnapshot()
	if good == nil {
		t.Fatalf("expected initial store")
	}

	writeSettings(t, rt, root, `{"storage":{"backend":"nope"}}`)
	if err := srv.reloadObjectStore(context.Background()); err == nil {
		t.Fatalf("reload with unknown backend should error")
	}

	after, _ := srv.handler.objectStoreSnapshot()
	if after != good {
		t.Fatalf("failed reload replaced the live store; want=%p got=%p", good, after)
	}
}
