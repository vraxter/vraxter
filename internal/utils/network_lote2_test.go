// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package utils

import (
	"net/http"
	"testing"
)

func TestIsAllowedHost_BlocksIMDSLinkLocal(t *testing.T) {
	imdsTargets := []string{
		"169.254.169.254",
		"169.254.0.1",
		"fe80::1",
	}

	for _, mode := range []string{"airgap", "strict_local", "local_lan", "hybrid"} {
		for _, target := range imdsTargets {
			allowed, err := IsAllowedHost(target, mode, nil)
			if allowed || err == nil {
				t.Errorf("[%s] expected link-local/IMDS IP %s to be BLOCKED by default, got allowed=%v err=%v", mode, target, allowed, err)
			}
		}
	}

	// Explicit whitelist entry can override if intentionally configured by operator
	allowed, err := IsAllowedHost("169.254.169.254", "airgap", []string{"169.254.169.254"})
	if !allowed || err != nil {
		t.Errorf("expected explicitly whitelisted IP to pass, got allowed=%v err=%v", allowed, err)
	}
}

func TestIsAllowedHost_CIDRWhitelistSupport(t *testing.T) {
	whitelist := []string{"203.0.113.0/24", "198.51.100.128/25"}

	for _, mode := range []string{"airgap", "local_lan"} {
		if ok, err := IsAllowedNetworkWithMode("http://203.0.113.42:8080/v1", mode, whitelist); !ok || err != nil {
			t.Errorf("[%s] expected IP inside CIDR 203.0.113.0/24 to be allowed, got ok=%v err=%v", mode, ok, err)
		}
		if ok, err := IsAllowedHost("198.51.100.200", mode, whitelist); !ok || err != nil {
			t.Errorf("[%s] expected IP inside CIDR 198.51.100.128/25 to be allowed, got ok=%v err=%v", mode, ok, err)
		}
		if ok, _ := IsAllowedHost("203.0.114.1", mode, whitelist); ok {
			t.Errorf("[%s] expected IP outside whitelisted CIDR to be blocked", mode)
		}
		if ok, _ := IsAllowedHost("198.51.100.10", mode, whitelist); ok {
			t.Errorf("[%s] expected IP outside /25 subnet to be blocked", mode)
		}
	}
}

func TestIsAllowedHost_HybridPermitsLiteralPublicIPs(t *testing.T) {
	for _, target := range []string{"8.8.8.8", "1.1.1.1", "http://8.8.8.8:443/v1"} {
		allowed, err := IsAllowedNetworkWithMode(target, "hybrid", nil)
		if !allowed || err != nil {
			t.Errorf("expected hybrid mode to permit literal public IP %s, got allowed=%v err=%v", target, allowed, err)
		}
	}

	// Verify IMDS link-local remains blocked even in hybrid mode
	if allowed, _ := IsAllowedNetworkWithMode("http://169.254.169.254/latest/meta-data/", "hybrid", nil); allowed {
		t.Errorf("expected hybrid mode to still block link-local IMDS 169.254.169.254 by default")
	}
}

func TestDDILTransportAndClient_TimeoutsConfigured(t *testing.T) {
	tr := NewAirgapTransport("airgap", nil)
	if tr.ResponseHeaderTimeout != DDILResponseHeaderTimeout {
		t.Errorf("expected ResponseHeaderTimeout=%v, got %v", DDILResponseHeaderTimeout, tr.ResponseHeaderTimeout)
	}
	if tr.TLSHandshakeTimeout != DDILTLSHandshakeTimeout {
		t.Errorf("expected TLSHandshakeTimeout=%v, got %v", DDILTLSHandshakeTimeout, tr.TLSHandshakeTimeout)
	}
	if tr.IdleConnTimeout != DDILIdleConnTimeout {
		t.Errorf("expected IdleConnTimeout=%v, got %v", DDILIdleConnTimeout, tr.IdleConnTimeout)
	}
	if tr.DialContext == nil {
		t.Fatal("expected non-nil Airgap DialContext")
	}

	c := NewDDILHTTPClient()
	clientTr, ok := c.Transport.(*http.Transport)
	if !ok || clientTr == nil {
		t.Fatalf("expected *http.Transport on NewDDILHTTPClient, got %T", c.Transport)
	}
	if clientTr.ResponseHeaderTimeout != DDILResponseHeaderTimeout {
		t.Errorf("expected client ResponseHeaderTimeout=%v, got %v", DDILResponseHeaderTimeout, clientTr.ResponseHeaderTimeout)
	}
	if clientTr.IdleConnTimeout != DDILIdleConnTimeout {
		t.Errorf("expected client IdleConnTimeout=%v, got %v", DDILIdleConnTimeout, clientTr.IdleConnTimeout)
	}
}
