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
//   - "airgap" / "strict_local": strictly loopback-only (127.0.0.1, ::1, localhost) plus explicit
//     whitelisted_ips (exact host/IP or CIDR). Blocks public WAN IPs, external FQDNs (with zero DNS),
//     unwhitelisted RFC 1918 private LAN subnets, and link-local/IMDS (169.254.0.0/16, fe80::/10).
//   - "local_lan": allows loopback, RFC 1918 private LAN subnets, .local/.lan/single-label internal
//     hostnames, and explicit whitelisted_ips, while blocking public WAN IPs, external WAN FQDNs
//     (with zero DNS), and link-local/IMDS.
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
		case "airgap", "strict_local":
			if parsedIP.IsLoopback() {
				return true, nil
			}
			if IsPublicIP(parsedIP) {
				return false, fmt.Errorf("public IP %s is blocked under %s isolation perimeter (zero-egress to WAN)", parsedIP.String(), normMode)
			}
			if parsedIP.IsPrivate() {
				return false, fmt.Errorf("private LAN IP %s is blocked under strict %s perimeter (loopback and explicit whitelisted_ips only; use local_lan for RFC 1918 subnets)", parsedIP.String(), normMode)
			}
			return false, fmt.Errorf("IP %s blocked in %s mode", parsedIP.String(), normMode)
		case "local_lan":
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
	case "airgap", "strict_local":
		// DETERMINISTIC ZERO-EGRESS: Strict airgap forbids all non-loopback, non-whitelisted hostnames with zero DNS queries.
		return false, fmt.Errorf("DNS resolution blocked: strict %s perimeter permits only loopback and explicit whitelisted_ips (blocked host '%s')", normMode, host)
	case "local_lan":
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

// LookupIPFunc resolves a hostname into IP addresses with context support.
type LookupIPFunc func(ctx context.Context, host string) ([]net.IP, error)

// DefaultLookupIP resolves host using Go's net.DefaultResolver.
func DefaultLookupIP(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		if a.IP != nil {
			ips = append(ips, a.IP)
		}
	}
	return ips, nil
}

// multiLabelPublicSuffixes lists common two-label public suffixes that must never be
// accepted as wildcard bases (e.g., "*.co.uk" or "*.com.ar").
var multiLabelPublicSuffixes = map[string]bool{
	"co.uk":  true,
	"org.uk": true,
	"gov.uk": true,
	"ac.uk":  true,
	"me.uk":  true,
	"net.uk": true,
	"com.ar": true,
	"org.ar": true,
	"gov.ar": true,
	"gob.ar": true,
	"edu.ar": true,
	"com.br": true,
	"org.br": true,
	"gov.br": true,
	"com.au": true,
	"org.au": true,
	"gov.au": true,
	"co.jp":  true,
	"or.jp":  true,
	"go.jp":  true,
	"com.mx": true,
	"org.mx": true,
	"gob.mx": true,
	"com.cn": true,
	"com.es": true,
	"co.in":  true,
	"co.nz":  true,
	"co.za":  true,
	"co.kr":  true,
	"co.il":  true,
}

// ValidateAllowedHostPattern checks that an allowed_hosts entry is structurally safe.
// It rejects global wildcards ("*"), TLD/public-suffix wildcards ("*.com", "*.co.uk"),
// multi-wildcard patterns ("*.*.example.com"), and wildcarded IP addresses.
func ValidateAllowedHostPattern(pattern string) error {
	p := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(pattern), "."))
	if p == "" {
		return fmt.Errorf("empty host pattern")
	}
	if strings.Contains(p, "*") {
		if p == "*" || !strings.HasPrefix(p, "*.") {
			return fmt.Errorf("unsafe wildcard pattern %q: only single leading subdomain wildcard '*.domain.tld' is allowed", pattern)
		}
		base := p[2:]
		if base == "" || strings.Contains(base, "*") || strings.Contains(base, "..") || strings.HasPrefix(base, ".") || strings.HasSuffix(base, ".") {
			return fmt.Errorf("malformed wildcard pattern %q", pattern)
		}
		if net.ParseIP(base) != nil {
			return fmt.Errorf("wildcard pattern %q cannot target an IP address", pattern)
		}
		labels := strings.Split(base, ".")
		if len(labels) < 2 {
			return fmt.Errorf("unsafe wildcard pattern %q: cannot wildcard a top-level domain", pattern)
		}
		if multiLabelPublicSuffixes[base] {
			return fmt.Errorf("unsafe wildcard pattern %q: cannot wildcard public suffix %q", pattern, base)
		}
		for _, lbl := range labels {
			if lbl == "" {
				return fmt.Errorf("malformed wildcard pattern %q: empty label", pattern)
			}
		}
	}
	return nil
}

// MatchAllowedHostPattern matches a host and its resolved IPs against a single allowed_hosts
// pattern supporting exact hostnames/IPs, CIDR ranges (net.ParseCIDR), and safe subdomain
// wildcards ("*.example.com"). Returns an error if the pattern itself is unsafe.
func MatchAllowedHostPattern(pattern, host string, resolvedIPs []net.IP) (bool, error) {
	if err := ValidateAllowedHostPattern(pattern); err != nil {
		return false, err
	}
	p := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(pattern), "."))
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")

	// 1. CIDR block check (matches literal host IP or any resolved IP)
	if strings.Contains(p, "/") {
		_, cidrNet, err := net.ParseCIDR(p)
		if err != nil {
			return false, fmt.Errorf("invalid CIDR pattern %q: %w", pattern, err)
		}
		if literalIP := net.ParseIP(h); literalIP != nil {
			return cidrNet.Contains(literalIP), nil
		}
		if len(resolvedIPs) == 0 {
			return false, nil
		}
		for _, ip := range resolvedIPs {
			if !cidrNet.Contains(ip) {
				return false, nil
			}
		}
		return true, nil
	}

	// 2. Safe subdomain wildcard check ("*.example.com")
	if strings.HasPrefix(p, "*.") {
		base := p[2:]
		suffix := "." + base
		if !strings.HasSuffix(h, suffix) {
			return false, nil
		}
		prefix := strings.TrimSuffix(h, suffix)
		if prefix == "" || strings.HasPrefix(prefix, ".") || strings.HasSuffix(prefix, ".") || strings.Contains(prefix, "..") {
			return false, nil
		}
		return true, nil
	}

	// 3. Exact hostname or IP literal match
	if h == p {
		return true, nil
	}
	if pIP := net.ParseIP(p); pIP != nil {
		if hIP := net.ParseIP(h); hIP != nil && pIP.Equal(hIP) {
			return true, nil
		}
		if len(resolvedIPs) > 0 {
			for _, ip := range resolvedIPs {
				if !pIP.Equal(ip) {
					return false, nil
				}
			}
			return true, nil
		}
	}
	return false, nil
}

// ResolveAndValidateHost resolves rawHost at most once using lookupFn (defaulting to
// DefaultLookupIP), enforces the active isolation mode without emitting external DNS
// queries in airgap/local_lan modes, and returns the validated IP addresses for pinning.
func ResolveAndValidateHost(ctx context.Context, rawHost, mode string, whitelist []string, lookupFn LookupIPFunc) ([]net.IP, error) {
	if lookupFn == nil {
		lookupFn = DefaultLookupIP
	}

	host := strings.TrimSpace(rawHost)
	if host == "" {
		return nil, fmt.Errorf("empty host")
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	host = strings.TrimSuffix(host, ".")

	parsedIP := net.ParseIP(host)
	normMode := strings.ToLower(strings.TrimSpace(mode))

	// 1. Literal IP path (zero DNS)
	if parsedIP != nil {
		if isWhitelistedHostOrIP(host, parsedIP, whitelist) {
			return []net.IP{parsedIP}, nil
		}
		if IsLinkLocalIP(parsedIP) {
			return nil, fmt.Errorf("link-local / IMDS IP %s is blocked under %s perimeter", parsedIP.String(), normMode)
		}
		if parsedIP.IsUnspecified() {
			return nil, fmt.Errorf("unspecified IP %s is blocked", parsedIP.String())
		}
		switch normMode {
		case "airgap", "strict_local":
			if parsedIP.IsLoopback() {
				return []net.IP{parsedIP}, nil
			}
			if IsPublicIP(parsedIP) {
				return nil, fmt.Errorf("public IP %s is blocked under %s isolation perimeter (zero-egress to WAN)", parsedIP.String(), normMode)
			}
			if parsedIP.IsPrivate() {
				return nil, fmt.Errorf("private LAN IP %s is blocked under strict %s perimeter (loopback and explicit whitelisted_ips only; use local_lan for RFC 1918 subnets)", parsedIP.String(), normMode)
			}
			return nil, fmt.Errorf("IP %s blocked in %s mode", parsedIP.String(), normMode)
		case "local_lan":
			if IsPublicIP(parsedIP) {
				return nil, fmt.Errorf("public IP %s is blocked under %s isolation perimeter (zero-egress to WAN)", parsedIP.String(), normMode)
			}
			if parsedIP.IsLoopback() || parsedIP.IsPrivate() {
				return []net.IP{parsedIP}, nil
			}
			return nil, fmt.Errorf("IP %s blocked in %s mode", parsedIP.String(), normMode)
		default:
			return []net.IP{parsedIP}, nil
		}
	}

	// 2. Localhost fast path (zero DNS)
	if strings.EqualFold(host, "localhost") || strings.EqualFold(host, "localhost.localdomain") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}, nil
	}

	// 3. Explicit hostname whitelist (still requires single resolution to pin IP)
	explicitlyWhitelistedHost := isWhitelistedHostOrIP(host, nil, whitelist)

	// 4. Non-IP hostname / FQDN under active isolation mode
	switch normMode {
	case "airgap", "strict_local":
		if !explicitlyWhitelistedHost {
			// Deterministic zero-egress: strict airgap permits only loopback and explicit whitelisted_ips.
			return nil, fmt.Errorf("DNS resolution blocked: strict %s perimeter permits only loopback and explicit whitelisted_ips (blocked host '%s')", normMode, host)
		}
		ips, err := lookupFn(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("whitelisted host resolution failed for '%s': %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("DNS resolution returned no addresses for '%s'", host)
		}
		for _, ip := range ips {
			if isWhitelistedHostOrIP(ip.String(), ip, whitelist) {
				continue
			}
			if IsLinkLocalIP(ip) {
				return nil, fmt.Errorf("host '%s' resolved to link-local / IMDS IP %s (blocked in %s)", host, ip.String(), normMode)
			}
			if ip.IsUnspecified() {
				return nil, fmt.Errorf("host '%s' resolved to unspecified IP %s (blocked)", host, ip.String())
			}
		}
		return ips, nil

	case "local_lan":
		lower := strings.ToLower(host)
		isLocalName := strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".lan") || !strings.Contains(host, ".")
		if !isLocalName && !explicitlyWhitelistedHost {
			// Deterministic zero-egress: never emit DNS queries for external WAN domains.
			return nil, fmt.Errorf("DNS resolution blocked: %s perimeter forbids external domain '%s' (zero-egress to WAN)", normMode, host)
		}
		ips, err := lookupFn(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("local resolution failed for '%s': %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("DNS resolution returned no addresses for '%s'", host)
		}
		for _, ip := range ips {
			if isWhitelistedHostOrIP(ip.String(), ip, whitelist) {
				continue
			}
			if IsLinkLocalIP(ip) {
				return nil, fmt.Errorf("host '%s' resolved to link-local / IMDS IP %s (blocked in %s)", host, ip.String(), normMode)
			}
			if ip.IsUnspecified() {
				return nil, fmt.Errorf("host '%s' resolved to unspecified IP %s (blocked)", host, ip.String())
			}
			if !explicitlyWhitelistedHost && IsPublicIP(ip) {
				return nil, fmt.Errorf("host '%s' resolved to public IP %s (blocked in %s)", host, ip.String(), normMode)
			}
		}
		return ips, nil

	default: // "hybrid"
		ips, err := lookupFn(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("DNS resolution failed for '%s': %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("DNS resolution returned no addresses for '%s'", host)
		}
		for _, ip := range ips {
			if isWhitelistedHostOrIP(ip.String(), ip, whitelist) {
				continue
			}
			if IsLinkLocalIP(ip) {
				return nil, fmt.Errorf("host '%s' resolved to link-local / IMDS IP %s (blocked)", host, ip.String())
			}
			if ip.IsUnspecified() {
				return nil, fmt.Errorf("host '%s' resolved to unspecified IP %s (blocked)", host, ip.String())
			}
		}
		return ips, nil
	}
}

// NewAirgapDialer constructs a net.DialContext function that enforces zero-egress perimeter
// rules at the socket level with single-lookup IP pinning (anti-TOCTOU) and strict DDIL timeouts.
// Disallowed dials fail immediately with syscall.EPERM.
func NewAirgapDialer(mode string, whitelist []string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{
		Timeout:   DDILDialTimeout,
		KeepAlive: DDILKeepAlive,
	}
	wlCopy := append([]string(nil), whitelist...)

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, &net.OpError{
				Op:  "dial",
				Net: network,
				Err: syscall.EPERM,
			}
		}

		ips, err := ResolveAndValidateHost(ctx, host, mode, wlCopy, nil)
		if err != nil || len(ips) == 0 {
			return nil, &net.OpError{
				Op:     "dial",
				Net:    network,
				Source: nil,
				Addr:   nil,
				Err:    syscall.EPERM,
			}
		}

		// Dial the validated IP directly so net.Dialer never performs a second DNS lookup (anti-TOCTOU).
		var lastErr error
		for _, ip := range ips {
			conn, dialErr := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
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
