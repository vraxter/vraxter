// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vraxter/vraxter/internal/utils"
)

// MTLSPaths holds the filesystem paths for the local CA, server certificate/key,
// and client certificate/key used for mTLS 1.3 gRPC connections.
type MTLSPaths struct {
	CAPath         string
	CAKeyPath      string
	ServerCertPath string
	ServerKeyPath  string
	ClientCertPath string
	ClientKeyPath  string
}

// DefaultMTLSPaths returns the standard PKI file locations under <appDir>/pki.
func DefaultMTLSPaths(appDir string) MTLSPaths {
	pkiDir := filepath.Join(appDir, "pki")
	return MTLSPaths{
		CAPath:         filepath.Join(pkiDir, "ca.crt"),
		CAKeyPath:      filepath.Join(pkiDir, "ca.key"),
		ServerCertPath: filepath.Join(pkiDir, "server.crt"),
		ServerKeyPath:  filepath.Join(pkiDir, "server.key"),
		ClientCertPath: filepath.Join(pkiDir, "client.crt"),
		ClientKeyPath:  filepath.Join(pkiDir, "client.key"),
	}
}

func allFilesExist(paths ...string) bool {
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			return false
		}
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() || fi.Size() == 0 {
			return false
		}
	}
	return true
}

func randomSerialNumber() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}

func encodeCertAndKeyPEM(certDER []byte, key *ecdsa.PrivateKey) (certPEM, keyPEM []byte, err error) {
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// EnsureLocalPKI verifies that a local mTLS PKI (Root CA, Server certificate, Client certificate)
// exists in <appDir>/pki, generating a fresh self-signed ECDSA P-256 PKI atomically with 0600
// permissions if any file is missing.
func EnsureLocalPKI(appDir string, extraSANHosts []string) (MTLSPaths, error) {
	paths := DefaultMTLSPaths(appDir)
	pkiDir := filepath.Dir(paths.CAPath)
	if err := os.MkdirAll(pkiDir, 0700); err != nil {
		return MTLSPaths{}, fmt.Errorf("failed to create PKI directory %s: %w", pkiDir, err)
	}
	_ = os.Chmod(pkiDir, 0700)

	if allFilesExist(paths.CAPath, paths.ServerCertPath, paths.ServerKeyPath, paths.ClientCertPath, paths.ClientKeyPath) {
		return paths, nil
	}

	now := time.Now().UTC()

	// 1. Generate Root CA (ECDSA P-256)
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return MTLSPaths{}, fmt.Errorf("failed generating CA key: %w", err)
	}
	caSerial, err := randomSerialNumber()
	if err != nil {
		return MTLSPaths{}, err
	}
	caTmpl := &x509.Certificate{
		SerialNumber: caSerial,
		Subject: pkix.Name{
			Organization: []string{"Vraxter Sovereign AI"},
			CommonName:   "Vraxter Local Root CA",
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(5 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return MTLSPaths{}, fmt.Errorf("failed creating CA certificate: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return MTLSPaths{}, err
	}
	caCertPEM, caKeyPEM, err := encodeCertAndKeyPEM(caDER, caKey)
	if err != nil {
		return MTLSPaths{}, err
	}

	// 2. Collect SANs for Server Certificate (localhost, loopback, interface IPs, and extraSANHosts)
	dnsNames := []string{"localhost", "vraxter.local"}
	ipAddrs := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	seenDNS := map[string]bool{"localhost": true, "vraxter.local": true}
	seenIP := map[string]bool{"127.0.0.1": true, "::1": true}

	addHostOrIP := func(raw string) {
		h := strings.TrimSpace(raw)
		if h == "" {
			return
		}
		if splitH, _, splitErr := net.SplitHostPort(h); splitErr == nil {
			h = splitH
		}
		h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
		if h == "" || h == "0.0.0.0" || h == "::" {
			return
		}
		if ip := net.ParseIP(h); ip != nil {
			if !seenIP[ip.String()] {
				seenIP[ip.String()] = true
				ipAddrs = append(ipAddrs, ip)
			}
			return
		}
		low := strings.ToLower(h)
		if !seenDNS[low] {
			seenDNS[low] = true
			dnsNames = append(dnsNames, low)
		}
	}

	for _, h := range extraSANHosts {
		addHostOrIP(h)
	}
	if ifaces, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range ifaces {
			if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP != nil && !ipNet.IP.IsUnspecified() {
				addHostOrIP(ipNet.IP.String())
			}
		}
	}

	// 3. Generate Server Certificate (ECDSA P-256)
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return MTLSPaths{}, fmt.Errorf("failed generating server key: %w", err)
	}
	serverSerial, err := randomSerialNumber()
	if err != nil {
		return MTLSPaths{}, err
	}
	serverTmpl := &x509.Certificate{
		SerialNumber: serverSerial,
		Subject: pkix.Name{
			Organization: []string{"Vraxter Sovereign AI"},
			CommonName:   "vraxter-daemon",
		},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    dnsNames,
		IPAddresses: ipAddrs,
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTmpl, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return MTLSPaths{}, fmt.Errorf("failed creating server certificate: %w", err)
	}
	serverCertPEM, serverKeyPEM, err := encodeCertAndKeyPEM(serverDER, serverKey)
	if err != nil {
		return MTLSPaths{}, err
	}

	// 4. Generate Client Certificate (ECDSA P-256)
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return MTLSPaths{}, fmt.Errorf("failed generating client key: %w", err)
	}
	clientSerial, err := randomSerialNumber()
	if err != nil {
		return MTLSPaths{}, err
	}
	clientTmpl := &x509.Certificate{
		SerialNumber: clientSerial,
		Subject: pkix.Name{
			Organization: []string{"Vraxter Sovereign AI"},
			CommonName:   "vraxter-client",
		},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTmpl, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		return MTLSPaths{}, fmt.Errorf("failed creating client certificate: %w", err)
	}
	clientCertPEM, clientKeyPEM, err := encodeCertAndKeyPEM(clientDER, clientKey)
	if err != nil {
		return MTLSPaths{}, err
	}

	// 5. Persist all PKI artifacts atomically with 0600 permissions
	writes := []struct {
		path string
		data []byte
	}{
		{paths.CAPath, caCertPEM},
		{paths.CAKeyPath, caKeyPEM},
		{paths.ServerCertPath, serverCertPEM},
		{paths.ServerKeyPath, serverKeyPEM},
		{paths.ClientCertPath, clientCertPEM},
		{paths.ClientKeyPath, clientKeyPEM},
	}
	for _, w := range writes {
		if err := utils.AtomicWriteFile(w.path, w.data, 0600); err != nil {
			return MTLSPaths{}, fmt.Errorf("failed writing PKI file %s: %w", w.path, err)
		}
	}

	return paths, nil
}

// ValidateServerMTLSConfig verifies that tlsCfg enforces strict mTLS 1.3 with client certificate verification.
func ValidateServerMTLSConfig(tlsCfg *tls.Config) error {
	if tlsCfg == nil {
		return fmt.Errorf("mTLS 1.3 configuration is nil")
	}
	if tlsCfg.MinVersion < tls.VersionTLS13 {
		return fmt.Errorf("mTLS configuration must enforce TLS 1.3 (MinVersion=%x)", tlsCfg.MinVersion)
	}
	if tlsCfg.ClientAuth != tls.RequireAndVerifyClientCert {
		return fmt.Errorf("mTLS configuration must enforce tls.RequireAndVerifyClientCert")
	}
	if len(tlsCfg.Certificates) == 0 {
		return fmt.Errorf("mTLS configuration has no server certificates loaded")
	}
	if tlsCfg.ClientCAs == nil {
		return fmt.Errorf("mTLS configuration has no ClientCAs pool configured")
	}
	return nil
}

// LoadServerTLSConfig loads the CA certificate, server certificate, and server private key
// from disk and returns a strict mTLS 1.3 server *tls.Config.
func LoadServerTLSConfig(caPath, certPath, keyPath string) (*tls.Config, error) {
	if strings.TrimSpace(caPath) == "" || strings.TrimSpace(certPath) == "" || strings.TrimSpace(keyPath) == "" {
		return nil, fmt.Errorf("mTLS 1.3 requires ca_path, cert_path, and key_path")
	}

	serverCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load server TLS keypair (%s, %s): %w", certPath, keyPath, err)
	}

	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate %s: %w", caPath, err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate PEM from %s", caPath)
	}

	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
	}
	return cfg, nil
}

// LoadClientTLSConfig loads the CA certificate, client certificate, and client private key
// from disk and returns a strict mTLS 1.3 client *tls.Config.
func LoadClientTLSConfig(caPath, certPath, keyPath, serverName string) (*tls.Config, error) {
	if strings.TrimSpace(caPath) == "" || strings.TrimSpace(certPath) == "" || strings.TrimSpace(keyPath) == "" {
		return nil, fmt.Errorf("mTLS 1.3 client requires ca_path, cert_path, and key_path")
	}

	clientCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load client TLS keypair (%s, %s): %w", certPath, keyPath, err)
	}

	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate %s: %w", caPath, err)
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate PEM from %s", caPath)
	}

	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      rootCAs,
		ServerName:   strings.TrimSpace(serverName),
	}
	return cfg, nil
}
