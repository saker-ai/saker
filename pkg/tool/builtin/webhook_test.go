package toolbuiltin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestWebhookTool_Name(t *testing.T) {
	wh := NewWebhookTool()
	if wh.Name() != "webhook" {
		t.Fatalf("expected name webhook, got %s", wh.Name())
	}
}

func TestWebhookTool_RequiresURL(t *testing.T) {
	wh := NewWebhookTool()
	_, err := wh.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}

func TestWebhookTool_RejectsNonHTTP(t *testing.T) {
	wh := NewWebhookTool()
	_, err := wh.Execute(context.Background(), map[string]any{"url": "ftp://example.com"})
	if err == nil {
		t.Fatal("expected error for non-http url")
	}
}

func TestWebhookTool_PostSuccess(t *testing.T) {
	var gotMethod string
	var gotBody map[string]any
	var gotHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotHeader = r.Header.Get("X-Custom")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	parsed, _ := url.Parse(srv.URL)
	wh := &WebhookTool{Client: srv.Client(), AllowedHosts: map[string]bool{parsed.Host: true}}
	result, err := wh.Execute(context.Background(), map[string]any{
		"url":     srv.URL,
		"method":  "POST",
		"headers": map[string]any{"X-Custom": "test-value"},
		"body":    map[string]any{"message": "hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}
	if gotMethod != "POST" {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotHeader != "test-value" {
		t.Errorf("expected header test-value, got %s", gotHeader)
	}
	if gotBody["message"] != "hello" {
		t.Errorf("expected body message=hello, got %v", gotBody)
	}
}

func TestWebhookTool_DefaultMethodPOST(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(200)
	}))
	defer srv.Close()

	parsed, _ := url.Parse(srv.URL)
	wh := &WebhookTool{Client: srv.Client(), AllowedHosts: map[string]bool{parsed.Host: true}}
	_, err := wh.Execute(context.Background(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("expected default POST, got %s", gotMethod)
	}
}

func TestWebhookTool_Non2xxNotSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	parsed, _ := url.Parse(srv.URL)
	wh := &WebhookTool{Client: srv.Client(), AllowedHosts: map[string]bool{parsed.Host: true}}
	result, err := wh.Execute(context.Background(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected success=false for 500 response")
	}
}

func TestIsBlockedHost_DNSFailureBlocks(t *testing.T) {
	// Fail-closed: DNS lookup failure must BLOCK (not allow). Otherwise an
	// attacker with control of an authoritative resolver could return
	// NXDOMAIN to bypass SSRF protection during the validation pass and a
	// private IP at dial time.
	if !isBlockedHost("this-host-does-not-exist-12345.example.invalid") {
		t.Error("expected DNS failure to block (fail-closed), not allow through")
	}
}

func TestIsBlockedHost_DirectIPBlocked(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "172.16.0.1", "169.254.169.254"}
	for _, ip := range blocked {
		if !isBlockedHost(ip) {
			t.Errorf("expected %s to be blocked", ip)
		}
	}
}

func TestWebhookTool_SSRFBlocked(t *testing.T) {
	wh := NewWebhookTool()
	blocked := []string{
		"http://127.0.0.1/hook",
		"http://10.0.0.1/hook",
		"http://172.16.0.1/hook",
		"http://192.168.1.1/hook",
		"http://169.254.169.254/latest/meta-data/",
	}
	for _, u := range blocked {
		_, err := wh.Execute(context.Background(), map[string]any{"url": u})
		if err == nil {
			t.Errorf("expected SSRF block for %s, got nil error", u)
			continue
		}
		// CheckSSRF errors are prefixed with "ssrf:" — accept any of the
		// concrete forms ("not routable", "non-routable ip", "host ... is blocked").
		msg := err.Error()
		if !strings.Contains(msg, "ssrf:") {
			t.Errorf("expected SSRF-prefixed error for %s, got %q", u, msg)
		}
	}
}
