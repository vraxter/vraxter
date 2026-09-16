// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (h *AgentHandler) GenerateToken(ctx context.Context, req *v1.GenerateTokenRequest) (*v1.GenerateTokenResponse, error) {
	// Generating tokens requires root auth access
	if err := EnforceScope(ctx, "*"); err != nil {
		return nil, status.Errorf(codes.PermissionDenied, "only root key can generate tokens")
	}

	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to generate secure token")
	}
	
	rawToken := "vrx_" + hex.EncodeToString(bytes)
	
	hash := sha256.Sum256([]byte(rawToken))
	hashStr := hex.EncodeToString(hash[:])

	id := uuid.New().String()
	apiKey := &db.APIKey{
		ID:        id,
		Name:      req.Name,
		KeyHash:   hashStr,
		Scopes:    req.Scopes,
	}

	if globalAPIKeyRepo != nil {
		if err := globalAPIKeyRepo.Create(apiKey); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to save token: %v", err)
		}
	} else {
		return nil, status.Errorf(codes.Internal, "auth repository not initialized")
	}

	return &v1.GenerateTokenResponse{
		Token: rawToken,
		Id:    id,
	}, nil
}

func (h *AgentHandler) RevokeToken(ctx context.Context, req *v1.RevokeTokenRequest) (*v1.RevokeTokenResponse, error) {
	if err := EnforceScope(ctx, "*"); err != nil {
		return nil, status.Errorf(codes.PermissionDenied, "only root key can revoke tokens")
	}

	if globalAPIKeyRepo != nil {
		if err := globalAPIKeyRepo.Delete(req.Id); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to delete token: %v", err)
		}
	} else {
		return nil, status.Errorf(codes.Internal, "auth repository not initialized")
	}

	return &v1.RevokeTokenResponse{
		Success: true,
		Message: fmt.Sprintf("Token %s revoked", req.Id),
	}, nil
}

func (h *AgentHandler) ListTokens(ctx context.Context, req *v1.ListTokensRequest) (*v1.ListTokensResponse, error) {
	if err := EnforceScope(ctx, "*"); err != nil {
		return nil, status.Errorf(codes.PermissionDenied, "only root key can list tokens")
	}

	var res []*v1.TokenInfo
	if globalAPIKeyRepo != nil {
		keys, err := globalAPIKeyRepo.List()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to list tokens: %v", err)
		}
		for _, k := range keys {
			res = append(res, &v1.TokenInfo{
				Id:        k.ID,
				Name:      k.Name,
				Scopes:    k.Scopes,
				CreatedAt: k.CreatedAt,
			})
		}
	} else {
		return nil, status.Errorf(codes.Internal, "auth repository not initialized")
	}

	return &v1.ListTokensResponse{Tokens: res}, nil
}
