package server

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jkO8AAAAASUVORK5CYII="

func TestCacheMediaForProjectDownloadsRemoteImage(t *testing.T) {
	t.Parallel()

	pngBytes, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer server.Close()

	projectRoot := t.TempDir()
	cached, err := cacheMediaForProject(projectRoot, server.URL+"/generated.png?sig=abc", "image")
	if err != nil {
		t.Fatalf("cache media: %v", err)
	}

	if cached.SourceURL == "" || !strings.Contains(cached.SourceURL, "generated.png") {
		t.Fatalf("source url = %q", cached.SourceURL)
	}
	if filepath.Ext(cached.Path) != ".png" {
		t.Fatalf("cached path ext = %q", filepath.Ext(cached.Path))
	}
	if !strings.HasPrefix(cached.URL, "/api/files/") && !strings.HasPrefix(cached.URL, "/api/files/") {
		t.Fatalf("cached url = %q", cached.URL)
	}
	if _, err := os.Stat(cached.Path); err != nil {
		t.Fatalf("stat cached path: %v", err)
	}
}

func TestCacheMediaForProjectSupportsDataURL(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	dataURL := "data:image/png;base64," + tinyPNGBase64
	cached, err := cacheMediaForProject(projectRoot, dataURL, "image")
	if err != nil {
		t.Fatalf("cache media from data url: %v", err)
	}

	if cached.SourceURL != "" {
		t.Fatalf("source url = %q, want empty", cached.SourceURL)
	}
	if filepath.Ext(cached.Path) != ".png" {
		t.Fatalf("cached path ext = %q", filepath.Ext(cached.Path))
	}

	abs, err := resolveLocalMediaPath(projectRoot, cached.URL)
	if err != nil {
		t.Fatalf("resolve local media path: %v", err)
	}
	if abs != cached.Path {
		t.Fatalf("resolved path = %q, want %q", abs, cached.Path)
	}
}

// TestSweepMediaCache_KeepsRecentAndReferenced verifies the multi-tenant
// cleanup primitives: only files that are both unreferenced AND older than
// the 7-day cutoff are removed.
func TestSweepMediaCache_KeepsRecentAndReferenced(t *testing.T) {
	t.Parallel()

	cacheDir := t.TempDir()
	mkfile := func(name string, age time.Duration) string {
		p := filepath.Join(cacheDir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		mt := time.Now().Add(-age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
		return p
	}

	keepRecent := mkfile("recent.png", 1*time.Hour)
	keepReferenced := mkfile("ref.png", 30*24*time.Hour)
	dropOld := mkfile("stale.png", 30*24*time.Hour)

	referenced := map[string]bool{
		keepReferenced: true,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sweepMediaCache(cacheDir, referenced, logger)

	for _, p := range []string{keepRecent, keepReferenced} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to be retained, got %v", filepath.Base(p), err)
		}
	}
	if _, err := os.Stat(dropOld); !os.IsNotExist(err) {
		t.Errorf("expected %s to be removed, got err=%v", filepath.Base(dropOld), err)
	}
}

// TestCollectMediaReferences_Union verifies the helper unions refs from a
// SessionStore into the caller-provided map. The union shape is what lets
// the multi-tenant sweep accumulate refs from every project before deciding
// which files to remove from the (still-shared) on-disk cache directory.
func TestCollectMediaReferences_Union(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewSessionStore(dir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	thread := store.CreateThread("t")
	store.AppendItemWithArtifacts(thread.ID, "assistant", "x", "turn-1", []Artifact{
		{Type: "image", URL: "/api/files" + filepath.Join(dir, "ref.png")},
		{Type: "image", URL: "https://example.com/skip.png"}, // non /api/files prefix is ignored
	})

	referenced := map[string]bool{"/preexisting": true}
	collectMediaReferences(store, referenced)

	if !referenced["/preexisting"] {
		t.Errorf("preexisting entry was wiped out")
	}
	want := filepath.Join(dir, "ref.png")
	if !referenced[want] {
		t.Errorf("expected %q in referenced set, got %v", want, referenced)
	}
	if _, ok := referenced["https://example.com/skip.png"]; ok {
		t.Errorf("non /api/files URL should not have been collected")
	}

	// nil store must be a no-op (used by Server when projects=nil).
	collectMediaReferences(nil, referenced)
}
