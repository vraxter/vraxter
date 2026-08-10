package client

import (
	"context"
)

// APICredentials implements grpc/credentials.PerRPCCredentials
type APICredentials struct {
	token string
}

// NewAPICredentials creates a new PerRPCCredentials using the provided token
func NewAPICredentials(token string) *APICredentials {
	return &APICredentials{
		token: token,
	}
}

// GetRequestMetadata gets the current request metadata, refreshing tokens if required
func (c *APICredentials) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": "Bearer " + c.token,
	}, nil
}

// RequireTransportSecurity indicates whether the credentials requires transport security
func (c *APICredentials) RequireTransportSecurity() bool {
	return false // We allow this locally without TLS
}
