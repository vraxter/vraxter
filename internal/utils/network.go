// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package utils

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
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

// IsLoopbackIP checks if an IP is a local loopback address.
func IsLoopbackIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// ParseAndLookup extracts the host from a URL and resolves it to a list of IP addresses.
// In-memory literal IPs and localhost are parsed directly without network DNS calls.
func ParseAndLookup(urlStr string) ([]net.IP, string, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, "", fmt.Errorf("invalid URL format: %w", err)
	}

	host := u.Hostname()
	if host == "" {
		host = urlStr // Fallback if no scheme was provided
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	// 1. In-memory IP literal check (Zero DNS)
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, host, nil
	}

	// 2. Localhost check (Zero DNS)
	if strings.EqualFold(host, "localhost") || strings.EqualFold(host, "localhost.localdomain") {
		return []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}, host, nil
	}

	// 3. Fallback resolution for non-loopback domains
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, host, fmt.Errorf("DNS resolution failed for host '%s': %w", host, err)
	}

	return ips, host, nil
}

// IsAllowedHost validates whether a destination host is permitted under the given isolation mode.
// In "airgap" mode:
// - External hostnames are blocked in-memory with ZERO DNS queries emitted.
// - Only loopback (127.0.0.1, ::1, localhost) and explicitly whitelisted IPs are allowed.
func IsAllowedHost(rawHost string, mode string, whitelist []string) (bool, error) {
	host := strings.TrimSpace(rawHost)
	if host == "" {
		return false, fmt.Errorf("empty host")
	}

	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	// Fast path: Whitelist check
	for _, w := range whitelist {
		wClean := strings.TrimSpace(w)
		if strings.EqualFold(host, wClean) {
			return true, nil
		}
	}

	// Localhost is always permitted
	if strings.EqualFold(host, "localhost") || strings.EqualFold(host, "localhost.localdomain") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true, nil
	}

	// Check if host is an IP literal
	if ip := net.ParseIP(host); ip != nil {
		if IsPublicIP(ip) {
			return false, fmt.Errorf("public IP %s is blocked under %s isolation perimeter (zero-egress to WAN)", ip.String(), mode)
		}

		switch mode {
		case "airgap", "strict_local", "local_lan":
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
				return true, nil
			}
			return false, fmt.Errorf("IP %s blocked in %s mode", ip.String(), mode)
		default: // "hybrid" or other
			return true, nil
		}
	}

	// Host is a non-IP hostname / FQDN
	switch mode {
	case "airgap", "strict_local", "local_lan":
		// Allow .local, .lan, or internal hostnames (single-label without dots, e.g. "ollama-server")
		lower := strings.ToLower(host)
		if strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".lan") || !strings.Contains(host, ".") {
			ips, err := net.LookupIP(host)
			if err != nil {
				return false, fmt.Errorf("local resolution failed for '%s': %w", host, err)
			}
			for _, ip := range ips {
				if IsPublicIP(ip) {
					return false, fmt.Errorf("host '%s' resolved to public IP %s (blocked in %s)", host, ip.String(), mode)
				}
			}
			return len(ips) > 0, nil
		}
		// DETERMINISTIC ZERO-EGRESS: Never emit DNS queries for external WAN domains!
		return false, fmt.Errorf("DNS resolution blocked: %s perimeter forbids external domain '%s' (zero-egress to WAN)", mode, host)
	default: // "hybrid"
		ips, err := net.LookupIP(host)
		if err != nil {
			return false, fmt.Errorf("DNS resolution failed for '%s': %w", host, err)
		}
		return len(ips) > 0, nil
	}
}

// IsAllowedNetworkWithMode parses a URL and verifies whether the target host is permitted
// under the specified isolation mode without leaking DNS queries in airgap mode.
func IsAllowedNetworkWithMode(urlStr string, mode string, whitelist []string) (bool, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false, fmt.Errorf("invalid URL format: %w", err)
	}

	host := u.Hostname()
	if host == "" {
		host = urlStr
	}

	for _, w := range whitelist {
		if strings.EqualFold(host, w) || strings.EqualFold(urlStr, w) {
			return true, nil
		}
	}

	return IsAllowedHost(host, mode, whitelist)
}

// IsAllowedNetwork provides backward-compatible verification defaulting to strict airgap rules.
func IsAllowedNetwork(urlStr string, whitelist []string) (bool, error) {
	return IsAllowedNetworkWithMode(urlStr, "airgap", whitelist)
}

// NewAirgapDialer constructs a net.DialContext function that enforces zero-egress perimeter
// rules at the socket level. Disallowed dials fail immediately with syscall.EPERM.
func NewAirgapDialer(mode string, whitelist []string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}

		allowed, err := IsAllowedHost(host, mode, whitelist)
		if !allowed || err != nil {
			return nil, &net.OpError{
				Op:     "dial",
				Net:    network,
				Source: nil,
				Addr:   nil,
				Err:    syscall.EPERM,
			}
		}

		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
}

// NewAirgapTransport returns an http.Transport configured with the zero-egress AirgapDialer.
func NewAirgapTransport(mode string, whitelist []string) *http.Transport {
	return &http.Transport{
		DialContext:           NewAirgapDialer(mode, whitelist),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// InstallAirgapPerimeter replaces http.DefaultTransport with the AirgapTransport,
// enforcing zero-egress across all standard HTTP client connections in Vraxter.
func InstallAirgapPerimeter(mode string, whitelist []string) {
	http.DefaultTransport = NewAirgapTransport(mode, whitelist)
}
