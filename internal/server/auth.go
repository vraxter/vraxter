// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/vraxter/vraxter/internal/audit"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/security"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type contextKey string

const (
	scopesCtxKey    = contextKey("scopes")
	principalCtxKey = contextKey("principal")
)

// Principal represents the cryptographically authenticated identity of a gRPC caller,
// derived exclusively from verified mTLS client certificates and/or authenticated API tokens.
type Principal struct {
	AuthMethod string   // "mtls+root_key", "mtls+api_key", "root_key", "api_key"
	Subject    string   // Verified mTLS Subject CommonName or primary SAN
	SANs       []string // Verified mTLS SANs (DNS, Email, URI, IP)
	TokenID    string   // Authenticated api_keys.id or "root"
	TokenName  string   // Authenticated api_keys.name or "daemon-root"
	Scopes     []string
}

// AuditActor returns the canonical verified actor identifier recorded in audit_ledger.
func (p Principal) AuditActor() string {
	subject := strings.TrimSpace(p.Subject)
	tokenID := strings.TrimSpace(p.TokenID)
	if subject != "" {
		if tokenID != "" && tokenID != "root" {
			return fmt.Sprintf("mtls:%s|api_key:%s", subject, tokenID)
		}
		return "mtls:" + subject
	}
	if tokenID != "" && tokenID != "root" {
		return "api_key:" + tokenID
	}
	if tokenID == "root" {
		return "root:daemon"
	}
	return "system"
}

// WithPrincipal attaches a verified Principal and its authenticated audit actor to ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, principalCtxKey, p)
	ctx = audit.WithAuthenticatedActor(ctx, p.AuditActor())
	return ctx
}

// PrincipalFromContext retrieves the authenticated Principal attached to ctx.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	p, ok := ctx.Value(principalCtxKey).(Principal)
	return p, ok
}

func extractVerifiedMTLSPeer(ctx context.Context) (subject string, sans []string, ok bool) {
	p, hasPeer := peer.FromContext(ctx)
	if !hasPeer || p == nil || p.AuthInfo == nil {
		return "", nil, false
	}
	tlsInfo, isTLS := p.AuthInfo.(credentials.TLSInfo)
	if !isTLS {
		return "", nil, false
	}
	// Trust ONLY certificates that passed CA chain verification.
	if len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.VerifiedChains[0]) == 0 {
		return "", nil, false
	}
	cert := tlsInfo.State.VerifiedChains[0][0]
	for _, dns := range cert.DNSNames {
		if d := strings.TrimSpace(dns); d != "" {
			sans = append(sans, "dns:"+d)
		}
	}
	for _, email := range cert.EmailAddresses {
		if em := strings.TrimSpace(email); em != "" {
			sans = append(sans, "email:"+em)
		}
	}
	for _, u := range cert.URIs {
		if u != nil && strings.TrimSpace(u.String()) != "" {
			sans = append(sans, "uri:"+strings.TrimSpace(u.String()))
		}
	}
	for _, ip := range cert.IPAddresses {
		if ip != nil {
			sans = append(sans, "ip:"+ip.String())
		}
	}

	subject = strings.TrimSpace(cert.Subject.CommonName)
	if subject == "" && len(sans) > 0 {
		subject = sans[0]
	}
	if subject == "" {
		subject = cert.SerialNumber.String()
	}
	return subject, sans, true
}

var (
	globalAPIKeyRepoMu sync.RWMutex
	globalAPIKeyRepo   *db.APIKeyRepository
)

// SetAPIKeyRepo initializes the global reference (used by middleware).
func SetAPIKeyRepo(repo *db.APIKeyRepository) {
	globalAPIKeyRepoMu.Lock()
	defer globalAPIKeyRepoMu.Unlock()
	globalAPIKeyRepo = repo
}

func getAPIKeyRepo() *db.APIKeyRepository {
	globalAPIKeyRepoMu.RLock()
	defer globalAPIKeyRepoMu.RUnlock()
	return globalAPIKeyRepo
}

// wrappedServerStream propagates an authenticated, scoped context into gRPC stream handlers.
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

// WithScopes attaches the provided authorization scopes to ctx (primarily for tests/internal wiring).
func WithScopes(ctx context.Context, scopes ...string) context.Context {
	cleaned := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return context.WithValue(ctx, scopesCtxKey, cleaned)
}

// UnaryAuthInterceptor returns a gRPC interceptor that validates the Authorization header
// and propagates resolved scopes in the handler context.
func UnaryAuthInterceptor(expectedKey string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		authCtx, err := authorize(ctx, expectedKey)
		if err != nil {
			return nil, err
		}
		return handler(authCtx, req)
	}
}

// StreamAuthInterceptor returns a gRPC stream interceptor that validates the Authorization header
// and wraps the ServerStream so downstream streaming handlers receive the scoped context.
func StreamAuthInterceptor(expectedKey string) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		authCtx, err := authorize(stream.Context(), expectedKey)
		if err != nil {
			return err
		}
		return handler(srv, &wrappedServerStream{
			ServerStream: stream,
			ctx:          authCtx,
		})
	}
}

func authorize(ctx context.Context, expectedKey string) (context.Context, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx, status.Errorf(codes.Unauthenticated, "missing metadata")
	}

	values := md["authorization"]
	if len(values) == 0 {
		return ctx, status.Errorf(codes.Unauthenticated, "authorization token is not provided")
	}

	mtlsSubject, mtlsSANs, hasMTLS := extractVerifiedMTLSPeer(ctx)

	provided := values[0]
	// 1. Check Root Key
	if err := security.ValidateAPIKey(expectedKey, provided); err == nil {
		// Root key gets wildcards
		authMethod := "root_key"
		if hasMTLS {
			authMethod = "mtls+root_key"
		}
		scopes := []string{"*"}
		principal := Principal{
			AuthMethod: authMethod,
			Subject:    mtlsSubject,
			SANs:       mtlsSANs,
			TokenID:    "root",
			TokenName:  "daemon-root",
			Scopes:     scopes,
		}
		authCtx := context.WithValue(ctx, scopesCtxKey, scopes)
		return WithPrincipal(authCtx, principal), nil
	}

	// 2. Check Scoped Tokens
	if repo := getAPIKeyRepo(); repo != nil {
		// Strip Bearer prefix if any
		tokenStr := strings.TrimPrefix(provided, "Bearer ")
		tokenStr = strings.TrimSpace(tokenStr)

		hash := sha256.Sum256([]byte(tokenStr))
		hashStr := hex.EncodeToString(hash[:])

		if key, err := repo.GetByKeyHash(hashStr); err == nil {
			rawScopes := strings.Split(key.Scopes, ",")
			scopes := make([]string, 0, len(rawScopes))
			for _, s := range rawScopes {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					scopes = append(scopes, trimmed)
				}
			}
			authMethod := "api_key"
			if hasMTLS {
				authMethod = "mtls+api_key"
			}
			principal := Principal{
				AuthMethod: authMethod,
				Subject:    mtlsSubject,
				SANs:       mtlsSANs,
				TokenID:    key.ID,
				TokenName:  key.Name,
				Scopes:     scopes,
			}
			authCtx := context.WithValue(ctx, scopesCtxKey, scopes)
			return WithPrincipal(authCtx, principal), nil
		}
	}

	return ctx, status.Errorf(codes.Unauthenticated, "invalid authorization token")
}

// HasScope reports whether ctx carries the required scope (or wildcard "*").
func HasScope(ctx context.Context, required string) bool {
	scopes, ok := ctx.Value(scopesCtxKey).([]string)
	if !ok {
		return false
	}
	for _, s := range scopes {
		if s == "*" || s == required {
			return true
		}
	}
	return false
}

// EnforceScope checks if the context has the required scope.
func EnforceScope(ctx context.Context, required string) error {
	scopes, ok := ctx.Value(scopesCtxKey).([]string)
	if !ok {
		return status.Errorf(codes.PermissionDenied, "no scopes found in context")
	}

	for _, s := range scopes {
		if s == "*" || s == required {
			return nil
		}
	}

	return status.Errorf(codes.PermissionDenied, "missing required scope: %s", required)
}

// EnforceAnyScope succeeds if the context carries at least one of the allowed scopes (or "*").
func EnforceAnyScope(ctx context.Context, allowed ...string) error {
	scopes, ok := ctx.Value(scopesCtxKey).([]string)
	if !ok {
		return status.Errorf(codes.PermissionDenied, "no scopes found in context")
	}

	for _, s := range scopes {
		if s == "*" {
			return nil
		}
		for _, req := range allowed {
			if s == req {
				return nil
			}
		}
	}

	return status.Errorf(codes.PermissionDenied, "missing required scope (need one of: %s)", strings.Join(allowed, ", "))
}
