package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/config"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// callGinHandler builds a *gin.Context wrapping the supplied recorder and
// request and runs the handler. Used by auth tests that previously invoked
// the (w, r) form directly before the handlers were converted to gin style.
func callGinHandler(rec *httptest.ResponseRecorder, req *http.Request, h gin.HandlerFunc) {
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	h(c)
}

func hashPassword(t *testing.T, plain string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

func newTestAuth(t *testing.T) (*AuthManager, string) {
	t.Helper()
	password := "testpass"
	cfg := &config.WebAuthConfig{
		Username: "admin",
		Password: hashPassword(t, password),
	}
	am := NewAuthManager(cfg, nil)
	return am, password
}

func TestMiddleware_LocalhostBypass(t *testing.T) {
	am, _ := newTestAuth(t)
	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for localhost, got %d", rec.Code)
	}
}

func TestMiddleware_IPv6LocalhostBypass(t *testing.T) {
	am, _ := newTestAuth(t)
	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[::1]:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for IPv6 localhost, got %d", rec.Code)
	}
}

func TestMiddleware_RemoteBlocked(t *testing.T) {
	am, _ := newTestAuth(t)
	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/protected", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for remote without auth, got %d", rec.Code)
	}
}

func TestMiddleware_HealthAlwaysAllowed(t *testing.T) {
	am, _ := newTestAuth(t)
	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /health, got %d", rec.Code)
	}
}

func TestMiddleware_NilConfig(t *testing.T) {
	am := NewAuthManager(nil, nil)
	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with nil config, got %d", rec.Code)
	}
}

func TestLoginAndSession(t *testing.T) {
	am, password := newTestAuth(t)

	// Login with correct credentials.
	body := `{"username":"admin","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	callGinHandler(rec, req, am.HandleLogin)

	if rec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d", rec.Code)
	}

	// Extract session cookie.
	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie after login")
	}

	// Use session cookie for remote request.
	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "192.168.1.100:12345"
	req2.AddCookie(sessionCookie)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid session, got %d", rec2.Code)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	am, _ := newTestAuth(t)

	body := `{"username":"admin","password":"wrongpass"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	callGinHandler(rec, req, am.HandleLogin)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong password, got %d", rec.Code)
	}
}

func TestLoginWrongUsername(t *testing.T) {
	am, password := newTestAuth(t)

	body := `{"username":"root","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	callGinHandler(rec, req, am.HandleLogin)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong username, got %d", rec.Code)
	}
}

func TestLogout(t *testing.T) {
	am, password := newTestAuth(t)

	// Login first.
	body := `{"username":"admin","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	callGinHandler(rec, req, am.HandleLogin)

	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie")
	}

	// Logout.
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	logoutRec := httptest.NewRecorder()
	callGinHandler(logoutRec, logoutReq, am.HandleLogout)

	// Session should be invalid now.
	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req2 := httptest.NewRequest(http.MethodGet, "/api/protected", nil)
	req2.RemoteAddr = "192.168.1.100:12345"
	req2.AddCookie(sessionCookie)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", rec2.Code)
	}
}

func TestAuthStatus(t *testing.T) {
	am, _ := newTestAuth(t)

	// Remote unauthenticated.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	rec := httptest.NewRecorder()
	callGinHandler(rec, req, am.HandleStatus)

	var resp map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp["required"] {
		t.Fatal("expected required=true")
	}
	if resp["authenticated"] {
		t.Fatal("expected authenticated=false")
	}

	// Localhost always authenticated.
	req2 := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	req2.RemoteAddr = "127.0.0.1:12345"
	rec2 := httptest.NewRecorder()
	callGinHandler(rec2, req2, am.HandleStatus)

	var resp2 map[string]bool
	if err := json.NewDecoder(rec2.Body).Decode(&resp2); err != nil {
		t.Fatal(err)
	}
	if !resp2["authenticated"] {
		t.Fatal("expected authenticated=true for localhost")
	}
}

func TestGeneratePassword(t *testing.T) {
	plain, hash, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 32 {
		t.Fatalf("expected 32-char password, got %d", len(plain))
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)); err != nil {
		t.Fatal("hash does not match plain password")
	}
}
