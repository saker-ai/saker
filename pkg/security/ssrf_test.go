package security

import (
	"context"
	"net"
	"testing"
)

func TestCheckSSRF_BlocksLocalhost(t *testing.T) {
	t.Parallel()
	_, err := CheckSSRF(context.Background(), "localhost")
	if err == nil {
		t.Error("expected localhost to be blocked")
	}
}

func TestCheckSSRF_BlocksLoopbackIP(t *testing.T) {
	t.Parallel()
	_, err := CheckSSRF(context.Background(), "127.0.0.1")
	if err == nil {
		t.Error("expected 127.0.0.1 to be blocked")
	}
}

func TestCheckSSRF_BlocksMetadataIP(t *testing.T) {
	t.Parallel()
	_, err := CheckSSRF(context.Background(), "169.254.169.254")
	if err == nil {
		t.Error("expected metadata IP to be blocked")
	}
}

func TestCheckSSRF_BlocksMetadataHostname(t *testing.T) {
	t.Parallel()
	_, err := CheckSSRF(context.Background(), "metadata.google.internal")
	if err == nil {
		t.Error("expected metadata hostname to be blocked")
	}
}

func TestCheckSSRF_AllowsPublicIP(t *testing.T) {
	t.Parallel()
	result, err := CheckSSRF(context.Background(), "8.8.8.8")
	if err != nil {
		t.Errorf("expected public IP to be allowed: %v", err)
	}
	if result == nil || len(result.ResolvedIPs) == 0 {
		t.Error("expected resolved IPs in result")
	}
}

func TestCheckSSRF_BlocksPrivateIP(t *testing.T) {
	t.Parallel()
	tests := []string{"10.0.0.1", "192.168.1.1", "172.16.0.1"}
	for _, ip := range tests {
		_, err := CheckSSRF(context.Background(), ip)
		if err == nil {
			t.Errorf("expected private IP %s to be blocked", ip)
		}
	}
}

func TestCheckSSRF_EmptyHost(t *testing.T) {
	t.Parallel()
	_, err := CheckSSRF(context.Background(), "")
	if err == nil {
		t.Error("expected empty host to be blocked")
	}
}

func TestCheckSSRF_HostWithPort(t *testing.T) {
	t.Parallel()
	_, err := CheckSSRF(context.Background(), "127.0.0.1:8080")
	if err == nil {
		t.Error("expected loopback with port to be blocked")
	}
}

func TestCheckSSRF_BlocksIPv6Loopback(t *testing.T) {
	t.Parallel()
	_, err := CheckSSRF(context.Background(), "::1")
	if err == nil {
		t.Error("expected IPv6 loopback to be blocked")
	}
}

func TestCheckSSRF_FailClosed_DNSError(t *testing.T) {
	t.Parallel()
	// A hostname that will fail DNS resolution should be blocked (fail-closed).
	_, err := CheckSSRF(context.Background(), "this-host-definitely-does-not-exist-zzz.invalid")
	if err == nil {
		t.Error("expected DNS resolution failure to block (fail-closed)")
	}
}

func TestIsPrivateIP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"192.168.0.1", true},
		{"172.16.0.1", true},
		{"169.254.169.254", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"::1", true},
		{"0.0.0.0", true},
	}
	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		got := isPrivateIP(ip)
		if got != tt.want {
			t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

func TestSSRFResult_ResolvedIPs(t *testing.T) {
	t.Parallel()
	result, err := CheckSSRF(context.Background(), "8.8.8.8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.ResolvedIPs) != 1 {
		t.Errorf("expected 1 resolved IP, got %d", len(result.ResolvedIPs))
	}
}

func TestNewSSRFSafeDialer_Nil(t *testing.T) {
	t.Parallel()
	dialer := NewSSRFSafeDialer(nil, "443")
	if dialer != nil {
		t.Error("expected nil dialer for nil result")
	}
}
