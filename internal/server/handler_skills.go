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
	"strings"

	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/pkg/types"
)

func (h *AgentHandler) ListSkills(ctx context.Context, req *v1.ListSkillsRequest) (*v1.ListSkillsResponse, error) {
	if err := EnforceScope(ctx, "skills:read"); err != nil {
		return nil, err
	}
	skills, err := h.skillService.ListSkills()
	if err != nil {
		return nil, err
	}

	var res []*v1.SkillInfo
	for _, s := range skills {
		res = append(res, &v1.SkillInfo{
			Id:          s.ID,
			Name:        s.Name,
			Description: s.Description,
			Version:     s.Version,
			Engine:      s.Engine,
			Tier:        int32(s.Tier),
			Score:       float32(s.Score),
			IsOfficial:  s.IsOfficial,
			Checksum:    s.Checksum,
			Permissions: s.Permissions,
			Downloads:   int32(s.Downloads),
			HasParams:   s.ParamsSchema != "",
		})
	}
	return &v1.ListSkillsResponse{Skills: res}, nil
}

func (h *AgentHandler) GetSkillInfo(ctx context.Context, req *v1.GetSkillInfoRequest) (*v1.GetSkillInfoResponse, error) {
	if err := EnforceScope(ctx, "skills:read"); err != nil {
		return nil, err
	}
	s, err := h.skillService.GetSkill(req.Id)
	if err != nil {
		return nil, err
	}

	info := &v1.SkillInfo{
		Id:          s.ID,
		Name:        s.Name,
		Description: s.Description,
		Version:     s.Version,
		Engine:      s.Engine,
		Tier:        int32(s.Tier),
		Score:       float32(s.Score),
		IsOfficial:  s.IsOfficial,
		Checksum:    s.Checksum,
		Permissions: s.Permissions,
		Downloads:   int32(s.Downloads),
		HasParams:   s.ParamsSchema != "",
	}
	return &v1.GetSkillInfoResponse{Skill: info, Schema: s.ParamsSchema}, nil
}

func (h *AgentHandler) InstallSkill(ctx context.Context, req *v1.InstallSkillRequest) (*v1.InstallSkillResponse, error) {
	if err := EnforceScope(ctx, "skills:write"); err != nil {
		return nil, err
	}
	h.cfgMu.RLock()
	privacyPolicy := h.config.PrivacyPolicy
	isolationMode := h.config.IsolationMode
	hubURL := strings.TrimSpace(h.config.HubURL)
	h.cfgMu.RUnlock()
	if isolationMode == "airgap" || privacyPolicy == "strict_local" {
		return &v1.InstallSkillResponse{
			Success: false,
			Message: "Error: remote skill registry access is blocked in airgap/strict_local mode. Use 'vraxter skill verify' and 'vraxter skills inject' with offline signed artifacts.",
		}, nil
	}
	if hubURL == "" {
		return &v1.InstallSkillResponse{
			Success: false,
			Message: "Error: no remote skill registry URL is configured. Use 'vraxter skills inject' to register locally verified WASM skills.",
		}, nil
	}
	return &v1.InstallSkillResponse{
		Success: false,
		Message: "Error: remote hub installation is disabled in this release; import signed artifacts via 'vraxter skills inject'.",
	}, nil
}

func (h *AgentHandler) DownloadSkill(ctx context.Context, req *v1.DownloadSkillRequest) (*v1.DownloadSkillResponse, error) {
	if err := EnforceScope(ctx, "skills:write"); err != nil {
		return nil, err
	}
	h.cfgMu.RLock()
	privacyPolicy := h.config.PrivacyPolicy
	isolationMode := h.config.IsolationMode
	hubURL := strings.TrimSpace(h.config.HubURL)
	h.cfgMu.RUnlock()
	if isolationMode == "airgap" || privacyPolicy == "strict_local" {
		return &v1.DownloadSkillResponse{
			Success: false,
			Message: "Error: remote skill downloads are blocked in airgap/strict_local mode.",
		}, nil
	}
	if hubURL == "" {
		return &v1.DownloadSkillResponse{
			Success: false,
			Message: "Error: no remote skill registry URL is configured.",
		}, nil
	}
	return &v1.DownloadSkillResponse{
		Success: false,
		Message: "Error: remote hub downloads are disabled in this release; transfer signed skill bundles offline.",
	}, nil
}

func (h *AgentHandler) InjectSkill(ctx context.Context, req *v1.InjectSkillRequest) (*v1.InjectSkillResponse, error) {
	if err := EnforceScope(ctx, "skills:write"); err != nil {
		return nil, err
	}
	var manifestPath string
	if req.ManifestPath != nil {
		manifestPath = *req.ManifestPath
	}
	manifest, err := h.skillService.RegisterFromWASM(req.FilePath, manifestPath)
	if err != nil {
		return &v1.InjectSkillResponse{Success: false, Message: err.Error()}, nil
	}

	if h.engine != nil {
		h.engine.RegisterSkill(manifest)
	}

	return &v1.InjectSkillResponse{
		Success: true,
		Message: fmt.Sprintf("Skill '%s' (ID: %s, v%s) successfully injected and registered into live engine.", manifest.Name, manifest.ID, manifest.Version),
		SkillId: manifest.ID,
	}, nil
}

func (h *AgentHandler) InspectSkill(ctx context.Context, req *v1.InspectSkillRequest) (*v1.InspectSkillResponse, error) {
	if err := EnforceScope(ctx, "skills:read"); err != nil {
		return nil, err
	}
	s, err := h.skillService.GetSkill(req.Id)
	if err != nil {
		return &v1.InspectSkillResponse{
			IsValid: false,
			Message: fmt.Sprintf("Failed to load skill: %v", err),
		}, nil
	}

	actualHash, err := h.skillService.CalculateHash(s.Command)
	if err != nil {
		return &v1.InspectSkillResponse{
			IsValid: false,
			ExpectedChecksum: s.Checksum,
			Message: fmt.Sprintf("Failed to read skill binary: %v", err),
		}, nil
	}

	isValid := actualHash == s.Checksum
	msg := "Checksum matched. Skill is valid."
	if !isValid {
		msg = "DANGER: Checksum mismatch! The binary has been modified."
	}

	return &v1.InspectSkillResponse{
		IsValid:          isValid,
		ExpectedChecksum: s.Checksum,
		ActualChecksum:   actualHash,
		Message:          msg,
	}, nil
}

func (h *AgentHandler) DeleteSkill(ctx context.Context, req *v1.DeleteSkillRequest) (*v1.DeleteSkillResponse, error) {
	if err := EnforceScope(ctx, "skills:write"); err != nil {
		return nil, err
	}
	err := h.skillService.DeleteSkill(req.Id)
	if err != nil {
		return &v1.DeleteSkillResponse{Success: false, Message: err.Error()}, nil
	}
	if h.engine != nil {
		h.engine.UnregisterSkill(req.Id)
	}
	return &v1.DeleteSkillResponse{Success: true, Message: "Skill deleted successfully."}, nil
}

func (h *AgentHandler) TrustSkill(ctx context.Context, req *v1.TrustSkillRequest) (*v1.TrustSkillResponse, error) {
	if err := EnforceScope(ctx, "skills:write"); err != nil {
		return nil, err
	}
	s, err := h.skillService.GetSkill(req.Id)
	if err != nil {
		return &v1.TrustSkillResponse{Success: false, Message: err.Error()}, nil
	}
	
	s.Tier = types.Tier2CommunityVerified
	if err := h.skillService.UpdateSkill(s); err != nil {
		return &v1.TrustSkillResponse{Success: false, Message: fmt.Sprintf("Failed to save skill: %v", err)}, nil
	}

	return &v1.TrustSkillResponse{Success: true, Message: fmt.Sprintf("Skill '%s' is now trusted.", s.Name)}, nil
}
