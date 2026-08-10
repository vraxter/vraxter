package server

import (
	"context"
	"fmt"

	v1 "github.com/patagonicrune/vraxter/api/v1"
)

func (h *AgentHandler) GetConfig(ctx context.Context, req *v1.GetConfigRequest) (*v1.ConfigResponse, error) {
	return &v1.ConfigResponse{
		PrivacyPolicy:        h.config.PrivacyPolicy,
		HubUrl:               h.config.HubURL,
		RequireSkillApproval: h.config.RequireSkillApproval,
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

	if err := h.config.SaveSettings(); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	return &v1.ConfigResponse{
		PrivacyPolicy:        h.config.PrivacyPolicy,
		HubUrl:               h.config.HubURL,
		RequireSkillApproval: h.config.RequireSkillApproval,
	}, nil
}
