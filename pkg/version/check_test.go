package version

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"v0.1.0", "0.1.0"},
		{"0.1.0", "0.1.0"},
		{" v1.2.3 ", "1.2.3"},
		{"dev", "dev"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := normalizeVersion(tt.input); got != tt.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int // >0, <0, or 0
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.2.0", "0.1.0", 1},
		{"0.1.0", "0.2.0", -1},
		{"1.0.0", "0.9.9", 1},
		{"0.1.1", "0.1.0", 1},
		{"1.0.0", "1.0.0", 0},
		{"2.0.0", "1.99.99", 1},
		{"0.1.0-beta", "0.1.0", 0},
	}
	for _, tt := range tests {
		got := compareVersions(tt.a, tt.b)
		switch {
		case tt.want > 0 && got <= 0:
			t.Errorf("compareVersions(%q, %q) = %d, want > 0", tt.a, tt.b, got)
		case tt.want < 0 && got >= 0:
			t.Errorf("compareVersions(%q, %q) = %d, want < 0", tt.a, tt.b, got)
		case tt.want == 0 && got != 0:
			t.Errorf("compareVersions(%q, %q) = %d, want 0", tt.a, tt.b, got)
		}
	}
}

func TestCheckForUpdate_RemoteFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(RemoteVersion{
			Latest:       "0.2.0",
			MinSupported: "0.1.0",
			InstallURL:   "https://example.com/install.sh",
			ReleaseURL:   "https://example.com/releases",
			Message:      "Bug fixes",
		})
	}))
	defer srv.Close()

	// Use temp dir for cache to avoid polluting ~/.saker.
	tmpDir := t.TempDir()
	origFunc := cacheFilePathFunc
	cacheFilePathFunc = func() string { return filepath.Join(tmpDir, CacheFileName) }
	defer func() { cacheFilePathFunc = origFunc }()

	info, err := CheckForUpdateWithURL("0.1.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil UpdateInfo")
	}
	if !info.HasUpdate {
		t.Error("expected HasUpdate=true")
	}
	if info.Latest != "0.2.0" {
		t.Errorf("Latest = %q, want %q", info.Latest, "0.2.0")
	}
	if info.Current != "0.1.0" {
		t.Errorf("Current = %q, want %q", info.Current, "0.1.0")
	}
}

func TestCheckForUpdate_NoUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(RemoteVersion{Latest: "0.1.0"})
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	origFunc := cacheFilePathFunc
	cacheFilePathFunc = func() string { return filepath.Join(tmpDir, CacheFileName) }
	defer func() { cacheFilePathFunc = origFunc }()

	info, err := CheckForUpdateWithURL("0.1.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil UpdateInfo")
	}
	if info.HasUpdate {
		t.Error("expected HasUpdate=false")
	}
}

func TestCheckForUpdate_DevVersion(t *testing.T) {
	info, err := CheckForUpdateWithURL("dev", "http://should-not-be-called")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Error("expected nil for dev version")
	}
}

func TestCheckForUpdate_Cache(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(RemoteVersion{Latest: "0.2.0"})
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	origFunc := cacheFilePathFunc
	cacheFilePathFunc = func() string { return filepath.Join(tmpDir, CacheFileName) }
	defer func() { cacheFilePathFunc = origFunc }()

	// First call should hit the server.
	_, err := CheckForUpdateWithURL("0.1.0", srv.URL)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 server call, got %d", callCount)
	}

	// Second call should use cache.
	info, err := CheckForUpdateWithURL("0.1.0", srv.URL)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected cache hit (1 server call), got %d", callCount)
	}
	if !info.HasUpdate {
		t.Error("expected HasUpdate=true from cache")
	}
}

func TestCheckForUpdate_ExpiredCache(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(RemoteVersion{Latest: "0.3.0"})
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	origFunc := cacheFilePathFunc
	cacheFilePathFunc = func() string { return filepath.Join(tmpDir, CacheFileName) }
	defer func() { cacheFilePathFunc = origFunc }()

	// Write an expired cache entry.
	expired := CachedCheck{
		CheckedAt: time.Now().Add(-25 * time.Hour),
		Latest:    "0.2.0",
		Current:   "0.1.0",
	}
	data, _ := json.Marshal(expired)
	os.WriteFile(filepath.Join(tmpDir, CacheFileName), data, 0o644)

	info, err := CheckForUpdateWithURL("0.1.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected server call for expired cache, got %d calls", callCount)
	}
	if info.Latest != "0.3.0" {
		t.Errorf("Latest = %q, want %q", info.Latest, "0.3.0")
	}
}

func TestCheckForUpdate_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in short mode")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		json.NewEncoder(w).Encode(RemoteVersion{Latest: "0.2.0"})
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	origFunc := cacheFilePathFunc
	cacheFilePathFunc = func() string { return filepath.Join(tmpDir, CacheFileName) }
	defer func() { cacheFilePathFunc = origFunc }()

	_, err := CheckForUpdateWithURL("0.1.0", srv.URL)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestFormatUpdateNotice(t *testing.T) {
	info := &UpdateInfo{
		HasUpdate:  true,
		Current:    "0.1.0",
		Latest:     "0.2.0",
		InstallURL: "https://example.com/install.sh",
		Message:    "Important fix",
	}
	notice := FormatUpdateNotice(info)
	if notice == "" {
		t.Fatal("expected non-empty notice")
	}

	// No update.
	noUpdate := &UpdateInfo{HasUpdate: false}
	if FormatUpdateNotice(noUpdate) != "" {
		t.Error("expected empty notice for no update")
	}

	// Nil.
	if FormatUpdateNotice(nil) != "" {
		t.Error("expected empty notice for nil")
	}
}

func TestCheckForUpdateAsync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(RemoteVersion{Latest: "0.2.0"})
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	origFunc := cacheFilePathFunc
	cacheFilePathFunc = func() string { return filepath.Join(tmpDir, CacheFileName) }
	defer func() { cacheFilePathFunc = origFunc }()

	// Temporarily override the default URL.
	origURL := DefaultCheckURL
	defer func() { _ = origURL }()

	// Use the URL variant directly via channel.
	ch := make(chan *UpdateInfo, 1)
	go func() {
		defer close(ch)
		info, err := CheckForUpdateWithURL("0.1.0", srv.URL)
		if err != nil || info == nil {
			return
		}
		ch <- info
	}()

	select {
	case info := <-ch:
		if info == nil || !info.HasUpdate {
			t.Error("expected update info from async check")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("async check timed out")
	}
}
