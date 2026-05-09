package security

import (
	"context"
	"fmt"
	"net"
	"time"
)

const ssrfResolveTimeout = 3 * time.Second

// blockedHostnames contains well-known internal hostnames that must never be
// accessed via user-supplied URLs. Package-level to avoid per-call allocation.
var blockedHostnames = map[string]bool{
	"localhost":                true,
	"metadata.google.internal": true,
	"metadata.internal":        true,
	"instance-data":            true,
}

// cloudMetadataIPs contains cloud provider metadata service IPs.
var cloudMetadataIPs = []net.IP{
	net.ParseIP("169.254.169.254"), // AWS/GCP/Azure
	net.ParseIP("169.254.169.253"), // Azure IMDS alternate
}

// SSRFResult holds the validated IPs from a successful SSRF check.
// The caller can use ResolvedIPs to pin the connection (TOCTOU mitigation).
type SSRFResult struct {
	ResolvedIPs []net.IP
}

// CheckSSRF performs a pre-flight DNS resolution of host and returns an error
// if any resolved IP address is private, loopback, link-local, or otherwise
// non-routable. This prevents Server-Side Request Forgery attacks where a
// user-supplied URL resolves to an internal service.
//
// On success, returns an SSRFResult containing the validated IPs that should
// be pinned in the HTTP transport's DialContext to prevent TOCTOU attacks
// (DNS rebinding between validation and connection).
//
// Fail-closed: DNS resolution failure blocks the request.
func CheckSSRF(ctx context.Context, host string) (*SSRFResult, error) {
	if host == "" {
		return nil, fmt.Errorf("ssrf: empty host")
	}

	// Strip port if present.
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}

	// Fast-path: block well-known dangerous hostnames.
	if blockedHostnames[hostname] {
		return nil, fmt.Errorf("ssrf: host %q is blocked", hostname)
	}

	// If it's already an IP literal, check directly.
	if ip := net.ParseIP(hostname); ip != nil {
		if isPrivateIP(ip) {
			return nil, fmt.Errorf("ssrf: ip %s is not routable", ip)
		}
		return &SSRFResult{ResolvedIPs: []net.IP{ip}}, nil
	}

	// Resolve DNS and check all returned IPs.
	resolveCtx, cancel := context.WithTimeout(ctx, ssrfResolveTimeout)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIPAddr(resolveCtx, hostname)
	if err != nil {
		// Fail-closed: DNS failure blocks the request to prevent bypass.
		return nil, fmt.Errorf("ssrf: dns resolution failed for %q: %w", hostname, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("ssrf: no addresses resolved for %q", hostname)
	}

	resolved := make([]net.IP, 0, len(ips))
	for _, ipAddr := range ips {
		if isPrivateIP(ipAddr.IP) {
			return nil, fmt.Errorf("ssrf: host %q resolves to non-routable ip %s", hostname, ipAddr.IP)
		}
		resolved = append(resolved, ipAddr.IP)
	}

	return &SSRFResult{ResolvedIPs: resolved}, nil
}

// isPrivateIP returns true if the IP is loopback, private, link-local,
// multicast, unspecified, or in other reserved ranges that should not
// be accessed via user-supplied URLs.
func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		isCloudMetadata(ip)
}

// isCloudMetadata checks for cloud provider metadata service IPs.
func isCloudMetadata(ip net.IP) bool {
	for _, meta := range cloudMetadataIPs {
		if ip.Equal(meta) {
			return true
		}
	}
	return false
}

// NewSSRFSafeDialer returns a DialContext function that pins connections to the
// pre-validated IPs, preventing DNS rebinding (TOCTOU) attacks.
func NewSSRFSafeDialer(result *SSRFResult, port string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if result == nil || len(result.ResolvedIPs) == 0 {
		return nil
	}
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		var lastErr error
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		for _, ip := range result.ResolvedIPs {
			target := net.JoinHostPort(ip.String(), port)
			conn, err := dialer.DialContext(ctx, network, target)
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, fmt.Errorf("ssrf: all resolved IPs failed: %w", lastErr)
	}
}
