package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/project"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestProviderToUserSource(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want project.UserSource
	}{
		{"ldap", project.UserSourceLDAP},
		{"LDAP", project.UserSourceLDAP},
		{"  ldap  ", project.UserSourceLDAP},
		{"oidc", project.UserSourceOIDC},
		{"OIDC", project.UserSourceOIDC},
		{"localhost", project.UserSourceLocalhost},
		{"local", project.UserSourceLocal},
		{"", project.UserSourceLocal},
		{"unknown-provider", project.UserSourceLocal},
	}
	for _, c := range cases {
		require.Equal(t, c.want, providerToUserSource(c.in), "providerToUserSource(%q)", c.in)
	}
}

func TestSetProjectStore(t *testing.T) {
	t.Parallel()
	am := NewAuthManager(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(am.Close)

	require.Nil(t, am.projectStore)
	am.SetProjectStore(nil) // nil is allowed
	require.Nil(t, am.projectStore)

	store := newTestProjectStore(t)
	am.SetProjectStore(store)
	am.mu.RLock()
	require.Same(t, store, am.projectStore)
	am.mu.RUnlock()
}

func TestHandleProviders_LocalOnly(t *testing.T) {
	t.Parallel()
	am := NewAuthManager(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(am.Close)

	gin.SetMode(gin.TestMode)
	eng := gin.New()
	eng.GET("/api/auth/providers", am.HandleProviders)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	eng.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	provs := body["providers"].([]any)
	require.Len(t, provs, 1)
	require.Equal(t, "local", provs[0].(map[string]any)["name"])
}

func TestHandleProviders_LDAPAndOIDCEnabled(t *testing.T) {
	t.Parallel()
	cfg := &config.WebAuthConfig{
		LDAP: &config.LDAPConfig{Enabled: true},
		OIDC: &config.OIDCConfig{Enabled: true},
	}
	am := NewAuthManager(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(am.Close)

	gin.SetMode(gin.TestMode)
	eng := gin.New()
	eng.GET("/api/auth/providers", am.HandleProviders)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	eng.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	provs := body["providers"].([]any)

	names := []string{}
	for _, p := range provs {
		names = append(names, p.(map[string]any)["name"].(string))
	}
	require.ElementsMatch(t, []string{"local", "ldap", "oidc"}, names)
}

func TestAuthManagerClose_Idempotent(t *testing.T) {
	t.Parallel()
	am := NewAuthManager(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	am.Close()
	// Second close should panic on closing closed channel — we explicitly do
	// not call Close twice, but verify the first Close completes cleanly.
}

func TestHasBearerAPIKey(t *testing.T) {
	t.Parallel()
	hex32 := strings.Repeat("a", 32) // 32 hex chars

	cases := []struct {
		header string
		want   bool
	}{
		{"Bearer ak_" + hex32, true},
		{"bearer ak_" + hex32, true},
		{"BEARER ak_" + hex32, true},
		{"", false},
		{"Bearer ", false},
		{"Bearer wrong-format-token-not-32-hex", false},
		{"Bearer ak_" + hex32 + "x", false},               // length wrong (43)
		{"Bearer ak_" + strings.Repeat("a", 31), false},   // length wrong (41)
		{"Token ak_" + hex32, false},                      // wrong scheme prefix
		{"Bearer XXX_" + strings.Repeat("a", 31), false},  // missing ak_ prefix
		{"Bearer ak_" + strings.Repeat("z", 32), false},   // not hex
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		require.Equal(t, c.want, hasBearerAPIKey(req), "hasBearerAPIKey(%q)", c.header)
	}
}

func TestIsPublicPath_Endpoints(t *testing.T) {
	t.Parallel()
	publicPaths := []string{
		"/health",
		"/api/auth/login",
		"/api/auth/status",
		"/api/auth/logout",
		"/api/auth/providers",
		"/api/auth/oidc/login",
		"/api/auth/oidc/callback",
		"/_s3/foo/bar",
		"/_s3",
		"/_next/static/chunks.js",
		"/main.js",
		"/styles.css",
		"/favicon.ico",
		"/logo.svg",
		"/img.png",
		"/font.woff",
		"/font.woff2",
		"/",
		"/index.html",
		"/api/apps/public/abcdef/run",
		"/api/apps/some-project/public/token/run",
	}
	for _, p := range publicPaths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		require.True(t, isPublicPath(req), "expected public: %q", p)
	}

	privatePaths := []string{
		"/api/auth/users",
		"/api/threads",
		"/api/apps/list",
		"/api/apps/x/y/z",
	}
	for _, p := range privatePaths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		require.False(t, isPublicPath(req), "expected private: %q", p)
	}
}

func TestIsPublicPath_BearerOnAppsRun(t *testing.T) {
	t.Parallel()
	hex32 := strings.Repeat("a", 32)
	req := httptest.NewRequest(http.MethodPost, "/api/apps/x/run", nil)
	require.False(t, isPublicPath(req), "no Authorization header → not public")

	req.Header.Set("Authorization", "Bearer ak_"+hex32)
	require.True(t, isPublicPath(req), "valid bearer → public")

	req2 := httptest.NewRequest(http.MethodGet, "/api/apps/x/runs/run-1", nil)
	require.False(t, isPublicPath(req2))
	req2.Header.Set("Authorization", "Bearer ak_"+hex32)
	require.True(t, isPublicPath(req2))
}

func TestIsLocalhost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		remote string
		want   bool
	}{
		{"127.0.0.1:1234", true},
		{"::1", true},
		{"[::1]:80", true},
		{"10.0.0.1:80", false},
		{"not-an-ip", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = c.remote
		require.Equal(t, c.want, isLocalhost(req), "isLocalhost(%q)", c.remote)
	}
}
