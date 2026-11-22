package http

import (
	"testing"
)

func TestIsTrustedProxy_VulnerabilityRegression(t *testing.T) {
	trustedProxies := []string{"127.0.0.1", "::1"}

	// 1. Request from non-loopback address MUST NOT be trusted, even if trustedProxies contains 127.0.0.1
	untrustedRemoteAddr := "192.168.1.100:54321"
	if IsTrustedProxy(untrustedRemoteAddr, trustedProxies) {
		t.Errorf("CRITICAL VULNERABILITY RECURRENCE: Non-loopback IP %s was trusted because trustedProxies contained 127.0.0.1!", untrustedRemoteAddr)
	}

	// 2. Request from loopback address MUST be trusted when 127.0.0.1 is in trustedProxies
	loopbackIPv4 := "127.0.0.1:54321"
	if !IsTrustedProxy(loopbackIPv4, trustedProxies) {
		t.Errorf("Expected loopback IPv4 %s to be trusted", loopbackIPv4)
	}

	loopbackIPv6 := "[::1]:54321"
	if !IsTrustedProxy(loopbackIPv6, trustedProxies) {
		t.Errorf("Expected loopback IPv6 %s to be trusted", loopbackIPv6)
	}

	// 3. Request from non-loopback IP matching CIDR range
	cidrProxies := []string{"10.0.0.0/8"}
	trustedCIDRIP := "10.1.2.3:12345"
	if !IsTrustedProxy(trustedCIDRIP, cidrProxies) {
		t.Errorf("Expected CIDR IP %s to be trusted", trustedCIDRIP)
	}

	untrustedCIDRIP := "192.168.1.1:12345"
	if IsTrustedProxy(untrustedCIDRIP, cidrProxies) {
		t.Errorf("Expected IP outside CIDR %s to be untrusted", untrustedCIDRIP)
	}

	// 4. Malformed remote address handling
	if IsTrustedProxy("invalid_remote_addr", trustedProxies) {
		t.Errorf("Malformed remote address must be untrusted")
	}
}
