package security

import (
	"testing"
	"time"
)

func TestURLPolicy_NilPolicy(t *testing.T) {
	t.Parallel()
	var p *URLPolicy
	if err := p.Check("https://example.com"); err != nil {
		t.Errorf("nil policy should allow all: %v", err)
	}
}

func TestURLPolicy_DefaultAllow(t *testing.T) {
	t.Parallel()
	p := NewURLPolicy(nil, nil)
	if err := p.Check("https://example.com/page"); err != nil {
		t.Errorf("default allow should permit: %v", err)
	}
}

func TestURLPolicy_DenyRule(t *testing.T) {
	t.Parallel()
	p := NewURLPolicy(nil, []URLRule{
		{Host: "evil.com"},
	})
	if err := p.Check("https://evil.com/malware"); err == nil {
		t.Error("expected deny rule to block evil.com")
	}
	if err := p.Check("https://good.com/page"); err != nil {
		t.Errorf("good.com should be allowed: %v", err)
	}
}

func TestURLPolicy_AllowRule(t *testing.T) {
	t.Parallel()
	p := NewURLPolicy([]URLRule{
		{Host: "api.example.com"},
	}, nil)
	if err := p.Check("https://api.example.com/v1"); err != nil {
		t.Errorf("allow-listed host should pass: %v", err)
	}
	if err := p.Check("https://other.com/v1"); err == nil {
		t.Error("non-allowed host should be blocked when allow rules exist")
	}
}

func TestURLPolicy_WildcardHost(t *testing.T) {
	t.Parallel()
	p := NewURLPolicy(nil, []URLRule{
		{Host: "*.internal.corp"},
	})
	if err := p.Check("https://api.internal.corp/health"); err == nil {
		t.Error("expected wildcard deny to block subdomain")
	}
	if err := p.Check("https://external.com/health"); err != nil {
		t.Errorf("external should be allowed: %v", err)
	}
}

func TestURLPolicy_PathPrefix(t *testing.T) {
	t.Parallel()
	p := NewURLPolicy(nil, []URLRule{
		{Host: "example.com", Path: "/admin"},
	})
	if err := p.Check("https://example.com/admin/users"); err == nil {
		t.Error("expected /admin path to be blocked")
	}
	if err := p.Check("https://example.com/public/page"); err != nil {
		t.Errorf("non-admin path should be allowed: %v", err)
	}
}

func TestURLPolicy_DenyTakesPrecedence(t *testing.T) {
	t.Parallel()
	p := NewURLPolicy(
		[]URLRule{{Host: "example.com"}},
		[]URLRule{{Host: "example.com", Path: "/secret"}},
	)
	if err := p.Check("https://example.com/public"); err != nil {
		t.Errorf("public path should be allowed: %v", err)
	}
	if err := p.Check("https://example.com/secret/data"); err == nil {
		t.Error("deny should override allow for /secret path")
	}
}

func TestURLPolicy_Cache(t *testing.T) {
	t.Parallel()
	p := NewURLPolicy(nil, []URLRule{
		{Host: "blocked.com"},
	}, WithCacheTTL(1*time.Second))

	// First check populates cache.
	err1 := p.Check("https://allowed.com/page")
	if err1 != nil {
		t.Fatalf("should be allowed: %v", err1)
	}

	// Second check should hit cache.
	err2 := p.Check("https://allowed.com/page")
	if err2 != nil {
		t.Fatalf("cached result should allow: %v", err2)
	}
}

func TestURLPolicy_Reset(t *testing.T) {
	t.Parallel()
	p := NewURLPolicy(nil, nil)
	_ = p.Check("https://example.com")
	p.Reset()
	// After reset, cache is empty but check should still work.
	if err := p.Check("https://example.com"); err != nil {
		t.Errorf("after reset should still allow: %v", err)
	}
}

func TestURLPolicy_InvalidURL(t *testing.T) {
	t.Parallel()
	p := NewURLPolicy(nil, nil)
	if err := p.Check("://invalid"); err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestURLPolicy_DefaultDeny(t *testing.T) {
	t.Parallel()
	p := NewURLPolicy(nil, nil, WithDefaultAllow(false))
	if err := p.Check("https://anything.com"); err == nil {
		t.Error("default-deny should block when no allow rules match")
	}
}

func TestURLRule_Matches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		rule     URLRule
		host     string
		path     string
		expected bool
	}{
		{URLRule{Host: "example.com"}, "example.com", "/", true},
		{URLRule{Host: "example.com"}, "other.com", "/", false},
		{URLRule{Host: "*.example.com"}, "sub.example.com", "/", true},
		{URLRule{Host: "*.example.com"}, "example.com", "/", true},
		{URLRule{Host: "*.example.com"}, "other.com", "/", false},
		{URLRule{Path: "/api"}, "any.com", "/api/v1", true},
		{URLRule{Path: "/api"}, "any.com", "/web", false},
		{URLRule{}, "any.com", "/any", true}, // empty rule matches all
	}

	for _, tt := range tests {
		got := tt.rule.matches(tt.host, tt.path)
		if got != tt.expected {
			t.Errorf("rule %+v matches(%q, %q) = %v, want %v",
				tt.rule, tt.host, tt.path, got, tt.expected)
		}
	}
}
