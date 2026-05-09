package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// echoHandler writes back a fixed JS payload so we can verify gzip ratio.
func echoHandler(payload []byte, contentType string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(payload)
	})
}

func TestGzipStaticHandler_compressesJS(t *testing.T) {
	t.Parallel()

	payload := []byte(strings.Repeat("function foo() { return 'hello world'; }\n", 200))
	srv := httptest.NewServer(gzipStaticHandler(echoHandler(payload, "application/javascript")))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer res.Body.Close()

	if got := res.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("expected Content-Encoding=gzip, got %q", got)
	}
	if got := res.Header.Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Fatalf("expected Vary to include Accept-Encoding, got %q", got)
	}

	gz, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	got, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read decompressed body: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestGzipStaticHandler_skipsBinaryAssets(t *testing.T) {
	t.Parallel()

	payload := []byte("\x89PNG\x0d\x0a\x1a\x0a")
	srv := httptest.NewServer(gzipStaticHandler(echoHandler(payload, "image/png")))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/img.png", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer res.Body.Close()

	if got := res.Header.Get("Content-Encoding"); got == "gzip" {
		t.Fatalf("png should not be gzipped")
	}
}

func TestGzipStaticHandler_respectsClientPreference(t *testing.T) {
	t.Parallel()

	payload := []byte(strings.Repeat("body { color: red; }\n", 100))
	srv := httptest.NewServer(gzipStaticHandler(echoHandler(payload, "text/css")))
	defer srv.Close()

	// Client without Accept-Encoding gets raw payload.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/style.css", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer res.Body.Close()

	if got := res.Header.Get("Content-Encoding"); got == "gzip" {
		t.Fatalf("client without Accept-Encoding should get plain body")
	}
	got, _ := io.ReadAll(res.Body)
	if string(got) != string(payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestAcceptsGzip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"gzip", true},
		{"gzip, deflate, br", true},
		{"deflate, gzip", true},
		{"br;q=1.0, gzip;q=0.5", true},
		{"deflate", false},
		{"identity", false},
	}
	for _, c := range cases {
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", c.header)
		if got := acceptsGzip(req); got != c.want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}

func TestShouldCompress(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"/app.js":         true,
		"/style.css":      true,
		"/page.html":      true,
		"/data.json":      true,
		"/icon.svg":       true,
		"/font.woff2":     false,
		"/photo.png":      false,
		"/video.mp4":      false,
		"/sound.mp3":      false,
		"/runtime.wasm":   true,
		"/PATH/Bundle.JS": true,
		"/no-extension":   false,
	}
	for p, want := range cases {
		if got := shouldCompress(p); got != want {
			t.Errorf("shouldCompress(%q) = %v, want %v", p, got, want)
		}
	}
}
