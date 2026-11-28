package http

import (
	"net"
	"strings"
)

// ExtractIP extracts the IP address from a RemoteAddr string (e.g. "192.168.1.1:54321" or "[::1]:54321").
func ExtractIP(remoteAddr string) string {
	ipStr := remoteAddr
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		ipStr = host
	}
	ipStr = strings.Trim(ipStr, "[]")
	return ipStr
}

// IsLoopbackRemoteAddr checks whether remoteAddr originates from a loopback interface.
func IsLoopbackRemoteAddr(remoteAddr string) bool {
	ip := net.ParseIP(ExtractIP(remoteAddr))
	return ip != nil && ip.IsLoopback()
}

// IsTrustedProxy checks whether the requester IP (from remoteAddr) matches any entry in trustedProxies (exact IP or CIDR).
// Loopback entries (127.0.0.1, ::1) in trustedProxies apply ONLY if the requester IP is actually loopback!
func IsTrustedProxy(remoteAddr string, trustedProxies []string) bool {
	clientIPStr := ExtractIP(remoteAddr)
	clientIP := net.ParseIP(clientIPStr)
	if clientIP == nil {
		return false
	}

	for _, proxy := range trustedProxies {
		proxy = strings.TrimSpace(proxy)
		if proxy == "" {
			continue
		}

		// Exact IP match
		if proxy == clientIPStr {
			return true
		}

		// Handle localhost aliases only if requester is actually loopback
		if proxy == "localhost" && clientIP.IsLoopback() {
			return true
		}

		// CIDR range match
		if strings.Contains(proxy, "/") {
			_, cidrNet, err := net.ParseCIDR(proxy)
			if err == nil && cidrNet.Contains(clientIP) {
				return true
			}
		}
	}

	return false
}
