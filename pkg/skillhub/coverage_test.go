package skillhub

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ----- Search / List / Get / Versions -----

func TestSearch(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		limit     int
		wantLimit string
	}{
		{"default limit when zero", "foo", 0, "20"},
		{"default limit when negative", "foo", -3, "20"},
		{"default limit when too high", "foo", 1000, "20"},
		{"explicit limit", "foo bar", 50, "50"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/search" {
					t.Errorf("path: got %q", r.URL.Path)
				}
				if got := r.URL.Query().Get("limit"); got != tt.wantLimit {
					t.Errorf("limit: got %q, want %q", got, tt.wantLimit)
				}
				if got := r.URL.Query().Get("q"); got != tt.query {
					t.Errorf("q: got %q, want %q", got, tt.query)
				}
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(SearchResult{
					Hits:               []SearchHit{{Slug: "a/b", DisplayName: "A B"}},
					EstimatedTotalHits: 1,
				})
			}))
			defer srv.Close()
			c := New(srv.URL)
			res, err := c.Search(context.Background(), tt.query, tt.limit)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if res.EstimatedTotalHits != 1 || len(res.Hits) != 1 || res.Hits[0].Slug != "a/b" {
				t.Errorf("unexpected result: %+v", res)
			}
		})
	}
}

func TestSearchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	_, err := c.Search(context.Background(), "x", 0)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/skills" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("limit") != "5" {
			t.Errorf("limit: got %q", q.Get("limit"))
		}
		if q.Get("category") != "writing" {
			t.Errorf("category: got %q", q.Get("category"))
		}
		if q.Get("sort") != "popular" {
			t.Errorf("sort: got %q", q.Get("sort"))
		}
		if q.Get("cursor") != "abc" {
			t.Errorf("cursor: got %q", q.Get("cursor"))
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(ListResult{
			Data:       []Skill{{ID: "1", Slug: "x/y"}},
			NextCursor: "next",
		})
	}))
	defer srv.Close()
	c := New(srv.URL)
	res, err := c.List(context.Background(), "writing", "popular", "abc", 5)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.NextCursor != "next" || len(res.Data) != 1 {
		t.Errorf("unexpected: %+v", res)
	}
}

func TestListDefaultLimit(t *testing.T) {
	var capturedLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedLimit = r.URL.Query().Get("limit")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	if _, err := c.List(context.Background(), "", "", "", 0); err != nil {
		t.Fatalf("List: %v", err)
	}
	if capturedLimit != "20" {
		t.Errorf("default limit: got %q, want 20", capturedLimit)
	}
	if _, err := c.List(context.Background(), "", "", "", 200); err != nil {
		t.Fatalf("List big limit: %v", err)
	}
	if capturedLimit != "20" {
		t.Errorf("clamped limit: got %q, want 20", capturedLimit)
	}
}

func TestGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/skills/owner%2Fname" && r.URL.Path != "/api/v1/skills/owner/name" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Skill{ID: "1", Slug: "owner/name", Category: "writing"})
	}))
	defer srv.Close()
	c := New(srv.URL)
	s, err := c.Get(context.Background(), "owner/name")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if s.Slug != "owner/name" {
		t.Errorf("slug: got %q", s.Slug)
	}
}

func TestGetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"missing"}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	_, err := c.Get(context.Background(), "missing/skill")
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != 404 {
		t.Errorf("got %T %v", err, err)
	}
}

func TestVersions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/versions") {
			t.Errorf("path: got %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"versions":[{"id":"v1","version":"1.0.0"},{"id":"v2","version":"1.1.0"}]}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	vers, err := c.Versions(context.Background(), "owner/name")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(vers) != 2 {
		t.Fatalf("len: got %d, want 2", len(vers))
	}
	if vers[0].Version != "1.0.0" || vers[1].Version != "1.1.0" {
		t.Errorf("unexpected: %+v", vers)
	}
}

func TestVersionsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := New(srv.URL)
	_, err := c.Versions(context.Background(), "x/y")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ----- Download -----

func TestDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/download" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("slug") != "a/b" {
			t.Errorf("slug: got %q", q.Get("slug"))
		}
		if q.Get("version") != "1.2.3" {
			t.Errorf("version: got %q", q.Get("version"))
		}
		w.Header().Set("ETag", `"abc"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("zip-bytes"))
	}))
	defer srv.Close()
	c := New(srv.URL)
	body, etag, err := c.Download(context.Background(), "a/b", "1.2.3", "")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer body.Close()
	if etag != `"abc"` {
		t.Errorf("etag: got %q", etag)
	}
	got, _ := io.ReadAll(body)
	if string(got) != "zip-bytes" {
		t.Errorf("body: got %q", string(got))
	}
}

func TestDownloadNotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != `"existing"` {
			t.Errorf("missing If-None-Match: got %q", r.Header.Get("If-None-Match"))
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()
	c := New(srv.URL)
	body, etag, err := c.Download(context.Background(), "a/b", "", `"existing"`)
	if err == nil || err != ErrNotModified {
		t.Fatalf("err: got %v, want ErrNotModified", err)
	}
	if body != nil {
		t.Error("body should be nil")
	}
	if etag != "" {
		t.Errorf("etag should be empty, got %q", etag)
	}
}

func TestDownloadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	_, _, err := c.Download(context.Background(), "a/b", "", "")
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != 400 || apiErr.Msg != "bad request" {
		t.Errorf("got %T %v", err, err)
	}
}

func TestDownloadNetworkError(t *testing.T) {
	c := New("http://127.0.0.1:1")
	_, _, err := c.Download(context.Background(), "a/b", "", "")
	if err == nil {
		t.Fatal("expected network error")
	}
}

// ----- Publish -----

func TestPublish(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: got %q", r.Method)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Errorf("content-type: got %q", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if r.FormValue("slug") != "alice/foo" {
			t.Errorf("slug: got %q", r.FormValue("slug"))
		}
		if r.FormValue("version") != "1.0.0" {
			t.Errorf("version: got %q", r.FormValue("version"))
		}
		if r.FormValue("category") != "writing" {
			t.Errorf("category: got %q", r.FormValue("category"))
		}
		if r.FormValue("kind") != "skill" {
			t.Errorf("kind: got %q", r.FormValue("kind"))
		}
		if r.FormValue("displayName") != "Foo" {
			t.Errorf("displayName: got %q", r.FormValue("displayName"))
		}
		if r.FormValue("summary") != "summary text" {
			t.Errorf("summary: got %q", r.FormValue("summary"))
		}
		if r.FormValue("changelog") != "Initial." {
			t.Errorf("changelog: got %q", r.FormValue("changelog"))
		}
		if r.FormValue("tags") != "a,b" {
			t.Errorf("tags: got %q", r.FormValue("tags"))
		}
		if got := len(r.MultipartForm.File["files"]); got != 1 {
			t.Errorf("file count: got %d, want 1", got)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(PublishResponse{
			Skill:   Skill{Slug: "alice/foo"},
			Version: Version{Version: "1.0.0"},
		})
	}))
	defer srv.Close()
	c := New(srv.URL, WithToken("tok"))
	res, err := c.Publish(context.Background(), PublishRequest{
		Slug:        "alice/foo",
		Version:     "1.0.0",
		Category:    "writing",
		Kind:        "skill",
		DisplayName: "Foo",
		Summary:     "summary text",
		Changelog:   "Initial.",
		Tags:        []string{"a", "b"},
		Files:       map[string][]byte{"SKILL.md": []byte("# foo")},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.Skill.Slug != "alice/foo" || res.Version.Version != "1.0.0" {
		t.Errorf("unexpected: %+v", res)
	}
}

func TestPublishMissingSlug(t *testing.T) {
	c := New("http://127.0.0.1:1")
	_, err := c.Publish(context.Background(), PublishRequest{Version: "1"})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestPublishMissingVersion(t *testing.T) {
	c := New("http://127.0.0.1:1")
	_, err := c.Publish(context.Background(), PublishRequest{Slug: "a/b"})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestPublishNetworkError(t *testing.T) {
	c := New("http://127.0.0.1:1")
	_, err := c.Publish(context.Background(), PublishRequest{
		Slug:    "a/b",
		Version: "1",
		Files:   map[string][]byte{"SKILL.md": []byte("x")},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPublishServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid"}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	_, err := c.Publish(context.Background(), PublishRequest{
		Slug:    "a/b",
		Version: "1",
		Files:   map[string][]byte{"SKILL.md": []byte("x")},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ----- Install / Uninstall -----

func makeZipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestInstallHappyPath(t *testing.T) {
	zipBytes := makeZipBytes(t, map[string]string{
		"SKILL.md":          "# A skill",
		"docs/usage.md":     "Usage doc",
		"helpers/script.sh": "#!/bin/sh\necho hi\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(zipBytes)
	}))
	defer srv.Close()
	c := New(srv.URL)
	root := t.TempDir()
	res, err := c.Install(context.Background(), "alice/foo", InstallOptions{Dir: root})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.FilesCount != 3 {
		t.Errorf("files: got %d, want 3", res.FilesCount)
	}
	if res.NotModified {
		t.Error("should not be NotModified")
	}
	expectedDir := filepath.Join(root, "alice__foo")
	if res.Dir != expectedDir {
		t.Errorf("dir: got %q, want %q", res.Dir, expectedDir)
	}
	for _, p := range []string{"SKILL.md", "docs/usage.md", "helpers/script.sh", ".skillhub-origin"} {
		if _, err := os.Stat(filepath.Join(expectedDir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	originBytes, err := os.ReadFile(filepath.Join(expectedDir, ".skillhub-origin"))
	if err != nil {
		t.Fatalf("read origin: %v", err)
	}
	if !strings.Contains(string(originBytes), "slug=alice/foo") {
		t.Errorf("origin missing slug: %s", originBytes)
	}
}

func TestInstallMissingDir(t *testing.T) {
	c := New("http://127.0.0.1:1")
	_, err := c.Install(context.Background(), "a/b", InstallOptions{})
	if err == nil {
		t.Fatal("expected error for missing Dir")
	}
}

func TestInstallInvalidSlug(t *testing.T) {
	c := New("http://127.0.0.1:1")
	root := t.TempDir()
	_, err := c.Install(context.Background(), "../evil", InstallOptions{Dir: root})
	if err == nil {
		t.Fatal("expected error for traversal slug")
	}
	_, err = c.Install(context.Background(), "", InstallOptions{Dir: root})
	if err == nil {
		t.Fatal("expected error for empty slug")
	}
}

func TestInstallNotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()
	c := New(srv.URL)
	root := t.TempDir()
	res, err := c.Install(context.Background(), "a/b", InstallOptions{Dir: root, ETag: `"x"`})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.NotModified {
		t.Error("expected NotModified")
	}
	// No files written.
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Errorf("no files should be created, got %d entries", len(entries))
	}
}

func TestInstallExceedsMaxBytes(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(big)
	}))
	defer srv.Close()
	c := New(srv.URL)
	root := t.TempDir()
	_, err := c.Install(context.Background(), "a/b", InstallOptions{Dir: root, MaxBytes: 50})
	if err == nil {
		t.Fatal("expected size error")
	}
}

func TestInstallInvalidZip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not a zip"))
	}))
	defer srv.Close()
	c := New(srv.URL)
	root := t.TempDir()
	_, err := c.Install(context.Background(), "a/b", InstallOptions{Dir: root})
	if err == nil {
		t.Fatal("expected zip parse error")
	}
}

func TestInstallTraversalRejected(t *testing.T) {
	zipBytes := makeZipBytes(t, map[string]string{
		"../escape.md": "evil",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(zipBytes)
	}))
	defer srv.Close()
	c := New(srv.URL)
	root := t.TempDir()
	_, err := c.Install(context.Background(), "a/b", InstallOptions{Dir: root})
	if err == nil {
		t.Fatal("expected traversal error")
	}
}

func TestInstallDownloadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := New(srv.URL)
	root := t.TempDir()
	_, err := c.Install(context.Background(), "a/b", InstallOptions{Dir: root})
	if err == nil {
		t.Fatal("expected download error")
	}
}

func TestUninstall(t *testing.T) {
	root := t.TempDir()
	dir, err := safeInstallRoot(root, "alice/foo")
	if err != nil {
		t.Fatalf("safeInstallRoot: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("y"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := Uninstall(root, "alice/foo"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("expected dir removed, err=%v", err)
	}
}

func TestUninstallInvalidSlug(t *testing.T) {
	if err := Uninstall("/tmp/somewhere", "../bad"); err == nil {
		t.Fatal("expected error for traversal slug")
	}
}

func TestSafeInstallRoot(t *testing.T) {
	tests := []struct {
		name    string
		slug    string
		wantErr bool
		want    string
	}{
		{"simple slug", "foo", false, "foo"},
		{"namespaced", "alice/foo", false, "alice__foo"},
		{"empty", "", true, ""},
		{"traversal", "../bad", true, ""},
		{"whitespace only", "   ", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := safeInstallRoot("/root", tt.slug)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := filepath.Join("/root", tt.want)
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// ----- Config: Resolved / SyncIntervalDuration / Load+Save / Dirs -----

func TestConfigResolved(t *testing.T) {
	t.Setenv("SKILLHUB_REGISTRY", "")
	t.Setenv("SKILLHUB_TOKEN", "")
	t.Setenv("SKILLHUB_OFFLINE", "")
	c := Config{}.Resolved()
	if c.Registry != DefaultRegistry {
		t.Errorf("registry: got %q, want %q", c.Registry, DefaultRegistry)
	}
	if c.LearnedVisibility != "private" {
		t.Errorf("visibility: got %q", c.LearnedVisibility)
	}
}

func TestConfigResolvedEnvOverrides(t *testing.T) {
	t.Setenv("SKILLHUB_REGISTRY", "https://custom.example.com/")
	t.Setenv("SKILLHUB_TOKEN", "envtok")
	t.Setenv("SKILLHUB_OFFLINE", "1")
	c := Config{
		Registry:          "https://wont-be-used.example.com",
		Token:             "wont",
		LearnedVisibility: "public",
	}.Resolved()
	if c.Registry != "https://custom.example.com" {
		t.Errorf("registry: got %q", c.Registry)
	}
	if c.Token != "envtok" {
		t.Errorf("token: got %q", c.Token)
	}
	if !c.Offline {
		t.Error("offline should be true")
	}
	if c.LearnedVisibility != "public" {
		t.Errorf("visibility: got %q", c.LearnedVisibility)
	}
}

func TestConfigResolvedOfflineTrue(t *testing.T) {
	t.Setenv("SKILLHUB_OFFLINE", "true")
	t.Setenv("SKILLHUB_REGISTRY", "")
	t.Setenv("SKILLHUB_TOKEN", "")
	c := Config{}.Resolved()
	if !c.Offline {
		t.Error("offline true should activate")
	}
}

func TestConfigSyncInterval(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"", 15 * time.Minute},
		{"invalid", 15 * time.Minute},
		{"0s", 15 * time.Minute},
		{"30s", 30 * time.Second},
		{"5m", 5 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			c := Config{SyncInterval: tt.in}
			if got := c.SyncIntervalDuration(); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfigDirs(t *testing.T) {
	root := "/some/project"
	if got := SubscribedDir(root); got != filepath.Join(root, ".saker", "subscribed-skills") {
		t.Errorf("SubscribedDir: %q", got)
	}
	if got := LearnedDir(root); got != filepath.Join(root, ".saker", "learned-skills") {
		t.Errorf("LearnedDir: %q", got)
	}
}

func TestLoadFromProjectMissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadFromProject(dir)
	if err != nil {
		t.Fatalf("LoadFromProject: %v", err)
	}
	if cfg.Registry != "" {
		t.Errorf("expected zero config, got %+v", cfg)
	}
}

func TestLoadFromProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".saker"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	settings := map[string]any{
		"skillhub": map[string]any{
			"registry":     "https://hub.example.com",
			"handle":       "alice",
			"autoSync":     true,
			"syncInterval": "10m",
		},
	}
	raw, _ := json.Marshal(settings)
	if err := os.WriteFile(filepath.Join(dir, ".saker", "settings.json"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := LoadFromProject(dir)
	if err != nil {
		t.Fatalf("LoadFromProject: %v", err)
	}
	if cfg.Registry != "https://hub.example.com" {
		t.Errorf("registry: got %q", cfg.Registry)
	}
	if cfg.Handle != "alice" {
		t.Errorf("handle: got %q", cfg.Handle)
	}
	if !cfg.AutoSync {
		t.Error("autoSync should be true")
	}
	if cfg.SyncInterval != "10m" {
		t.Errorf("syncInterval: got %q", cfg.SyncInterval)
	}
}

func TestLoadFromProjectLocalOverrides(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".saker"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	base := map[string]any{
		"skillhub": map[string]any{
			"registry": "https://base.example.com",
			"handle":   "bob",
		},
	}
	local := map[string]any{
		"skillhub": map[string]any{
			"token":  "secret-token",
			"handle": "alice",
		},
	}
	for name, m := range map[string]map[string]any{
		"settings.json":       base,
		"settings.local.json": local,
	} {
		raw, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(dir, ".saker", name), raw, 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	cfg, err := LoadFromProject(dir)
	if err != nil {
		t.Fatalf("LoadFromProject: %v", err)
	}
	if cfg.Registry != "https://base.example.com" {
		t.Errorf("registry: got %q", cfg.Registry)
	}
	if cfg.Token != "secret-token" {
		t.Errorf("token: got %q", cfg.Token)
	}
	if cfg.Handle != "alice" {
		t.Errorf("handle (local should win): got %q", cfg.Handle)
	}
}

func TestLoadFromProjectInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".saker"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".saker", "settings.json"), []byte("{bad json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := LoadFromProject(dir)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestSaveToProjectAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Registry: "https://x.example.com",
		Token:    "tok-1",
		Handle:   "alice",
	}
	if err := SaveToProject(dir, cfg); err != nil {
		t.Fatalf("SaveToProject: %v", err)
	}
	loaded, err := LoadFromProject(dir)
	if err != nil {
		t.Fatalf("LoadFromProject: %v", err)
	}
	if loaded.Registry != cfg.Registry {
		t.Errorf("registry: got %q, want %q", loaded.Registry, cfg.Registry)
	}
	if loaded.Token != cfg.Token {
		t.Errorf("token: got %q, want %q", loaded.Token, cfg.Token)
	}
	if loaded.Handle != cfg.Handle {
		t.Errorf("handle: got %q, want %q", loaded.Handle, cfg.Handle)
	}
}

func TestSaveToProjectPreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".saker"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	existing := map[string]any{
		"otherKey": "should-stay",
		"skillhub": map[string]any{"oldKey": "old"},
	}
	raw, _ := json.Marshal(existing)
	if err := os.WriteFile(filepath.Join(dir, ".saker", "settings.local.json"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := SaveToProject(dir, Config{Registry: "https://new"}); err != nil {
		t.Fatalf("SaveToProject: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".saker", "settings.local.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got["otherKey"] != "should-stay" {
		t.Errorf("otherKey lost: %v", got["otherKey"])
	}
}

// ----- learned -----

func TestCollectDirFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# top"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "x.md"), []byte("sub content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Hidden dir should be skipped.
	if err := os.MkdirAll(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden", "secret"), []byte("nope"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".dotfile"), []byte("dot"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	files, err := CollectDirFiles(root, 0)
	if err != nil {
		t.Fatalf("CollectDirFiles: %v", err)
	}
	if _, ok := files["SKILL.md"]; !ok {
		t.Error("SKILL.md missing")
	}
	if _, ok := files["sub/x.md"]; !ok {
		t.Error("sub/x.md missing")
	}
	if _, ok := files[".dotfile"]; ok {
		t.Error(".dotfile should be skipped")
	}
	if _, ok := files[".hidden/secret"]; ok {
		t.Error(".hidden/secret should be skipped")
	}
}

func TestCollectDirFilesNoSkillMD(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "other.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := CollectDirFiles(root, 0)
	if err == nil {
		t.Fatal("expected error for missing SKILL.md")
	}
}

func TestCollectDirFilesExceedsMax(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "big.bin"), bytes.Repeat([]byte("x"), 200), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := CollectDirFiles(root, 50)
	if err == nil {
		t.Fatal("expected size error")
	}
}

func TestCollectDirFilesMissingRoot(t *testing.T) {
	_, err := CollectDirFiles(filepath.Join(t.TempDir(), "does-not-exist"), 0)
	if err == nil {
		t.Fatal("expected walk error for missing root")
	}
}

func TestPublishLearned(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "auto-foo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# foo"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var seenSlug, seenVersion, seenChangelog string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		seenSlug = r.FormValue("slug")
		seenVersion = r.FormValue("version")
		seenChangelog = r.FormValue("changelog")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"skill":{"slug":"alice/learned-auto-foo"},"version":{"version":"0.0.1"}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, WithToken("tok"))
	res, err := c.PublishLearned(context.Background(), skillDir, PublishLearnedOptions{
		Handle: "alice",
	})
	if err != nil {
		t.Fatalf("PublishLearned: %v", err)
	}
	if seenSlug != "alice/learned-auto-foo" {
		t.Errorf("slug: got %q", seenSlug)
	}
	if seenVersion != "0.0.1" {
		t.Errorf("version: got %q", seenVersion)
	}
	if seenChangelog != "auto-published learned skill" {
		t.Errorf("changelog: got %q", seenChangelog)
	}
	if res.Skill.Slug != "alice/learned-auto-foo" {
		t.Errorf("res slug: got %q", res.Skill.Slug)
	}
}

func TestPublishLearnedStripsLearnedPrefix(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "learned-foo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# foo"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var seenSlug string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		seenSlug = r.FormValue("slug")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	if _, err := c.PublishLearned(context.Background(), skillDir, PublishLearnedOptions{
		Handle:    "alice",
		Version:   "1.0.0",
		Changelog: "custom",
	}); err != nil {
		t.Fatalf("PublishLearned: %v", err)
	}
	// "learned-" trimmed → expected "alice/learned-foo".
	if seenSlug != "alice/learned-foo" {
		t.Errorf("slug: got %q, want alice/learned-foo", seenSlug)
	}
}

func TestPublishLearnedMissingHandle(t *testing.T) {
	c := New("http://127.0.0.1:1")
	_, err := c.PublishLearned(context.Background(), t.TempDir(), PublishLearnedOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPublishLearnedInvalidDir(t *testing.T) {
	c := New("http://127.0.0.1:1")
	_, err := c.PublishLearned(context.Background(), "/", PublishLearnedOptions{Handle: "alice"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// Sanity import to keep go vet happy if we add fmt-only helpers later.
var _ = fmt.Sprintf
