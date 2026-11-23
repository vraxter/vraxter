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
	"sync"
	"syscall"
	"time"
)

const (
	// DDIL network timing constants to prevent hung sockets on denied/intermittent links.
	DDILDialTimeout           = 10 * time.Second
	DDILKeepAlive             = 30 * time.Second
	DDILTLSHandshakeTimeout   = 10 * time.Second
	DDILResponseHeaderTimeout = 30 * time.Second
	DDILIdleConnTimeout       = 90 * time.Second
)

var (
	activeDialerMu sync.RWMutex
	activeDialer   func(ctx context.Context, network, addr string) (net.Conn, error)
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

// IsLinkLocalIP reports whether ip falls in link-local ranges (e.g. 169.254.0.0/16 IMDS or fe80::/10).
func IsLinkLocalIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// IsLoopbackIP checks if an IP is a local loopback address.
func IsLoopbackIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// isWhitelistedHostOrIP evaluates exact hostname/IP matches AND real CIDR subnet matches (net.ParseCIDR).
func isWhitelistedHostOrIP(host string, parsedIP net.IP, whitelist []string) bool {
	for _, w := range whitelist {
		wClean := strings.TrimSpace(w)
		if wClean == "" {
			continue
		}
		if strings.EqualFold(host, wClean) {
			return true
		}
		if parsedIP != nil {
			if _, cidrNet, err := net.ParseCIDR(wClean); err == nil && cidrNet.Contains(parsedIP) {
				return true
			}
		}
	}
	return false
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
//   - "airgap" / "strict_local" / "local_lan": blocks public IPs, external WAN FQDNs (with zero DNS),
//     and link-local/IMDS (169.254.0.0/16, fe80::/10) unless explicitly whitelisted (exact or CIDR).
//   - "hybrid": permits public IPs and WAN hostnames while still blocking link-local/IMDS by default
//     unless explicitly whitelisted.
func IsAllowedHost(rawHost string, mode string, whitelist []string) (bool, error) {
	host := strings.TrimSpace(rawHost)
	if host == "" {
		return false, fmt.Errorf("empty host")
	}

	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	// Strip IPv6 brackets if passed bare like "[::1]"
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")

	parsedIP := net.ParseIP(host)

	// Fast path: Explicit whitelist check (exact host/IP or CIDR block)
	if isWhitelistedHostOrIP(host, parsedIP, whitelist) {
		return true, nil
	}

	// Localhost is always permitted
	if strings.EqualFold(host, "localhost") || strings.EqualFold(host, "localhost.localdomain") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true, nil
	}

	normMode := strings.ToLower(strings.TrimSpace(mode))

	// Check if host is an IP literal
	if parsedIP != nil {
		// Always block link-local / Cloud IMDS (169.254.0.0/16, fe80::/10) unless explicitly whitelisted above
		if IsLinkLocalIP(parsedIP) {
			return false, fmt.Errorf("link-local / IMDS IP %s is blocked under %s perimeter", parsedIP.String(), normMode)
		}

		switch normMode {
		case "airgap", "strict_local", "local_lan":
			if IsPublicIP(parsedIP) {
				return false, fmt.Errorf("public IP %s is blocked under %s isolation perimeter (zero-egress to WAN)", parsedIP.String(), normMode)
			}
			if parsedIP.IsLoopback() || parsedIP.IsPrivate() {
				return true, nil
			}
			return false, fmt.Errorf("IP %s blocked in %s mode", parsedIP.String(), normMode)
		default: // "hybrid" or unrestricted: public, private, and loopback literal IPs are permitted
			if parsedIP.IsUnspecified() {
				return false, fmt.Errorf("unspecified IP %s is blocked", parsedIP.String())
			}
			return true, nil
		}
	}

	// Host is a non-IP hostname / FQDN
	switch normMode {
	case "airgap", "strict_local", "local_lan":
		// Allow .local, .lan, or internal single-label hostnames only if they resolve solely to allowed private/loopback IPs
		lower := strings.ToLower(host)
		if strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".lan") || !strings.Contains(host, ".") {
			ips, err := net.LookupIP(host)
			if err != nil {
				return false, fmt.Errorf("local resolution failed for '%s': %w", host, err)
			}
			for _, ip := range ips {
				if isWhitelistedHostOrIP(ip.String(), ip, whitelist) {
					continue
				}
				if IsLinkLocalIP(ip) {
					return false, fmt.Errorf("host '%s' resolved to link-local / IMDS IP %s (blocked in %s)", host, ip.String(), normMode)
				}
				if IsPublicIP(ip) {
					return false, fmt.Errorf("host '%s' resolved to public IP %s (blocked in %s)", host, ip.String(), normMode)
				}
			}
			return len(ips) > 0, nil
		}
		// DETERMINISTIC ZERO-EGRESS: Never emit DNS queries for external WAN domains!
		return false, fmt.Errorf("DNS resolution blocked: %s perimeter forbids external domain '%s' (zero-egress to WAN)", normMode, host)
	default: // "hybrid"
		ips, err := net.LookupIP(host)
		if err != nil {
			return false, fmt.Errorf("DNS resolution failed for '%s': %w", host, err)
		}
		for _, ip := range ips {
			if IsLinkLocalIP(ip) && !isWhitelistedHostOrIP(ip.String(), ip, whitelist) {
				return false, fmt.Errorf("host '%s' resolved to link-local / IMDS IP %s (blocked)", host, ip.String())
			}
		}
		return len(ips) > 0, nil
	}
}

// IsAllowedNetworkWithMode parses a URL and verifies whether the target host is permitted
// under the specified isolation mode without leaking DNS queries in restricted modes.
func IsAllowedNetworkWithMode(urlStr string, mode string, whitelist []string) (bool, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false, fmt.Errorf("invalid URL format: %w", err)
	}

	host := u.Hostname()
	if host == "" {
		host = urlStr
	}

	parsedIP := net.ParseIP(host)
	if isWhitelistedHostOrIP(host, parsedIP, whitelist) || isWhitelistedHostOrIP(urlStr, nil, whitelist) {
		return true, nil
	}

	return IsAllowedHost(host, mode, whitelist)
}

// IsAllowedNetwork provides backward-compatible verification defaulting to strict airgap rules.
func IsAllowedNetwork(urlStr string, whitelist []string) (bool, error) {
	return IsAllowedNetworkWithMode(urlStr, "airgap", whitelist)
}

// NewAirgapDialer constructs a net.DialContext function that enforces zero-egress perimeter
// rules at the socket level with strict DDIL connect/keepalive timeouts.
// Disallowed dials fail immediately with syscall.EPERM.
func NewAirgapDialer(mode string, whitelist []string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{
		Timeout:   DDILDialTimeout,
		KeepAlive: DDILKeepAlive,
	}
	wlCopy := append([]string(nil), whitelist...)

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}

		allowed, err := IsAllowedHost(host, mode, wlCopy)
		if !allowed || err != nil {
			return nil, &net.OpError{
				Op:     "dial",
				Net:    network,
				Source: nil,
				Addr:   nil,
				Err:    syscall.EPERM,
			}
		}

		return d.DialContext(ctx, network, addr)
	}
}

// NewAirgapTransport returns an http.Transport configured with the AirgapDialer and strict DDIL timeouts.
func NewAirgapTransport(mode string, whitelist []string) *http.Transport {
	return &http.Transport{
		DialContext:           NewAirgapDialer(mode, whitelist),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       DDILIdleConnTimeout,
		TLSHandshakeTimeout:   DDILTLSHandshakeTimeout,
		ResponseHeaderTimeout: DDILResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// InstallAirgapPerimeter replaces http.DefaultTransport with the AirgapTransport and registers
// the active dialer for all DDIL HTTP clients across Vraxter.
func InstallAirgapPerimeter(mode string, whitelist []string) {
	dialer := NewAirgapDialer(mode, whitelist)
	activeDialerMu.Lock()
	activeDialer = dialer
	activeDialerMu.Unlock()

	http.DefaultTransport = &http.Transport{
		DialContext:           dialer,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       DDILIdleConnTimeout,
		TLSHandshakeTimeout:   DDILTLSHandshakeTimeout,
		ResponseHeaderTimeout: DDILResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// NewDDILHTTPClient returns an *http.Client hardened for intermittent/denied (DDIL) links:
// strict connect (10s), TLS handshake (10s), response header (30s), and idle connection (90s)
// timeouts, while honoring the active AirgapPerimeter dialer whenever installed.
func NewDDILHTTPClient() *http.Client {
	fallbackDialer := &net.Dialer{
		Timeout:   DDILDialTimeout,
		KeepAlive: DDILKeepAlive,
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			activeDialerMu.RLock()
			d := activeDialer
			activeDialerMu.RUnlock()
			if d != nil {
				return d(ctx, network, addr)
			}
			return fallbackDialer.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       DDILIdleConnTimeout,
		TLSHandshakeTimeout:   DDILTLSHandshakeTimeout,
		ResponseHeaderTimeout: DDILResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{Transport: transport}
}
