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
)

func (h *AgentHandler) GetConfig(ctx context.Context, req *v1.GetConfigRequest) (*v1.ConfigResponse, error) {
	return &v1.ConfigResponse{
		PrivacyPolicy:        h.config.PrivacyPolicy,
		HubUrl:               h.config.HubURL,
		RequireSkillApproval: h.config.RequireSkillApproval,
		WhitelistedIps:       h.config.WhitelistedIPs,
	}, nil
}

func (h *AgentHandler) UpdateConfig(ctx context.Context, req *v1.UpdateConfigRequest) (*v1.ConfigResponse, error) {
	if req.PrivacyPolicy != nil {
		h.config.PrivacyPolicy = *req.PrivacyPolicy
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
						fmt.Printf("⚠️ SECURITY WARNING: You have whitelisted a public IP/Hostname (%s). This breaches the strict_local air-gap policy.\n", ipStr)
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
	}, nil
}
