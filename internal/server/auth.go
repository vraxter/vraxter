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
	"net/http"
	"strings"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/security"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type contextKey string
const scopesCtxKey = contextKey("scopes")

var globalAPIKeyRepo *db.APIKeyRepository

// SetAPIKeyRepo initializes the global reference (used by middleware)
func SetAPIKeyRepo(repo *db.APIKeyRepository) {
	globalAPIKeyRepo = repo
}

// UnaryAuthInterceptor returns a gRPC interceptor that validates the Authorization header
func UnaryAuthInterceptor(expectedKey string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		ctx, err := authorize(ctx, expectedKey)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamAuthInterceptor returns a gRPC stream interceptor that validates the Authorization header
func StreamAuthInterceptor(expectedKey string) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// Note: for streams we would need a wrapped stream to propagate context
		// For simplicity, we just authorize, but scope checks in stream handlers might not have the modified context
		// unless we wrap the stream.
		if _, err := authorize(stream.Context(), expectedKey); err != nil {
			return err
		}
		return handler(srv, stream)
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

	provided := values[0]
	// 1. Check Root Key
	if err := security.ValidateAPIKey(expectedKey, provided); err == nil {
		// Root key gets wildcards
		return context.WithValue(ctx, scopesCtxKey, []string{"*"}), nil
	}

	// 2. Check Scoped Tokens
	if globalAPIKeyRepo != nil {
		// Strip Bearer prefix if any
		tokenStr := strings.TrimPrefix(provided, "Bearer ")
		tokenStr = strings.TrimSpace(tokenStr)

		hash := sha256.Sum256([]byte(tokenStr))
		hashStr := hex.EncodeToString(hash[:])

		if key, err := globalAPIKeyRepo.GetByKeyHash(hashStr); err == nil {
			scopes := strings.Split(key.Scopes, ",")
			return context.WithValue(ctx, scopesCtxKey, scopes), nil
		}
	}

	return ctx, status.Errorf(codes.Unauthenticated, "invalid authorization token")
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

// HTTPAuthMiddleware wraps an http.Handler and enforces the Authorization header.
func HTTPAuthMiddleware(expectedKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		if err := security.ValidateAPIKey(expectedKey, authHeader); err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
