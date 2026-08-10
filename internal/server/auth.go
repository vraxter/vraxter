package server

import (
	"context"
	"net/http"

	"github.com/patagonicrune/vraxter/internal/security"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryAuthInterceptor returns a gRPC interceptor that validates the Authorization header
func UnaryAuthInterceptor(expectedKey string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := authorize(ctx, expectedKey); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamAuthInterceptor returns a gRPC stream interceptor that validates the Authorization header
func StreamAuthInterceptor(expectedKey string) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := authorize(stream.Context(), expectedKey); err != nil {
			return err
		}
		return handler(srv, stream)
	}
}

func authorize(ctx context.Context, expectedKey string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Errorf(codes.Unauthenticated, "missing metadata")
	}

	values := md["authorization"]
	if len(values) == 0 {
		return status.Errorf(codes.Unauthenticated, "authorization token is not provided")
	}

	if err := security.ValidateAPIKey(expectedKey, values[0]); err != nil {
		return status.Errorf(codes.Unauthenticated, "invalid authorization token")
	}

	return nil
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
