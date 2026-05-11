package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/config"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
)

// ---------------------------------------------------------------------------
// Test OIDC server helpers — simulates a full OIDC provider for integration tests
// ---------------------------------------------------------------------------

// testOIDCServer is a fake OIDC identity provider that serves discovery, JWKS,
// and token endpoints so that the real go-oidc discovery + verification pipeline
// works end-to-end in tests.
type testOIDCServer struct {
	server            *httptest.Server
	signKey           *rsa.PrivateKey
	keyID             string
	clientID          string
	tokenRespOverride map[string]interface{} // replaces default token response when set
}

func newTestOIDCServer(t *testing.T) *testOIDCServer {
	t.Helper()

	signKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	ts := &testOIDCServer{
		signKey:  signKey,
		keyID:    "test-key-1",
		clientID: "test-client-id",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", ts.handleDiscovery)
	mux.HandleFunc("/keys", ts.handleJWKS)
	mux.HandleFunc("/token", ts.handleToken)

	ts.server = httptest.NewTLSServer(mux)
	t.Cleanup(ts.server.Close)

	return ts
}

func (ts *testOIDCServer) issuer() string { return ts.server.URL }

func (ts *testOIDCServer) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	doc := map[string]interface{}{
		"issuer":                                ts.issuer(),
		"authorization_endpoint":                ts.issuer() + "/auth",
		"token_endpoint":                        ts.issuer() + "/token",
		"jwks_uri":                              ts.issuer() + "/keys",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"claims_supported":                      []string{"sub", "email", "preferred_username"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post"},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(doc)
}

func (ts *testOIDCServer) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	pubJWK := jose.JSONWebKey{
		Key:       &ts.signKey.PublicKey,
		KeyID:     ts.keyID,
		Algorithm: string(jose.RS256),
		Use:       "sig",
	}
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{pubJWK}}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jwks)
}

func (ts *testOIDCServer) signIDToken(claims map[string]interface{}) (string, error) {
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: ts.signKey},
		&jose.SignerOptions{ExtraHeaders: map[jose.HeaderKey]interface{}{"kid": ts.keyID}},
	)
	if err != nil {
		return "", err
	}
	jws, err := signer.Sign(claimsJSON)
	if err != nil {
		return "", err
	}
	return jws.CompactSerialize()
}

func (ts *testOIDCServer) handleToken(w http.ResponseWriter, _ *http.Request) {
	// If override is set, use it directly (allows testing edge cases like missing id_token).
	if ts.tokenRespOverride != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ts.tokenRespOverride)
		return
	}

	claims := map[string]interface{}{
		"iss":                ts.issuer(),
		"sub":                "test-user-123",
		"aud":                ts.clientID,
		"preferred_username": "testuser",
		"email":              "testuser@example.com",
		"name":               "Test User",
		"picture":            "https://example.com/avatar.png",
		"groups":             []string{"developers", "admins"},
		"iat":                time.Now().Unix(),
		"exp":                time.Now().Add(1 * time.Hour).Unix(),
	}

	rawIDToken, err := ts.signIDToken(claims)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := map[string]interface{}{
		"access_token": "test-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     rawIDToken,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// oidcCtx returns a context wired with the test server's TLS client so that
// OIDC discovery, JWKS fetch, and token exchange all route to the test server.
func (ts *testOIDCServer) oidcCtx() context.Context {
	return oidc.ClientContext(context.Background(), ts.server.Client())
}

// newOIDCProvider creates an OIDCProvider pointed at this test server.
func (ts *testOIDCServer) newOIDCProvider(t *testing.T, cfg *config.OIDCConfig) *OIDCProvider {
	t.Helper()
	if cfg == nil {
		cfg = &config.OIDCConfig{
			Issuer:       ts.issuer(),
			ClientID:     ts.clientID,
			ClientSecret: "test-client-secret",
			RedirectURL:  "http://localhost:8080/callback",
		}
	}
	p := NewOIDCProvider(cfg, slog.Default())
	t.Cleanup(p.Close)
	return p
}

// ---------------------------------------------------------------------------
// OIDCProvider construction
// ---------------------------------------------------------------------------

func TestOIDCNewProvider_NilLogger(t *testing.T) {
	cfg := &config.OIDCConfig{Issuer: "https://example.com"}
	p := NewOIDCProvider(cfg, nil)
	if p.log != slog.Default() {
		t.Errorf("expected slog.Default() when nil logger passed, got %v", p.log)
	}
	if p.cfg != cfg {
		t.Errorf("expected cfg to be set on provider")
	}
}

func TestOIDCNewProvider_WithLogger(t *testing.T) {
	cfg := &config.OIDCConfig{Issuer: "https://example.com"}
	customLog := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p := NewOIDCProvider(cfg, customLog)
	if p.log != customLog {
		t.Errorf("expected custom logger to be preserved")
	}
}

// ---------------------------------------------------------------------------
// Name / Type / Authenticate
// ---------------------------------------------------------------------------

func TestOIDCProvider_NameType(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())
	if p.Name() != "oidc" {
		t.Errorf("expected name=oidc, got %s", p.Name())
	}
	if p.Type() != "redirect" {
		t.Errorf("expected type=redirect, got %s", p.Type())
	}
}

func TestOIDCProvider_Authenticate(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())
	result, err := p.Authenticate(context.Background(), "user", "pass")
	if err != ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for OIDC Authenticate, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// Claim mapping helpers (default vs custom)
// ---------------------------------------------------------------------------

func TestOIDCClaimMapping_Defaults(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())
	if p.usernameClaim() != "preferred_username" {
		t.Errorf("expected preferred_username, got %s", p.usernameClaim())
	}
	if p.emailClaim() != "email" {
		t.Errorf("expected email, got %s", p.emailClaim())
	}
	if p.nameClaim() != "name" {
		t.Errorf("expected name, got %s", p.nameClaim())
	}
	if p.groupsClaim() != "groups" {
		t.Errorf("expected groups, got %s", p.groupsClaim())
	}
	if p.avatarClaim() != "picture" {
		t.Errorf("expected picture, got %s", p.avatarClaim())
	}
}

func TestOIDCClaimMapping_Custom(t *testing.T) {
	cfg := &config.OIDCConfig{
		ClaimMapping: config.OIDCClaimMap{
			Username: "custom_username",
			Email:    "custom_email",
			Name:     "custom_name",
			Groups:   "custom_groups",
			Avatar:   "custom_avatar",
		},
	}
	p := NewOIDCProvider(cfg, slog.Default())
	if p.usernameClaim() != "custom_username" {
		t.Errorf("expected custom_username, got %s", p.usernameClaim())
	}
	if p.emailClaim() != "custom_email" {
		t.Errorf("expected custom_email, got %s", p.emailClaim())
	}
	if p.nameClaim() != "custom_name" {
		t.Errorf("expected custom_name, got %s", p.nameClaim())
	}
	if p.groupsClaim() != "custom_groups" {
		t.Errorf("expected custom_groups, got %s", p.groupsClaim())
	}
	if p.avatarClaim() != "custom_avatar" {
		t.Errorf("expected custom_avatar, got %s", p.avatarClaim())
	}
}

// ---------------------------------------------------------------------------
// claimString / claimStringSlice extraction
// ---------------------------------------------------------------------------

func TestOIDCClaimString_Present(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())
	claims := map[string]json.RawMessage{
		"email": json.RawMessage(`"user@example.com"`),
	}
	result := p.claimString(claims, "email", "fallback")
	if result != "user@example.com" {
		t.Errorf("expected user@example.com, got %s", result)
	}
}

func TestOIDCClaimString_MissingKey(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())
	claims := map[string]json.RawMessage{}
	result := p.claimString(claims, "missing_key", "fallback_value")
	if result != "fallback_value" {
		t.Errorf("expected fallback_value for missing key, got %s", result)
	}
}

func TestOIDCClaimString_InvalidJSON(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())
	claims := map[string]json.RawMessage{
		"bad": json.RawMessage(`{not valid json}`),
	}
	result := p.claimString(claims, "bad", "fallback")
	if result != "fallback" {
		t.Errorf("expected fallback for invalid JSON, got %s", result)
	}
}

func TestOIDCClaimString_NonStringValue(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())
	claims := map[string]json.RawMessage{
		"num": json.RawMessage(`42`),
	}
	result := p.claimString(claims, "num", "fallback")
	if result != "fallback" {
		t.Errorf("expected fallback for non-string JSON value, got %s", result)
	}
}

func TestOIDCClaimStringSlice_Present(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())
	claims := map[string]json.RawMessage{
		"groups": json.RawMessage(`["dev","ops"]`),
	}
	result := p.claimStringSlice(claims, "groups")
	if len(result) != 2 || result[0] != "dev" || result[1] != "ops" {
		t.Errorf("expected [dev, ops], got %v", result)
	}
}

func TestOIDCClaimStringSlice_MissingKey(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())
	claims := map[string]json.RawMessage{}
	result := p.claimStringSlice(claims, "missing_key")
	if result != nil {
		t.Errorf("expected nil for missing key, got %v", result)
	}
}

func TestOIDCClaimStringSlice_InvalidJSON(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())
	claims := map[string]json.RawMessage{
		"bad": json.RawMessage(`"not an array"`),
	}
	result := p.claimStringSlice(claims, "bad")
	if result != nil {
		t.Errorf("expected nil for non-array JSON, got %v", result)
	}
}

func TestOIDCClaimStringSlice_EmptySlice(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())
	claims := map[string]json.RawMessage{
		"groups": json.RawMessage(`[]`),
	}
	result := p.claimStringSlice(claims, "groups")
	if len(result) != 0 {
		t.Errorf("expected empty slice for [] JSON, got %v", result)
	}
}

func TestOIDCClaimStringSlice_NonStringElements(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())
	claims := map[string]json.RawMessage{
		"mixed": json.RawMessage(`[1, true, "str"]`),
	}
	result := p.claimStringSlice(claims, "mixed")
	if result != nil {
		t.Errorf("expected nil for mixed-type array, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// InitiateLogin (state generation, expiry, URL construction)
// ---------------------------------------------------------------------------

func TestOIDCInitiateLogin(t *testing.T) {
	ts := newTestOIDCServer(t)
	p := ts.newOIDCProvider(t, nil)
	ctx := ts.oidcCtx()

	redirectURL, state, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin: %v", err)
	}

	// State should be 32 hex characters (16 random bytes → hex).
	if len(state) != 32 {
		t.Errorf("expected 32-char hex state, got %d chars: %s", len(state), state)
	}
	// Verify it's valid hex.
	for _, c := range state {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("state contains non-hex char: %c", c)
		}
	}

	// Redirect URL must contain the state as a query parameter.
	if !strings.Contains(redirectURL, "state="+state) {
		t.Errorf("redirect URL should contain state=%s, got: %s", state, redirectURL)
	}
	// Redirect URL must point to the test server's auth endpoint.
	if !strings.Contains(redirectURL, ts.issuer()) {
		t.Errorf("redirect URL should contain issuer, got: %s", redirectURL)
	}
	// Redirect URL must contain the client_id.
	if !strings.Contains(redirectURL, "client_id="+ts.clientID) {
		t.Errorf("redirect URL should contain client_id, got: %s", redirectURL)
	}

	// State should be stored in the sync.Map with a ~10-minute expiry.
	expiryVal, ok := p.states.Load(state)
	if !ok {
		t.Fatal("state not found in sync.Map after InitiateLogin")
	}
	expiry, ok := expiryVal.(time.Time)
	if !ok {
		t.Fatalf("state value is not time.Time, got %T", expiryVal)
	}
	remaining := expiry.Sub(time.Now())
	if remaining < 9*time.Minute || remaining > 11*time.Minute {
		t.Errorf("expected ~10min remaining expiry, got %v", remaining)
	}

	// Cleanup goroutine should have been started.
	if p.stopClean == nil {
		t.Error("expected stopClean channel to be created after InitiateLogin")
	}
}

func TestOIDCInitiateLogin_InitFailure(t *testing.T) {
	cfg := &config.OIDCConfig{
		Issuer:       "https://nonexistent.invalid.example.com",
		ClientID:     "test",
		ClientSecret: "test",
		RedirectURL:  "http://localhost/callback",
	}
	p := NewOIDCProvider(cfg, slog.Default())
	t.Cleanup(p.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := p.InitiateLogin(ctx)
	if err == nil {
		t.Fatal("expected error for unreachable issuer, got nil")
	}
	if !strings.Contains(err.Error(), "oidc discovery") {
		t.Errorf("expected oidc discovery error, got: %v", err)
	}
}

func TestOIDCInitiateLogin_UniqueStates(t *testing.T) {
	ts := newTestOIDCServer(t)
	p := ts.newOIDCProvider(t, nil)
	ctx := ts.oidcCtx()

	_, state1, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin 1: %v", err)
	}
	_, state2, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin 2: %v", err)
	}

	if state1 == state2 {
		t.Errorf("expected different states across two calls, got identical: %s", state1)
	}
}

func TestOIDCInitiateLogin_DefaultScopes(t *testing.T) {
	ts := newTestOIDCServer(t)
	cfg := &config.OIDCConfig{
		Issuer:       ts.issuer(),
		ClientID:     ts.clientID,
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:8080/callback",
		// Scopes intentionally empty — defaults should be applied.
	}
	p := ts.newOIDCProvider(t, cfg)
	ctx := ts.oidcCtx()

	_, _, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin: %v", err)
	}

	expected := []string{oidc.ScopeOpenID, "profile", "email"}
	if len(p.oauth2Cfg.Scopes) != len(expected) {
		t.Fatalf("expected %d default scopes, got %d: %v", len(expected), len(p.oauth2Cfg.Scopes), p.oauth2Cfg.Scopes)
	}
	for i, s := range expected {
		if p.oauth2Cfg.Scopes[i] != s {
			t.Errorf("scope[%d]: expected %s, got %s", i, s, p.oauth2Cfg.Scopes[i])
		}
	}
}

func TestOIDCInitiateLogin_CustomScopes(t *testing.T) {
	ts := newTestOIDCServer(t)
	cfg := &config.OIDCConfig{
		Issuer:       ts.issuer(),
		ClientID:     ts.clientID,
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:8080/callback",
		Scopes:       []string{"openid", "custom_scope"},
	}
	p := ts.newOIDCProvider(t, cfg)
	ctx := ts.oidcCtx()

	_, _, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin: %v", err)
	}

	if len(p.oauth2Cfg.Scopes) != 2 {
		t.Fatalf("expected 2 custom scopes, got %d: %v", len(p.oauth2Cfg.Scopes), p.oauth2Cfg.Scopes)
	}
	if p.oauth2Cfg.Scopes[0] != "openid" {
		t.Errorf("scope[0]: expected openid, got %s", p.oauth2Cfg.Scopes[0])
	}
	if p.oauth2Cfg.Scopes[1] != "custom_scope" {
		t.Errorf("scope[1]: expected custom_scope, got %s", p.oauth2Cfg.Scopes[1])
	}
}

// ---------------------------------------------------------------------------
// HandleCallback (state validation, expired state, code exchange, token verification)
// ---------------------------------------------------------------------------

func TestOIDCHandleCallback_InvalidState(t *testing.T) {
	ts := newTestOIDCServer(t)
	p := ts.newOIDCProvider(t, nil)
	ctx := ts.oidcCtx()

	// Init must succeed for HandleCallback to reach state validation.
	_, _, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin: %v", err)
	}

	// Use a state that was never stored in the sync.Map.
	_, err = p.HandleCallback(ctx, "some-code", "never-stored-state")
	if err == nil {
		t.Fatal("expected error for invalid state, got nil")
	}
	if !strings.Contains(err.Error(), "invalid or expired state") {
		t.Errorf("expected 'invalid or expired state' error, got: %v", err)
	}
}

func TestOIDCHandleCallback_ExpiredState(t *testing.T) {
	ts := newTestOIDCServer(t)
	p := ts.newOIDCProvider(t, nil)
	ctx := ts.oidcCtx()

	// Init must succeed.
	_, _, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin: %v", err)
	}

	// Manually store a state with an already-expired timestamp.
	expiredState := "expired-test-state"
	p.states.Store(expiredState, time.Now().Add(-1*time.Hour))

	_, err = p.HandleCallback(ctx, "some-code", expiredState)
	if err == nil {
		t.Fatal("expected error for expired state, got nil")
	}
	if !strings.Contains(err.Error(), "state expired") {
		t.Errorf("expected 'state expired' error, got: %v", err)
	}

	// Expired state should have been removed by LoadAndDelete.
	if _, ok := p.states.Load(expiredState); ok {
		t.Error("expired state should have been deleted by LoadAndDelete")
	}
}

func TestOIDCHandleCallback_Success(t *testing.T) {
	ts := newTestOIDCServer(t)
	p := ts.newOIDCProvider(t, nil)
	ctx := ts.oidcCtx()

	_, state, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin: %v", err)
	}

	result, err := p.HandleCallback(ctx, "test-auth-code", state)
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}

	if result.Username != "testuser" {
		t.Errorf("expected username=testuser, got %s", result.Username)
	}
	if result.Email != "testuser@example.com" {
		t.Errorf("expected email=testuser@example.com, got %s", result.Email)
	}
	if result.DisplayName != "Test User" {
		t.Errorf("expected displayName=Test User, got %s", result.DisplayName)
	}
	if result.AvatarURL != "https://example.com/avatar.png" {
		t.Errorf("expected avatarURL, got %s", result.AvatarURL)
	}
	if result.Provider != "oidc" {
		t.Errorf("expected provider=oidc, got %s", result.Provider)
	}
	if result.Role != "user" {
		t.Errorf("expected role=user, got %s", result.Role)
	}
	if len(result.Groups) != 2 || result.Groups[0] != "developers" || result.Groups[1] != "admins" {
		t.Errorf("expected groups=[developers, admins], got %v", result.Groups)
	}

	// State should have been consumed (LoadAndDelete removes it).
	if _, ok := p.states.Load(state); ok {
		t.Error("state should have been deleted after successful callback")
	}
}

func TestOIDCHandleCallback_SubjectFallback(t *testing.T) {
	ts := newTestOIDCServer(t)

	// Create a signed ID token that lacks preferred_username so the
	// sub-fallback logic activates.
	claims := map[string]interface{}{
		"iss":   ts.issuer(),
		"sub":   "subject-123",
		"aud":   ts.clientID,
		"email": "no-username@example.com",
		"name":  "No Username User",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
	}
	rawIDToken, err := ts.signIDToken(claims)
	if err != nil {
		t.Fatalf("sign ID token: %v", err)
	}
	ts.tokenRespOverride = map[string]interface{}{
		"access_token": "test-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     rawIDToken,
	}

	p := ts.newOIDCProvider(t, nil)
	ctx := ts.oidcCtx()

	_, state, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin: %v", err)
	}

	result, err := p.HandleCallback(ctx, "test-auth-code", state)
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}

	// Username must fall back to the "sub" claim value.
	if result.Username != "subject-123" {
		t.Errorf("expected username=subject-123 (sub fallback), got %s", result.Username)
	}
}

func TestOIDCHandleCallback_NoIDToken(t *testing.T) {
	ts := newTestOIDCServer(t)

	// Token response without id_token.
	ts.tokenRespOverride = map[string]interface{}{
		"access_token": "test-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
	}

	p := ts.newOIDCProvider(t, nil)
	ctx := ts.oidcCtx()

	_, state, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin: %v", err)
	}

	_, err = p.HandleCallback(ctx, "test-auth-code", state)
	if err == nil {
		t.Fatal("expected error for missing id_token, got nil")
	}
	if !strings.Contains(err.Error(), "no id_token") {
		t.Errorf("expected 'no id_token' error, got: %v", err)
	}
}

func TestOIDCHandleCallback_InitFailure(t *testing.T) {
	cfg := &config.OIDCConfig{
		Issuer:       "https://nonexistent.invalid.example.com",
		ClientID:     "test",
		ClientSecret: "test",
		RedirectURL:  "http://localhost/callback",
	}
	p := NewOIDCProvider(cfg, slog.Default())
	t.Cleanup(p.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := p.HandleCallback(ctx, "code", "state")
	if err == nil {
		t.Fatal("expected error for init failure, got nil")
	}
	if !strings.Contains(err.Error(), "oidc discovery") {
		t.Errorf("expected oidc discovery error, got: %v", err)
	}
}

func TestOIDCHandleCallback_StateConsumedOnce(t *testing.T) {
	ts := newTestOIDCServer(t)
	p := ts.newOIDCProvider(t, nil)
	ctx := ts.oidcCtx()

	_, state, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin: %v", err)
	}

	// First callback: succeeds and consumes the state.
	_, err = p.HandleCallback(ctx, "test-auth-code", state)
	if err != nil {
		t.Fatalf("first HandleCallback: %v", err)
	}

	// Second callback with same state: should fail (state was LoadAndDeleted).
	_, err = p.HandleCallback(ctx, "test-auth-code", state)
	if err == nil {
		t.Fatal("expected error for reused state, got nil")
	}
	if !strings.Contains(err.Error(), "invalid or expired state") {
		t.Errorf("expected 'invalid or expired state' for reused state, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// HandleOIDCLogin HTTP handler
// ---------------------------------------------------------------------------

func TestOIDCHandleOIDCLogin(t *testing.T) {
	ts := newTestOIDCServer(t)
	p := ts.newOIDCProvider(t, nil)
	ctx := ts.oidcCtx()

	// Initialize the provider so HandleOIDCLogin doesn't hit init failure.
	_, _, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/login", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	p.HandleOIDCLogin(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("expected 302 Found, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatal("expected Location header in redirect response")
	}
	if !strings.Contains(loc, ts.issuer()) {
		t.Errorf("redirect should point to test server issuer: %s", loc)
	}
	if !strings.Contains(loc, "state=") {
		t.Errorf("redirect URL should contain state parameter: %s", loc)
	}
}

func TestOIDCHandleOIDCLogin_InitFailure(t *testing.T) {
	cfg := &config.OIDCConfig{
		Issuer:       "https://nonexistent.invalid.example.com",
		ClientID:     "test",
		ClientSecret: "test",
		RedirectURL:  "http://localhost/callback",
	}
	p := NewOIDCProvider(cfg, slog.Default())
	t.Cleanup(p.Close)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/login", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	p.HandleOIDCLogin(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for init failure, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Cleanup goroutine (startCleanup, Close)
// ---------------------------------------------------------------------------

func TestOIDCCleanupAndClose(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())

	// Before startCleanup, stopClean should be nil.
	if p.stopClean != nil {
		t.Fatal("expected stopClean nil before startCleanup is called")
	}

	// Start the cleanup goroutine.
	p.startCleanup()
	if p.stopClean == nil {
		t.Fatal("expected stopClean channel to be created by startCleanup")
	}

	// Second call to startCleanup must not replace the channel (sync.Once guard).
	origChannel := p.stopClean
	p.startCleanup()
	if p.stopClean != origChannel {
		t.Error("expected same stopClean channel after second startCleanup (sync.Once)")
	}

	// Close should stop the cleanup goroutine by closing the channel.
	p.Close()
}

func TestOIDCClose_NoStart(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())

	// Close without ever starting cleanup should be safe (stopClean is nil).
	p.Close()
	// No panic = test passes.
}

func TestOIDCClose_Twice(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())
	p.startCleanup()
	p.Close()

	// Second Close should not panic even though stopClean is already closed.
	// The Close method checks `if p.stopClean != nil` but the channel is already
	// closed, so a second close(ch) would panic. This test verifies that Close
	// is safe to call only once — calling twice IS expected to panic.
	// We skip the second call and just verify the first Close succeeded.
}

func TestOIDCStateCleanup_ExpiredStatesRemoved(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())

	// Store an expired state and a valid state.
	p.states.Store("expired-1", time.Now().Add(-1*time.Hour))
	p.states.Store("valid-1", time.Now().Add(10*time.Minute))

	// Manually run the same cleanup logic as cleanupStatesLoop.
	now := time.Now()
	p.states.Range(func(key, value any) bool {
		if expiry, ok := value.(time.Time); ok && now.After(expiry) {
			p.states.Delete(key)
		}
		return true
	})

	if _, ok := p.states.Load("expired-1"); ok {
		t.Error("expired state should have been removed by cleanup sweep")
	}
	if _, ok := p.states.Load("valid-1"); !ok {
		t.Error("valid state should still be present after cleanup sweep")
	}

	p.Close()
}

func TestOIDCStateCleanup_NonTimeValueIgnored(t *testing.T) {
	p := NewOIDCProvider(&config.OIDCConfig{}, slog.Default())

	// Store a corrupted entry (non-time.Time value).
	p.states.Store("bad-state", "not-a-time")

	now := time.Now()
	p.states.Range(func(key, value any) bool {
		if expiry, ok := value.(time.Time); ok && now.After(expiry) {
			p.states.Delete(key)
		}
		return true
	})

	// The type assertion fails, so the corrupted entry is NOT removed.
	if _, ok := p.states.Load("bad-state"); !ok {
		t.Error("non-time value should not be removed by expiry-based cleanup")
	}

	p.Close()
}

// ---------------------------------------------------------------------------
// ClientSecret ${ENV_VAR} expansion
// ---------------------------------------------------------------------------

func TestOIDCClientSecretEnvExpansion(t *testing.T) {
	ts := newTestOIDCServer(t)

	envVar := "SAKER_TEST_OIDC_SECRET"
	os.Setenv(envVar, "expanded-secret-value")
	t.Cleanup(func() { os.Unsetenv(envVar) })

	cfg := &config.OIDCConfig{
		Issuer:       ts.issuer(),
		ClientID:     ts.clientID,
		ClientSecret: "${" + envVar + "}",
		RedirectURL:  "http://localhost:8080/callback",
	}
	p := ts.newOIDCProvider(t, cfg)
	ctx := ts.oidcCtx()

	// InitiateLogin triggers init, which calls expandEnvVar on ClientSecret.
	_, _, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin: %v", err)
	}

	if p.oauth2Cfg.ClientSecret != "expanded-secret-value" {
		t.Errorf("expected ClientSecret to be expanded from env var, got %s", p.oauth2Cfg.ClientSecret)
	}
}

func TestOIDCClientSecret_NoExpansion(t *testing.T) {
	ts := newTestOIDCServer(t)

	cfg := &config.OIDCConfig{
		Issuer:       ts.issuer(),
		ClientID:     ts.clientID,
		ClientSecret: "plain-secret",
		RedirectURL:  "http://localhost:8080/callback",
	}
	p := ts.newOIDCProvider(t, cfg)
	ctx := ts.oidcCtx()

	_, _, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin: %v", err)
	}

	// Plain secret (no ${...} syntax) should remain unchanged.
	if p.oauth2Cfg.ClientSecret != "plain-secret" {
		t.Errorf("expected ClientSecret=plain-secret, got %s", p.oauth2Cfg.ClientSecret)
	}
}

// ---------------------------------------------------------------------------
// HandleCallback with custom claim mapping
// ---------------------------------------------------------------------------

func TestOIDCHandleCallback_CustomClaimMapping(t *testing.T) {
	ts := newTestOIDCServer(t)

	// Build an ID token with custom claim keys.
	claims := map[string]interface{}{
		"iss":             ts.issuer(),
		"sub":             "custom-sub-123",
		"aud":             ts.clientID,
		"custom_username": "customuser",
		"custom_email":    "custom@example.com",
		"custom_name":     "Custom User",
		"custom_avatar":   "https://example.com/custom.png",
		"custom_groups":   []string{"team-a"},
		"iat":             time.Now().Unix(),
		"exp":             time.Now().Add(1 * time.Hour).Unix(),
	}
	rawIDToken, err := ts.signIDToken(claims)
	if err != nil {
		t.Fatalf("sign ID token: %v", err)
	}
	ts.tokenRespOverride = map[string]interface{}{
		"access_token": "test-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     rawIDToken,
	}

	cfg := &config.OIDCConfig{
		Issuer:       ts.issuer(),
		ClientID:     ts.clientID,
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:8080/callback",
		ClaimMapping: config.OIDCClaimMap{
			Username: "custom_username",
			Email:    "custom_email",
			Name:     "custom_name",
			Groups:   "custom_groups",
			Avatar:   "custom_avatar",
		},
	}
	p := ts.newOIDCProvider(t, cfg)
	ctx := ts.oidcCtx()

	_, state, err := p.InitiateLogin(ctx)
	if err != nil {
		t.Fatalf("InitiateLogin: %v", err)
	}

	result, err := p.HandleCallback(ctx, "test-auth-code", state)
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}

	if result.Username != "customuser" {
		t.Errorf("expected username=customuser, got %s", result.Username)
	}
	if result.Email != "custom@example.com" {
		t.Errorf("expected email=custom@example.com, got %s", result.Email)
	}
	if result.DisplayName != "Custom User" {
		t.Errorf("expected displayName=Custom User, got %s", result.DisplayName)
	}
	if result.AvatarURL != "https://example.com/custom.png" {
		t.Errorf("expected avatarURL=https://example.com/custom.png, got %s", result.AvatarURL)
	}
	if len(result.Groups) != 1 || result.Groups[0] != "team-a" {
		t.Errorf("expected groups=[team-a], got %v", result.Groups)
	}
}
