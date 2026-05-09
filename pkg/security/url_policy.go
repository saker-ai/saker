package security

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// URLPolicy enforces user-configurable URL access rules with a TTL cache
// for resolved decisions. Rules are evaluated in order: deny rules first,
// then allow rules, then the default policy.
type URLPolicy struct {
	mu       sync.RWMutex
	allow    []URLRule
	deny     []URLRule
	defAllow bool // default when no rule matches

	cache    map[string]cachedDecision
	cacheTTL time.Duration
}

// URLRule describes a pattern-based URL access rule.
type URLRule struct {
	// Host matches the URL hostname. Supports prefix wildcard: "*.example.com"
	// matches "foo.example.com" and "bar.example.com".
	Host string `json:"host,omitempty"`

	// Path is an optional path prefix. If set, the rule only applies when the
	// URL path starts with this prefix.
	Path string `json:"path,omitempty"`
}

type cachedDecision struct {
	allowed   bool
	expiresAt time.Time
}

// URLPolicyOption configures URLPolicy construction.
type URLPolicyOption func(*URLPolicy)

// WithDefaultAllow sets the default policy when no rule matches.
func WithDefaultAllow(allow bool) URLPolicyOption {
	return func(p *URLPolicy) { p.defAllow = allow }
}

// WithCacheTTL sets the TTL for cached decisions.
func WithCacheTTL(ttl time.Duration) URLPolicyOption {
	return func(p *URLPolicy) { p.cacheTTL = ttl }
}

// NewURLPolicy creates a URL policy with the given allow/deny rules.
// Default policy is allow (fail-open) with a 5-minute cache TTL.
func NewURLPolicy(allow, deny []URLRule, opts ...URLPolicyOption) *URLPolicy {
	p := &URLPolicy{
		allow:    allow,
		deny:     deny,
		defAllow: true,
		cache:    make(map[string]cachedDecision),
		cacheTTL: 5 * time.Minute,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Check returns nil if the URL is allowed, or an error describing why it is
// blocked. Results are cached by the full URL string.
func (p *URLPolicy) Check(rawURL string) error {
	if p == nil {
		return nil
	}

	// Check cache first.
	p.mu.RLock()
	if cached, ok := p.cache[rawURL]; ok && time.Now().Before(cached.expiresAt) {
		p.mu.RUnlock()
		if cached.allowed {
			return nil
		}
		return fmt.Errorf("url %q is blocked by policy", rawURL)
	}
	p.mu.RUnlock()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("url_policy: invalid url: %w", err)
	}

	hostname := strings.ToLower(parsed.Hostname())
	path := parsed.Path

	allowed := p.evaluate(hostname, path)

	// Cache the decision.
	p.mu.Lock()
	p.purgeExpiredLocked()
	p.cache[rawURL] = cachedDecision{
		allowed:   allowed,
		expiresAt: time.Now().Add(p.cacheTTL),
	}
	p.mu.Unlock()

	if !allowed {
		return fmt.Errorf("url %q is blocked by policy", rawURL)
	}
	return nil
}

// evaluate checks deny rules first, then allow rules, then default.
func (p *URLPolicy) evaluate(hostname, path string) bool {
	// Deny rules take precedence.
	for _, rule := range p.deny {
		if rule.matches(hostname, path) {
			return false
		}
	}

	// If allow rules are defined, URL must match at least one.
	if len(p.allow) > 0 {
		for _, rule := range p.allow {
			if rule.matches(hostname, path) {
				return true
			}
		}
		return false
	}

	return p.defAllow
}

// matches checks whether a rule applies to the given hostname and path.
func (r URLRule) matches(hostname, path string) bool {
	if r.Host != "" {
		pattern := strings.ToLower(r.Host)
		if strings.HasPrefix(pattern, "*.") {
			// Wildcard: *.example.com matches sub.example.com
			suffix := pattern[1:] // ".example.com"
			if hostname != pattern[2:] && !strings.HasSuffix(hostname, suffix) {
				return false
			}
		} else if hostname != pattern {
			return false
		}
	}
	if r.Path != "" && !strings.HasPrefix(path, r.Path) {
		return false
	}
	return true
}

const maxURLPolicyCacheSize = 10000

func (p *URLPolicy) purgeExpiredLocked() {
	now := time.Now()
	for k, v := range p.cache {
		if now.After(v.expiresAt) {
			delete(p.cache, k)
		}
	}
	// Hard cap to prevent unbounded growth from unique URLs.
	if len(p.cache) > maxURLPolicyCacheSize {
		p.cache = make(map[string]cachedDecision)
	}
}

// Reset clears the decision cache.
func (p *URLPolicy) Reset() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.cache = make(map[string]cachedDecision)
	p.mu.Unlock()
}
