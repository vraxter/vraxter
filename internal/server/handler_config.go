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
	"fmt"

	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (h *AgentHandler) GetConfig(ctx context.Context, req *v1.GetConfigRequest) (*v1.ConfigResponse, error) {
	return &v1.ConfigResponse{
		PrivacyPolicy:        h.config.PrivacyPolicy,
		HubUrl:               h.config.HubURL,
		RequireSkillApproval: h.config.RequireSkillApproval,
		WhitelistedIps:       h.config.WhitelistedIPs,
		IsolationMode:        h.config.IsolationMode,
		IsAirgapLocked:       h.config.IsAirgapLocked,
	}, nil
}

func (h *AgentHandler) UpdateConfig(ctx context.Context, req *v1.UpdateConfigRequest) (*v1.ConfigResponse, error) {
	// 1. Anti-downgrade check: If airgap is locked (via CLI flag --airgap or VRAXTER_AIRGAP env), reject changes
	if h.config.IsAirgapLocked {
		if req.IsolationMode != nil && *req.IsolationMode != "airgap" {
			return nil, status.Errorf(codes.PermissionDenied, "airgap perimeter is locked by immutable system policy and cannot be modified at runtime")
		}
		if req.PrivacyPolicy != nil && *req.PrivacyPolicy != "strict_local" {
			return nil, status.Errorf(codes.PermissionDenied, "airgap perimeter is locked by immutable system policy and cannot be modified at runtime")
		}
	}

	// 2. Anti-downgrade check: If currently running in airgap mode, prevent dynamic runtime downgrade
	if h.config.IsAirgap() {
		if req.IsolationMode != nil && *req.IsolationMode != "airgap" {
			return nil, status.Errorf(codes.PermissionDenied, "cannot downgrade isolation mode from airgap while daemon is running; process restart required")
		}
		if req.PrivacyPolicy != nil && *req.PrivacyPolicy != "strict_local" {
			return nil, status.Errorf(codes.PermissionDenied, "cannot downgrade privacy policy from strict_local while daemon is running; process restart required")
		}
	}

	// 3. Apply isolation mode or privacy policy
	if req.IsolationMode != nil {
		mode := *req.IsolationMode
		if mode != "airgap" && mode != "local_lan" && mode != "hybrid" {
			return nil, status.Errorf(codes.InvalidArgument, "invalid isolation_mode '%s'; must be 'airgap', 'local_lan', or 'hybrid'", mode)
		}
		h.config.IsolationMode = mode
		if mode == "airgap" {
			h.config.PrivacyPolicy = "strict_local"
		} else {
			h.config.PrivacyPolicy = "custom"
		}
	} else if req.PrivacyPolicy != nil {
		h.config.PrivacyPolicy = *req.PrivacyPolicy
		if *req.PrivacyPolicy == "strict_local" {
			h.config.IsolationMode = "airgap"
		} else {
			h.config.IsolationMode = "hybrid"
		}
	}

	if req.HubUrl != nil {
		h.config.HubURL = *req.HubUrl
	}
	if req.RequireSkillApproval != nil {
		h.config.RequireSkillApproval = *req.RequireSkillApproval
	}
	if req.WhitelistedIps != nil {
		h.config.WhitelistedIPs = req.WhitelistedIps
		for _, ipStr := range req.WhitelistedIps {
			ips, _, err := utils.ParseAndLookup(ipStr)
			if err == nil {
				for _, ip := range ips {
					if utils.IsPublicIP(ip) {
						fmt.Printf("⚠️ SECURITY WARNING: You have whitelisted a public IP/Hostname (%s). This breaches the air-gap perimeter.\n", ipStr)
						break
					}
				}
			}
		}
	}

	if err := h.config.SaveSettings(); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	return &v1.ConfigResponse{
		PrivacyPolicy:        h.config.PrivacyPolicy,
		HubUrl:               h.config.HubURL,
		RequireSkillApproval: h.config.RequireSkillApproval,
		WhitelistedIps:       h.config.WhitelistedIPs,
		IsolationMode:        h.config.IsolationMode,
		IsAirgapLocked:       h.config.IsAirgapLocked,
	}, nil
}
