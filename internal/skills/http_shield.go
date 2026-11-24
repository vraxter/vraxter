// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package skills

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tetratelabs/wazero/api"
	"github.com/vraxter/vraxter/internal/utils"
	"github.com/vraxter/vraxter/pkg/types"
)

const (
	dryRunCtxKey      ctxKey = "vrax_dry_run"
	httpLookupCtxKey  ctxKey = "vrax_http_lookup_fn"
	httpDialCtxKey    ctxKey = "vrax_http_dial_fn"
)

// dialContextFunc matches http.Transport.DialContext for deterministic testing of pinned dials.
type dialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// resolvedHTTPTarget holds the single-lookup, policy-validated destination for one HTTP hop.
type resolvedHTTPTarget struct {
	RawURL     string
	Scheme     string
	Hostname   string
	Port       int
	PinnedIP   net.IP
	AllIPs     []net.IP
	IsExternal bool
}

func isExternalFQDN(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	if h == "" || net.ParseIP(h) != nil {
		return false
	}
	if h == "localhost" || h == "localhost.localdomain" || strings.HasSuffix(h, ".localhost") {
		return false
	}
	if strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".lan") || !strings.Contains(h, ".") {
		return false
	}
	return true
}

func isIPExplicitlyAllowedByManifest(ip net.IP, manifest types.SkillManifest) bool {
	if ip == nil || manifest.ManifestV1 == nil || manifest.ManifestV1.Capabilities == nil || manifest.ManifestV1.Capabilities.Network == nil {
		return false
	}
	for _, entry := range manifest.ManifestV1.Capabilities.Network.AllowedHosts {
		e := strings.TrimSpace(entry)
		if e == "" {
			continue
		}
		if parsed := net.ParseIP(e); parsed != nil && parsed.Equal(ip) {
			return true
		}
		if _, cidr, err := net.ParseCIDR(e); err == nil && cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func isIPExplicitlyWhitelisted(ip net.IP, whitelist []string) bool {
	if ip == nil {
		return false
	}
	for _, entry := range whitelist {
		e := strings.TrimSpace(entry)
		if e == "" {
			continue
		}
		if parsed := net.ParseIP(e); parsed != nil && parsed.Equal(ip) {
			return true
		}
		if _, cidr, err := net.ParseCIDR(e); err == nil && cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func selectPinnedIP(ips []net.IP) net.IP {
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4
		}
	}
	if len(ips) > 0 {
		return ips[0]
	}
	return nil
}

func parseTargetPort(u *url.URL, scheme string) (int, error) {
	if portStr := u.Port(); portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return 0, fmt.Errorf("invalid port %q", portStr)
		}
		return port, nil
	}
	if scheme == "https" {
		return 443, nil
	}
	return 80, nil
}

func validateTargetPort(port int, manifest types.SkillManifest) error {
	var allowedPorts []int
	if manifest.ManifestV1 != nil && manifest.ManifestV1.Capabilities != nil && manifest.ManifestV1.Capabilities.Network != nil {
		allowedPorts = manifest.ManifestV1.Capabilities.Network.AllowedPorts
	}

	if len(allowedPorts) > 0 {
		for _, p := range allowedPorts {
			if p == port {
				return nil
			}
		}
		return fmt.Errorf("port %d is not in manifest allowed_ports %v", port, allowedPorts)
	}

	// Secure default: only standard HTTP (80) and HTTPS (443) ports when allowed_ports is omitted.
	if port != 80 && port != 443 {
		return fmt.Errorf("port %d is blocked by default (only ports 80 and 443 are permitted unless declared in allowed_ports)", port)
	}
	return nil
}

// resolveAndValidateHTTPGetTarget validates scheme, port, host patterns (exact, safe wildcard,
// CIDR), and network isolation perimeter while performing at most ONE DNS lookup so the caller
// can pin the resulting IP directly in DialContext (eliminating DNS TOCTOU).
func resolveAndValidateHTTPGetTarget(
	ctx context.Context,
	rawURL string,
	manifest types.SkillManifest,
	policy networkPolicy,
	lookupFn utils.LookupIPFunc,
	isRedirect bool,
	prevWasExternal bool,
) (*resolvedHTTPTarget, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid URL %q", rawURL)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("disallowed URL scheme %q", u.Scheme)
	}

	port, err := parseTargetPort(u, scheme)
	if err != nil {
		return nil, err
	}

	if err := validateTargetPort(port, manifest); err != nil {
		return nil, err
	}

	// Enforce manifest allowed_schemes if declared
	var netCap *types.NetworkSpec
	if manifest.ManifestV1 != nil && manifest.ManifestV1.Capabilities != nil {
		netCap = manifest.ManifestV1.Capabilities.Network
	}
	if netCap != nil && len(netCap.AllowedSchemes) > 0 {
		schemeOK := false
		for _, allowedScheme := range netCap.AllowedSchemes {
			if strings.EqualFold(strings.TrimSpace(allowedScheme), scheme) {
				schemeOK = true
				break
			}
		}
		if !schemeOK {
			return nil, fmt.Errorf("scheme %q not in manifest allowed_schemes", scheme)
		}
	}

	hostname := u.Hostname()

	// Pre-validate allowed_hosts wildcard syntax BEFORE issuing any DNS lookup
	if netCap != nil && len(netCap.AllowedHosts) > 0 {
		for _, pat := range netCap.AllowedHosts {
			if err := utils.ValidateAllowedHostPattern(pat); err != nil {
				return nil, fmt.Errorf("invalid manifest allowed_hosts entry: %w", err)
			}
		}
	}

	// Single DNS resolution + isolation perimeter verification (zero DNS for WAN domains in airgap/local_lan)
	resolvedIPs, err := utils.ResolveAndValidateHost(ctx, hostname, policy.isolationMode, policy.whitelistedIPs, lookupFn)
	if err != nil {
		return nil, fmt.Errorf("blocked by %s network shield: %w", policy.isolationMode, err)
	}
	if len(resolvedIPs) == 0 {
		return nil, fmt.Errorf("host %q resolved to zero addresses", hostname)
	}

	extFQDN := isExternalFQDN(hostname)
	allPublic := true
	for _, ip := range resolvedIPs {
		if !utils.IsPublicIP(ip) {
			allPublic = false
		}
		// Always block link-local / Cloud IMDS (169.254.0.0/16, fe80::/10) unless explicitly whitelisted
		if utils.IsLinkLocalIP(ip) && !isIPExplicitlyWhitelisted(ip, policy.whitelistedIPs) {
			return nil, fmt.Errorf("resolved IP %s is in link-local/IMDS range (blocked)", ip.String())
		}
		// Anti-rebinding: an external FQDN must never resolve to loopback or private RFC 1918
		// addresses unless explicitly authorized via whitelisted_ips or an IP/CIDR entry in allowed_hosts.
		if extFQDN && (ip.IsLoopback() || ip.IsPrivate()) {
			if !isIPExplicitlyWhitelisted(ip, policy.whitelistedIPs) && !isIPExplicitlyAllowedByManifest(ip, manifest) {
				return nil, fmt.Errorf("DNS rebinding shield: external host %q resolved to unauthorized private/loopback IP %s", hostname, ip.String())
			}
		}
		// Anti-SSRF redirect guard: redirects originating from an external/public hop must never
		// pivot to loopback or private RFC 1918 addresses unless explicitly authorized.
		if isRedirect && prevWasExternal && (ip.IsLoopback() || ip.IsPrivate()) {
			if !isIPExplicitlyWhitelisted(ip, policy.whitelistedIPs) && !isIPExplicitlyAllowedByManifest(ip, manifest) {
				return nil, fmt.Errorf("redirect SSRF shield: external redirect target %q resolved to unauthorized private/loopback IP %s", hostname, ip.String())
			}
		}
	}

	// Enforce manifest allowed_hosts (exact host/IP, safe subdomain wildcard *.domain.tld, or CIDR)
	if netCap != nil && len(netCap.AllowedHosts) > 0 {
		hostOK := false
		for _, pat := range netCap.AllowedHosts {
			matched, matchErr := utils.MatchAllowedHostPattern(pat, hostname, resolvedIPs)
			if matchErr != nil {
				return nil, fmt.Errorf("invalid manifest allowed_hosts entry: %w", matchErr)
			}
			if matched {
				hostOK = true
				break
			}
		}
		if !hostOK {
			return nil, fmt.Errorf("host %q (resolved %v) not matched by manifest allowed_hosts", hostname, resolvedIPs)
		}
	}

	pinned := selectPinnedIP(resolvedIPs)
	if pinned == nil {
		return nil, fmt.Errorf("no valid IP available to pin for %q", hostname)
	}

	return &resolvedHTTPTarget{
		RawURL:     rawURL,
		Scheme:     scheme,
		Hostname:   hostname,
		Port:       port,
		PinnedIP:   pinned,
		AllIPs:     resolvedIPs,
		IsExternal: extFQDN || allPublic,
	}, nil
}

// validateHTTPGetTarget provides single-call target validation for callers and tests.
func validateHTTPGetTarget(rawURL string, manifest types.SkillManifest, policy networkPolicy) error {
	_, err := resolveAndValidateHTTPGetTarget(context.Background(), rawURL, manifest, policy, nil, false, false)
	return err
}

func isHTTPRedirectStatus(code int) bool {
	switch code {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func executePinnedHTTPGet(
	ctx context.Context,
	rawURL string,
	manifest types.SkillManifest,
	policy networkPolicy,
	readLimit int64,
	lookupFn utils.LookupIPFunc,
	customDial dialContextFunc,
) ([]byte, error) {
	currentURL := rawURL
	prevWasExternal := false

	for hop := 0; hop <= maxHTTPRedirects; hop++ {
		target, err := resolveAndValidateHTTPGetTarget(ctx, currentURL, manifest, policy, lookupFn, hop > 0, prevWasExternal)
		if err != nil {
			return nil, err
		}

		pinnedAddr := net.JoinHostPort(target.PinnedIP.String(), strconv.Itoa(target.Port))
		baseDialer := &net.Dialer{
			Timeout:   utils.DDILDialTimeout,
			KeepAlive: utils.DDILKeepAlive,
		}

		transport := &http.Transport{
			DialContext: func(dialCtx context.Context, network, _ string) (net.Conn, error) {
				// Always dial the pre-validated literal IP so no second DNS resolution can occur.
				if customDial != nil {
					return customDial(dialCtx, network, pinnedAddr)
				}
				return baseDialer.DialContext(dialCtx, network, pinnedAddr)
			},
			TLSClientConfig: &tls.Config{
				ServerName: target.Hostname,
				MinVersion: tls.VersionTLS12,
			},
			DisableKeepAlives:     true,
			ForceAttemptHTTP2:     true,
			IdleConnTimeout:       utils.DDILIdleConnTimeout,
			TLSHandshakeTimeout:   utils.DDILTLSHandshakeTimeout,
			ResponseHeaderTimeout: utils.DDILResponseHeaderTimeout,
			ExpectContinueTimeout: 1 * time.Second,
		}

		client := &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				// Handle redirects manually so each hop performs fresh single-DNS resolution and IP pinning.
				return http.ErrUseLastResponse
			},
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentURL, nil)
		if err != nil {
			transport.CloseIdleConnections()
			return nil, err
		}

		resp, err := client.Do(req)
		if err != nil {
			transport.CloseIdleConnections()
			return nil, err
		}

		if isHTTPRedirectStatus(resp.StatusCode) {
			loc := strings.TrimSpace(resp.Header.Get("Location"))
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			transport.CloseIdleConnections()

			if loc == "" {
				return nil, fmt.Errorf("redirect response missing Location header")
			}
			if hop >= maxHTTPRedirects {
				return nil, fmt.Errorf("too many redirects (max %d)", maxHTTPRedirects)
			}
			baseU, parseErr := url.Parse(currentURL)
			if parseErr != nil {
				return nil, parseErr
			}
			nextU, parseErr := baseU.Parse(loc)
			if parseErr != nil {
				return nil, fmt.Errorf("invalid redirect location %q: %w", loc, parseErr)
			}
			prevWasExternal = prevWasExternal || target.IsExternal
			currentURL = nextU.String()
			continue
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, readLimit))
		_ = resp.Body.Close()
		transport.CloseIdleConnections()
		if readErr != nil {
			return nil, readErr
		}
		return body, nil
	}

	return nil, fmt.Errorf("too many redirects (max %d)", maxHTTPRedirects)
}

func httpGetFn(ctx context.Context, mod api.Module, urlPtr, urlLen, outPtr, outMax uint32) uint32 {
	// Never emit outbound network calls during skill compilation dry-run verification.
	if dryRun, ok := ctx.Value(dryRunCtxKey).(bool); ok && dryRun {
		return 0
	}

	manifestVal := ctx.Value(manifestKey)
	if manifestVal == nil {
		// Secure by default: abort if manifest is not propagated.
		return 0
	}
	manifest, ok := manifestVal.(types.SkillManifest)
	if !ok {
		return 0
	}

	if !manifestHasNetwork(manifest) {
		fmt.Printf("\n[SHIELD SECURITY ALERT] Skill '%s' tried to make an outbound HTTP request but lacks the 'network' permission capability. Blocked.\n", manifest.ID)
		return 0
	}

	if outMax == 0 {
		return 0
	}

	urlBytes, ok := mod.Memory().Read(urlPtr, urlLen)
	if !ok {
		return 0
	}
	rawURL := string(urlBytes)

	policy := networkPolicy{isolationMode: "airgap"}
	if pv, ok := ctx.Value(networkPolicyKey).(networkPolicy); ok && pv.isolationMode != "" {
		policy = pv
	}

	var lookupFn utils.LookupIPFunc
	if lf, ok := ctx.Value(httpLookupCtxKey).(utils.LookupIPFunc); ok {
		lookupFn = lf
	}
	var customDial dialContextFunc
	if df, ok := ctx.Value(httpDialCtxKey).(dialContextFunc); ok {
		customDial = df
	}

	readLimit := int64(outMax)
	if readLimit > MaxHTTPGetBytes {
		readLimit = MaxHTTPGetBytes
	}

	body, err := executePinnedHTTPGet(ctx, rawURL, manifest, policy, readLimit, lookupFn, customDial)
	if err != nil {
		fmt.Printf("\n[SHIELD SECURITY ALERT] Skill '%s' outbound HTTP request to '%s' blocked: %v\n", manifest.ID, rawURL, err)
		return 0
	}

	if uint32(len(body)) > outMax {
		body = body[:outMax]
	}
	if !mod.Memory().Write(outPtr, body) {
		return 0
	}
	return uint32(len(body))
}
