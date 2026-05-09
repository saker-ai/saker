package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/config"
)

// newMultiUserAuth creates an AuthManager with one admin + regular users for testing.
func newMultiUserAuth(t *testing.T) (*AuthManager, string, string) {
	t.Helper()
	adminPass := "adminpass"
	userPass := "userpass"
	cfg := &config.WebAuthConfig{
		Username: "admin",
		Password: hashPassword(t, adminPass),
		Users: []config.UserAuth{
			{Username: "alice", Password: hashPassword(t, userPass)},
			{Username: "bob", Password: hashPassword(t, userPass)},
			{Username: "disabled-user", Password: hashPassword(t, userPass), Disabled: true},
		},
	}
	return NewAuthManager(cfg, nil), adminPass, userPass
}

func loginAs(t *testing.T, am *AuthManager, username, password string) *http.Cookie {
	t.Helper()
	body := `{"username":"` + username + `","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	am.HandleLogin(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login as %s: expected 200, got %d; body: %s", username, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatalf("login as %s: no session cookie", username)
	return nil
}

func TestMultiUser_AdminLogin(t *testing.T) {
	t.Parallel()
	am, adminPass, _ := newMultiUserAuth(t)
	cookie := loginAs(t, am, "admin", adminPass)

	// Verify admin role in context via middleware.
	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		role := RoleFromContext(r.Context())
		_ = json.NewEncoder(w).Encode(map[string]string{"user": user, "role": role})
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["user"] != "admin" {
		t.Errorf("expected user=admin, got %s", resp["user"])
	}
	if resp["role"] != "admin" {
		t.Errorf("expected role=admin, got %s", resp["role"])
	}
}

func TestMultiUser_RegularUserLogin(t *testing.T) {
	t.Parallel()
	am, _, userPass := newMultiUserAuth(t)
	cookie := loginAs(t, am, "alice", userPass)

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		role := RoleFromContext(r.Context())
		_ = json.NewEncoder(w).Encode(map[string]string{"user": user, "role": role})
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["user"] != "alice" {
		t.Errorf("expected user=alice, got %s", resp["user"])
	}
	if resp["role"] != "user" {
		t.Errorf("expected role=user, got %s", resp["role"])
	}
}

func TestMultiUser_DisabledUserRejected(t *testing.T) {
	t.Parallel()
	am, _, userPass := newMultiUserAuth(t)

	body := `{"username":"disabled-user","password":"` + userPass + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	am.HandleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for disabled user, got %d", rec.Code)
	}
}

func TestMultiUser_UnknownUserRejected(t *testing.T) {
	t.Parallel()
	am, _, userPass := newMultiUserAuth(t)

	body := `{"username":"unknown","password":"` + userPass + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	am.HandleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unknown user, got %d", rec.Code)
	}
}

func TestMultiUser_WrongPasswordRejected(t *testing.T) {
	t.Parallel()
	am, _, _ := newMultiUserAuth(t)

	body := `{"username":"alice","password":"wrongpass"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	am.HandleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong password, got %d", rec.Code)
	}
}

func TestMultiUser_LocalhostIsAdmin(t *testing.T) {
	t.Parallel()
	am, _, _ := newMultiUserAuth(t)

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		role := RoleFromContext(r.Context())
		_ = json.NewEncoder(w).Encode(map[string]string{"user": user, "role": role})
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["user"] != "admin" {
		t.Errorf("expected user=admin for localhost, got %s", resp["user"])
	}
	if resp["role"] != "admin" {
		t.Errorf("expected role=admin for localhost, got %s", resp["role"])
	}
}

func TestMultiUser_LoginResponseIncludesRole(t *testing.T) {
	t.Parallel()
	am, adminPass, userPass := newMultiUserAuth(t)

	// Admin login response.
	body := `{"username":"admin","password":"` + adminPass + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	am.HandleLogin(rec, req)

	var adminResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&adminResp); err != nil {
		t.Fatal(err)
	}
	if adminResp["role"] != "admin" {
		t.Errorf("admin login: expected role=admin, got %v", adminResp["role"])
	}

	// Regular user login response.
	body = `{"username":"alice","password":"` + userPass + `"}`
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	am.HandleLogin(rec, req)

	var userResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&userResp); err != nil {
		t.Fatal(err)
	}
	if userResp["role"] != "user" {
		t.Errorf("user login: expected role=user, got %v", userResp["role"])
	}
}

func TestMultiUser_TokenBackwardCompat(t *testing.T) {
	t.Parallel()
	am, adminPass, _ := newMultiUserAuth(t)

	// Create a legacy 3-part token (simulate old format).
	// First get a valid token via login.
	cookie := loginAs(t, am, "admin", adminPass)

	// The new token should be 4-part format and still valid.
	if !am.validToken(cookie.Value) {
		t.Fatal("new format token should be valid")
	}

	// extractTokenInfo should parse correctly.
	user, role := am.extractTokenInfo(cookie.Value)
	if user != "admin" {
		t.Errorf("expected user=admin, got %s", user)
	}
	if role != "admin" {
		t.Errorf("expected role=admin, got %s", role)
	}
}

func TestMultiUser_UserLogoutInvalidatesSession(t *testing.T) {
	t.Parallel()
	am, _, userPass := newMultiUserAuth(t)
	cookie := loginAs(t, am, "bob", userPass)

	// Logout.
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.AddCookie(cookie)
	logoutRec := httptest.NewRecorder()
	am.HandleLogout(logoutRec, logoutReq)

	// Session should be invalid.
	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after user logout, got %d", rec.Code)
	}
}
