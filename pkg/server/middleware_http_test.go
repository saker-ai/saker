package server

import "testing"

func TestIsLocalhostOrigin(t *testing.T) {
	cases := []struct {
		origin string
		want   bool
	}{
		{"http://localhost:10111", true},
		{"http://localhost:10112", true},
		{"http://localhost:3000", true},
		{"https://localhost:443", true},
		{"http://127.0.0.1:8080", true},
		{"https://127.0.0.1:443", true},
		{"http://[::1]:8080", true},
		{"https://[::1]:443", true},
		{"http://evil.com:10112", false},
		{"https://example.com", false},
		{"http://192.168.1.1:8080", false},
	}
	for _, c := range cases {
		got := isLocalhostOrigin(c.origin)
		if got != c.want {
			t.Errorf("isLocalhostOrigin(%q) = %v, want %v", c.origin, got, c.want)
		}
	}
}

func TestIsAllowedWSOrigin_NoExplicitOrigins(t *testing.T) {
	// With no explicit origins, localhost should pass and remote should fail.
	if !isAllowedWSOrigin("http://localhost:10112", nil, "localhost:10112") {
		t.Error("localhost should be allowed with nil origins")
	}
	if !isAllowedWSOrigin("http://127.0.0.1:10112", nil, "127.0.0.1:10112") {
		t.Error("127.0.0.1 should be allowed with nil origins")
	}
	if isAllowedWSOrigin("http://evil.com:10112", nil, "myserver:10112") {
		t.Error("remote origin should be blocked with nil origins when not same-origin")
	}
}

func TestIsAllowedWSOrigin_SameOrigin(t *testing.T) {
	// Same-origin remote requests (origin host matches request Host) should pass.
	if !isAllowedWSOrigin("http://[2409:8a28:6e6:b030::1]:10112", nil, "[2409:8a28:6e6:b030::1]:10112") {
		t.Error("same-origin IPv6 should be allowed")
	}
	if !isAllowedWSOrigin("http://192.168.1.100:10112", nil, "192.168.1.100:10112") {
		t.Error("same-origin LAN IP should be allowed")
	}
	if isAllowedWSOrigin("http://evil.com:10112", nil, "192.168.1.100:10112") {
		t.Error("cross-origin should be blocked")
	}
}

func TestIsAllowedWSOrigin_ExplicitOrigins(t *testing.T) {
	allowed := []string{"https://app.example.com", "http://dev.example.com:3000"}
	if !isAllowedWSOrigin("https://app.example.com", allowed, "app.example.com") {
		t.Error("explicit allowed origin should pass")
	}
	if !isAllowedWSOrigin("http://dev.example.com:3000", allowed, "dev.example.com:3000") {
		t.Error("explicit allowed origin should pass")
	}
	if isAllowedWSOrigin("http://evil.com", allowed, "myserver:10112") {
		t.Error("non-listed origin should be blocked")
	}
}

func TestIsAllowedWSOrigin_Wildcard(t *testing.T) {
	// Wildcard "*" is intentionally NOT honored: WebSocket upgrades carry
	// the session cookie (credentialed), and combining "*" with credentials
	// would let any origin read user data. Operators must list explicit
	// origins. Verify that an explicit "*" entry does NOT bypass the check.
	allowed := []string{"*"}
	if isAllowedWSOrigin("http://anything.com", allowed, "myserver:10112") {
		t.Error("wildcard must not honor arbitrary origins (credentialed-CORS rule)")
	}
}
