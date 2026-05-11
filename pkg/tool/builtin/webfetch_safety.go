// Host allow/deny validation and shared param-extraction helpers used by
// WebFetchTool. The HTTP fetching code lives in webfetch_fetch.go.
package toolbuiltin

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

type hostValidator struct {
	allowed      map[string]struct{}
	blocked      map[string]struct{}
	allowPrivate bool
}

var defaultBlockedHosts = map[string]struct{}{
	"localhost":                {},
	"127.0.0.1":                {},
	"::1":                      {},
	"metadata.google.internal": {},
	"169.254.169.254":          {},
}

func newHostValidator(allowed, blocked []string, allowPrivate bool) hostValidator {
	hv := hostValidator{
		allowed:      sliceToSet(allowed),
		blocked:      sliceToSet(blocked),
		allowPrivate: allowPrivate,
	}
	return hv
}

func sliceToSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		trimmed := strings.ToLower(strings.TrimSpace(v))
		if trimmed != "" {
			set[trimmed] = struct{}{}
		}
	}
	return set
}

func (h hostValidator) Validate(host string) error {
	hostname := strings.ToLower(hostWithoutPort(host))
	if hostname == "" {
		return errors.New("host cannot be empty")
	}
	if len(h.allowed) > 0 {
		matched := false
		for allow := range h.allowed {
			if domainMatches(hostname, allow) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("host %s is not in whitelist", hostname)
		}
	}
	if !h.allowPrivate {
		if _, ok := defaultBlockedHosts[hostname]; ok {
			return fmt.Errorf("host %s is blocked", hostname)
		}
		if ip := net.ParseIP(hostname); ip != nil {
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
				return fmt.Errorf("ip %s is not reachable", hostname)
			}
		}
	}
	for block := range h.blocked {
		if domainMatches(hostname, block) {
			return fmt.Errorf("host %s is blocked", hostname)
		}
	}
	return nil
}

func domainMatches(host, domain string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	domain = strings.ToLower(strings.TrimSpace(domain))
	if host == domain {
		return true
	}
	if strings.HasSuffix(host, "."+domain) {
		return true
	}
	return false
}

func extractNonEmptyString(params map[string]interface{}, key string) (string, error) {
	raw, ok := params[key]
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	value, err := stringValue(raw)
	if err != nil {
		return "", fmt.Errorf("%s must be string: %w", key, err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s cannot be empty", key)
	}
	return value, nil
}
