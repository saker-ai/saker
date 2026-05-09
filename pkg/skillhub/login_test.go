package skillhub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestDeviceCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: got %q, want POST", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(DeviceCode{
			DeviceCode:      "DC-123",
			UserCode:        "ABCD-EFGH",
			VerificationURL: "/auth/device/verify",
			ExpiresIn:       900,
			Interval:        5,
		})
	}))
	defer srv.Close()

	c := New(srv.URL, WithToken("tok"))
	dc, err := c.RequestDeviceCode(context.Background())
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	if dc.DeviceCode != "DC-123" {
		t.Errorf("deviceCode: got %q, want %q", dc.DeviceCode, "DC-123")
	}
	if dc.UserCode != "ABCD-EFGH" {
		t.Errorf("userCode: got %q, want %q", dc.UserCode, "ABCD-EFGH")
	}
	// Relative verificationURL should be joined with baseURL.
	wantURL := srv.URL + "/auth/device/verify"
	if dc.VerificationURL != wantURL {
		t.Errorf("verificationURL: got %q, want %q", dc.VerificationURL, wantURL)
	}
}

func TestRequestDeviceCodeEmptyURLFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(DeviceCode{
			DeviceCode: "DC-456",
			UserCode:   "WXYZ-1234",
			// VerificationURL intentionally empty.
		})
	}))
	defer srv.Close()

	c := New(srv.URL)
	dc, err := c.RequestDeviceCode(context.Background())
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	wantURL := srv.URL + "/auth/device/verify"
	if dc.VerificationURL != wantURL {
		t.Errorf("fallback verificationURL: got %q, want %q", dc.VerificationURL, wantURL)
	}
}

func TestRequestDeviceCodeAbsoluteURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(DeviceCode{
			DeviceCode:      "DC-789",
			UserCode:        "AAAA-BBBB",
			VerificationURL: "https://other.example.com/verify",
		})
	}))
	defer srv.Close()

	c := New(srv.URL)
	dc, err := c.RequestDeviceCode(context.Background())
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	if dc.VerificationURL != "https://other.example.com/verify" {
		t.Errorf("absolute URL should not be rewritten, got %q", dc.VerificationURL)
	}
}

func TestPollDeviceTokenSuccess(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			// First poll: pending.
			w.WriteHeader(http.StatusAccepted)
			return
		}
		// Second poll: authorized.
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"token": "bearer-token-xyz"})
	}))
	defer srv.Close()

	c := New(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	token, err := c.PollDeviceToken(ctx, "DC-123", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("PollDeviceToken: %v", err)
	}
	if token != "bearer-token-xyz" {
		t.Errorf("token: got %q, want %q", token, "bearer-token-xyz")
	}
}

func TestPollDeviceTokenContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always pending.
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := c.PollDeviceToken(ctx, "DC-123", 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if ctx.Err() != context.Canceled {
		t.Errorf("error should be context.Canceled, got: %v", err)
	}
}

func TestPollDeviceTokenServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "internal error"})
	}))
	defer srv.Close()

	c := New(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := c.PollDeviceToken(ctx, "DC-123", 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Status != 500 {
		t.Errorf("status: got %d, want 500", apiErr.Status)
	}
}

func TestLoginWithToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer my-login-token" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(User{
			ID:     "u-1",
			Handle: "testuser",
			Role:   "admin",
		})
	}))
	defer srv.Close()

	// LoginWithToken: create a client with a token and verify it works via WhoAmI.
	c := New(srv.URL, WithToken("my-login-token"))
	user, err := c.WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("WhoAmI with token: %v", err)
	}
	if user.Handle != "testuser" {
		t.Errorf("handle: got %q, want %q", user.Handle, "testuser")
	}
	if user.Role != "admin" {
		t.Errorf("role: got %q, want %q", user.Role, "admin")
	}

	// Without token, should get 401.
	c2 := New(srv.URL)
	_, err = c2.WhoAmI(context.Background())
	if err == nil {
		t.Fatal("expected error without token")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Status != 401 {
		t.Errorf("status: got %d, want 401", apiErr.Status)
	}
}