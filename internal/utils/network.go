package utils

import (
	"fmt"
	"net"
	"net/url"
)

// IsPublicIP checks if a given IP address routes to the public internet.
// It returns false for private (RFC 1918), loopback, link-local, and multicast addresses.
func IsPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}
	return true
}

// ParseAndLookup extracts the host from a URL and resolves it to a list of IP addresses.
func ParseAndLookup(urlStr string) ([]net.IP, string, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, "", fmt.Errorf("invalid URL format: %w", err)
	}

	host := u.Hostname()
	if host == "" {
		host = urlStr // Fallback if no scheme was provided
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, host, fmt.Errorf("DNS resolution failed for host '%s': %w", host, err)
	}

	return ips, host, nil
}

// IsAllowedNetwork parses a URL, resolves its IPs, and verifies if it is allowed
// under the strict_local policy (must be a private/loopback IP OR explicitly whitelisted).
func IsAllowedNetwork(urlStr string, whitelist []string) (bool, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false, fmt.Errorf("invalid URL format: %w", err)
	}

	host := u.Hostname()
	if host == "" {
		host = urlStr // Fallback if no scheme was provided
	}

	// Fast path: Check if the exact hostname or URL is whitelisted
	for _, w := range whitelist {
		if host == w || urlStr == w {
			return true, nil
		}
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return false, fmt.Errorf("DNS resolution failed for host '%s': %w", host, err)
	}

	// Verify all resolved IPs
	for _, ip := range ips {
		// Check if this specific IP is whitelisted
		isWhitelisted := false
		ipStr := ip.String()
		for _, w := range whitelist {
			if ipStr == w {
				isWhitelisted = true
				break
			}
		}

		if isWhitelisted {
			continue
		}

		if IsPublicIP(ip) {
			return false, fmt.Errorf("host '%s' resolves to public IP %s (strict_local policy blocks internet access)", host, ip.String())
		}
	}

	return true, nil
}
