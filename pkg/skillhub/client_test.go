package skillhub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewWithOptions(t *testing.T) {
	c := New("https://skillhub.example.com",
		WithToken("tok-123"),
		WithUserAgent("custom-agent/1.0"),
	)

	if c.baseURL != "https://skillhub.example.com" {
		t.Fatalf("baseURL: got %q, want %q", c.baseURL, "https://skillhub.example.com")
	}
	if c.Token() != "tok-123" {
		t.Fatalf("token: got %q, want %q", c.Token(), "tok-123")
	}
	if c.userAgent != "custom-agent/1.0" {
		t.Fatalf("userAgent: got %q, want %q", c.userAgent, "custom-agent/1.0")
	}

	// Default user-agent when none provided.
	c2 := New("https://skillhub.example.com")
	if c2.userAgent != "saker-skillhub/0.1" {
		t.Fatalf("default userAgent: got %q, want %q", c2.userAgent, "saker-skillhub/0.1")
	}

	// WithHTTPClient overrides the default.
	custom := &http.Client{Timeout: 5 * time.Second}
	c3 := New("https://skillhub.example.com", WithHTTPClient(custom))
	if c3.httpClient != custom {
		t.Fatalf("httpClient: got %v, want %v", c3.httpClient, custom)
	}

	// Trailing slash stripped.
	c4 := New("https://skillhub.example.com/")
	if c4.baseURL != "https://skillhub.example.com" {
		t.Fatalf("trailing slash: got %q, want %q", c4.baseURL, "https://skillhub.example.com")
	}
}

func TestNewDefaultTransportPoolSettings(t *testing.T) {
	c := New("https://skillhub.example.com")
	tr, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.httpClient.Transport)
	}
	if tr.MaxIdleConns != 100 {
		t.Errorf("MaxIdleConns: got %d, want 100", tr.MaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != 10 {
		t.Errorf("MaxIdleConnsPerHost: got %d, want 10", tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout: got %v, want 90s", tr.IdleConnTimeout)
	}
}

func TestAPIErrorFormatting(t *testing.T) {
	tests := []struct {
		name string
		err  *APIError
		want string
	}{
		{
			name: "with message",
			err:  &APIError{Status: 403, Msg: "forbidden"},
			want: "skillhub: 403 forbidden",
		},
		{
			name: "without message truncates body",
			err:  &APIError{Status: 500, Body: strings.Repeat("x", 300)},
			want: "skillhub: HTTP 500: " + strings.Repeat("x", 200) + "...",
		},
		{
			name: "without message short body",
			err:  &APIError{Status: 404, Body: "not found body"},
			want: "skillhub: HTTP 404: not found body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.want {
				t.Errorf("Error(): got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	if truncate("short", 10) != "short" {
		t.Error("short string should not be truncated")
	}
	long := strings.Repeat("a", 250)
	got := truncate(long, 200)
	if len(got) != 203 { // 200 + 3 for "..."
		t.Errorf("truncated length: got %d, want 203", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncated string should end with ..., got %q", got)
	}
	// Exact boundary: string length == limit
	exact := strings.Repeat("b", 200)
	if truncate(exact, 200) != exact {
		t.Error("string at exact limit should not be truncated")
	}
}

func TestDoJSONSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing auth header")
		}
		if r.Header.Get("User-Agent") != "saker-skillhub/0.1" {
			t.Errorf("missing user-agent header")
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"handle": "alice"})
	}))
	defer srv.Close()

	c := New(srv.URL, WithToken("test-token"))
	var result struct {
		Handle string `json:"handle"`
	}
	err := c.doJSON(context.Background(), http.MethodGet, "/api/v1/test", nil, &result)
	if err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	if result.Handle != "alice" {
		t.Errorf("handle: got %q, want %q", result.Handle, "alice")
	}
}

func TestDoJSONNetworkError(t *testing.T) {
	// Use a URL that will refuse connections.
	c := New("http://127.0.0.1:1")
	err := c.doJSON(context.Background(), http.MethodGet, "/fail", nil, nil)
	if err == nil {
		t.Fatal("expected error for network failure")
	}
	if !strings.Contains(err.Error(), "skillhub request") {
		t.Errorf("error should wrap network error, got: %v", err)
	}
}

func TestDoJSON4xxResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "skill not found"})
	}))
	defer srv.Close()

	c := New(srv.URL)
	err := c.doJSON(context.Background(), http.MethodGet, "/api/v1/skills/missing", nil, nil)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Status != 404 {
		t.Errorf("status: got %d, want 404", apiErr.Status)
	}
	if apiErr.Msg != "skill not found" {
		t.Errorf("msg: got %q, want %q", apiErr.Msg, "skill not found")
	}
}

func TestDoJSONDecodeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("this is not json"))
	}))
	defer srv.Close()

	c := New(srv.URL)
	var out struct{}
	err := c.doJSON(context.Background(), http.MethodGet, "/bad-json", nil, &out)
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Errorf("error should mention decode, got: %v", err)
	}
}

func TestDecodeResponseReadError(t *testing.T) {
	// Response body that returns an error on read.
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.ReadCloser(errorReader{}),
	}
	err := decodeResponse(resp, nil)
	if err == nil {
		t.Fatal("expected read error")
	}
	if !strings.Contains(err.Error(), "read response") {
		t.Errorf("error should mention read, got: %v", err)
	}
}

type errorReader struct{}

func (errorReader) Read(_ []byte) (int, error) { return 0, errors.New("read failure") }
func (errorReader) Close() error               { return nil }

func TestNewRequestSetsHeaders(t *testing.T) {
	c := New("https://skillhub.example.com", WithToken("mytoken"))
	req, err := c.newRequest(context.Background(), http.MethodGet, "/path", nil)
	if err != nil {
		t.Fatalf("newRequest: %v", err)
	}
	if req.Header.Get("Authorization") != "Bearer mytoken" {
		t.Errorf("auth header: got %q", req.Header.Get("Authorization"))
	}
	if req.Header.Get("User-Agent") != "saker-skillhub/0.1" {
		t.Errorf("user-agent: got %q", req.Header.Get("User-Agent"))
	}
}

func TestNewRequestNoTokenNoAuth(t *testing.T) {
	c := New("https://skillhub.example.com")
	req, err := c.newRequest(context.Background(), http.MethodGet, "/path", nil)
	if err != nil {
		t.Fatalf("newRequest: %v", err)
	}
	if req.Header.Get("Authorization") != "" {
		t.Errorf("no token should mean no auth header, got %q", req.Header.Get("Authorization"))
	}
}

func TestSetToken(t *testing.T) {
	c := New("https://skillhub.example.com")
	if c.Token() != "" {
		t.Fatalf("initial token should be empty")
	}
	c.SetToken("new-tok")
	if c.Token() != "new-tok" {
		t.Fatalf("SetToken: got %q, want %q", c.Token(), "new-tok")
	}
}

func TestBaseURL(t *testing.T) {
	c := New("https://skillhub.example.com")
	if c.BaseURL() != "https://skillhub.example.com" {
		t.Errorf("BaseURL: got %q, want %q", c.BaseURL(), "https://skillhub.example.com")
	}
}
