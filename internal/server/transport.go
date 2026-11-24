// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package server

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vraxter/vraxter/internal/security"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const staleSocketProbeTimeout = 250 * time.Millisecond

// IsLoopbackHost reports whether host refers strictly to the local loopback interface
// (127.0.0.0/8, ::1, or localhost). Unspecified addresses (0.0.0.0, ::, "") and LAN IPs return false.
func IsLoopbackHost(rawHost string) bool {
	h := strings.TrimSpace(rawHost)
	if splitH, _, err := net.SplitHostPort(h); err == nil {
		h = splitH
	}
	h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	if h == "" {
		return false
	}
	if strings.EqualFold(h, "localhost") || strings.EqualFold(h, "localhost.localdomain") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// ParseEndpoint normalizes a gRPC endpoint string ("unix:///path/to/sock", "tcp://host:port",
// or bare "host:port") into its network ("unix" or "tcp"), address, and whether it binds
// or connects to a non-loopback TCP interface.
func ParseEndpoint(endpoint string) (network, address string, isNonLoopbackTCP bool, err error) {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return "", "", false, fmt.Errorf("empty endpoint")
	}

	if strings.HasPrefix(ep, "unix://") {
		sockPath := strings.TrimPrefix(ep, "unix://")
		if strings.TrimSpace(sockPath) == "" {
			return "", "", false, fmt.Errorf("empty unix socket path in %q", endpoint)
		}
		return "unix", filepath.Clean(sockPath), false, nil
	}
	if strings.HasPrefix(ep, "unix:") {
		sockPath := strings.TrimPrefix(ep, "unix:")
		if strings.TrimSpace(sockPath) == "" {
			return "", "", false, fmt.Errorf("empty unix socket path in %q", endpoint)
		}
		return "unix", filepath.Clean(sockPath), false, nil
	}

	tcpAddr := strings.TrimPrefix(ep, "tcp://")
	host, port, splitErr := net.SplitHostPort(tcpAddr)
	if splitErr != nil || strings.TrimSpace(port) == "" {
		return "", "", false, fmt.Errorf("invalid TCP endpoint %q: %w", endpoint, splitErr)
	}

	return "tcp", tcpAddr, !IsLoopbackHost(host), nil
}

type unixSocketListener struct {
	net.Listener
	sockPath  string
	closeOnce sync.Once
}

func (l *unixSocketListener) Close() error {
	var err error
	l.closeOnce.Do(func() {
		err = l.Listener.Close()
		_ = os.Remove(l.sockPath)
	})
	return err
}

// PrepareUnixSocketListener creates a Unix Domain Socket listener at sockPath with 0700
// parent directory permissions and 0600 socket file permissions.
// If sockPath already exists on disk:
//   - It probes the socket with a short dial timeout; if an active server responds, it fails closed.
//   - If no listener responds, it safely removes the stale socket leftover from a previous crash.
func PrepareUnixSocketListener(sockPath string) (net.Listener, error) {
	cleanPath := filepath.Clean(strings.TrimSpace(sockPath))
	if cleanPath == "" || cleanPath == "." {
		return nil, fmt.Errorf("invalid unix socket path %q", sockPath)
	}

	parentDir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(parentDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create unix socket directory %s: %w", parentDir, err)
	}
	_ = os.Chmod(parentDir, 0700)

	if fi, err := os.Lstat(cleanPath); err == nil {
		if fi.IsDir() {
			return nil, fmt.Errorf("unix socket path %s is a directory", cleanPath)
		}
		// Probe whether a live daemon is currently listening on the socket.
		conn, dialErr := net.DialTimeout("unix", cleanPath, staleSocketProbeTimeout)
		if dialErr == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("vraxter daemon is already running and listening on unix socket %s", cleanPath)
		}
		// Stale socket file from a crashed process — remove safely before binding.
		if rmErr := os.Remove(cleanPath); rmErr != nil && !os.IsNotExist(rmErr) {
			return nil, fmt.Errorf("failed to remove stale unix socket %s: %w", cleanPath, rmErr)
		}
	}

	lis, err := net.Listen("unix", cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on unix socket %s: %w", cleanPath, err)
	}
	if err := os.Chmod(cleanPath, 0600); err != nil {
		_ = lis.Close()
		_ = os.Remove(cleanPath)
		return nil, fmt.Errorf("failed to enforce 0600 permissions on unix socket %s: %w", cleanPath, err)
	}

	return &unixSocketListener{Listener: lis, sockPath: cleanPath}, nil
}

// ListenEndpoint opens a gRPC net.Listener for endpoint ("unix://..." or "tcp://...") and
// returns any required grpc.ServerOption (such as TLS credentials).
// When binding to a non-loopback TCP address or when forceMTLS is true, it strictly enforces
// mTLS 1.3 (tls.VersionTLS13 + tls.RequireAndVerifyClientCert) and fails closed if tlsCfg is missing or invalid.
func ListenEndpoint(endpoint string, tlsCfg *tls.Config, forceMTLS bool) (net.Listener, grpc.ServerOption, error) {
	network, address, isNonLoopbackTCP, err := ParseEndpoint(endpoint)
	if err != nil {
		return nil, nil, err
	}

	if network == "unix" {
		lis, err := PrepareUnixSocketListener(address)
		if err != nil {
			return nil, nil, err
		}
		return lis, nil, nil
	}

	requireMTLS := isNonLoopbackTCP || forceMTLS || tlsCfg != nil
	if requireMTLS {
		if err := security.ValidateServerMTLSConfig(tlsCfg); err != nil {
			return nil, nil, fmt.Errorf("VRAXTER SHIELD: refusing to start TCP listener on %s without mandatory mTLS 1.3 configuration: %w", address, err)
		}
		lis, err := net.Listen("tcp", address)
		if err != nil {
			return nil, nil, err
		}
		return lis, grpc.Creds(credentials.NewTLS(tlsCfg)), nil
	}

	lis, err := net.Listen("tcp", address)
	if err != nil {
		return nil, nil, err
	}
	return lis, nil, nil
}
