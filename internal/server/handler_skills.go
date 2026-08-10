package server

import (
	"context"
	"fmt"
	
	v1 "github.com/patagonicrune/vraxter/api/v1"
	"github.com/patagonicrune/vraxter/pkg/types"
)

func (h *AgentHandler) ListSkills(ctx context.Context, req *v1.ListSkillsRequest) (*v1.ListSkillsResponse, error) {
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
	if h.config.PrivacyPolicy == "strict_local" {
		return &v1.InstallSkillResponse{
			Success: false,
			Message: "Error: Vraxter is in strict_local mode. Hub interaction is disabled. Use 'vraxter skills inject' for local files.",
		}, nil
	}
	return &v1.InstallSkillResponse{Success: true, Message: "Connecting to Hub URL... (Mocked)"}, nil
}

func (h *AgentHandler) DownloadSkill(ctx context.Context, req *v1.DownloadSkillRequest) (*v1.DownloadSkillResponse, error) {
	if h.config.PrivacyPolicy == "strict_local" {
		return &v1.DownloadSkillResponse{
			Success: false,
			Message: "Error: Vraxter is in strict_local mode. Hub interaction is disabled.",
		}, nil
	}
	return &v1.DownloadSkillResponse{Success: true, Message: fmt.Sprintf("Downloading skill to %s... (Mocked)", req.DestPath)}, nil
}

func (h *AgentHandler) InjectSkill(ctx context.Context, req *v1.InjectSkillRequest) (*v1.InjectSkillResponse, error) {
	err := h.skillService.RegisterFromWASM(req.FilePath)
	if err != nil {
		return &v1.InjectSkillResponse{Success: false, Message: err.Error()}, nil
	}
	return &v1.InjectSkillResponse{Success: true, Message: "Skill successfully injected."}, nil
}

func (h *AgentHandler) InspectSkill(ctx context.Context, req *v1.InspectSkillRequest) (*v1.InspectSkillResponse, error) {
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
	err := h.skillService.DeleteSkill(req.Id)
	if err != nil {
		return &v1.DeleteSkillResponse{Success: false, Message: err.Error()}, nil
	}
	return &v1.DeleteSkillResponse{Success: true, Message: "Skill deleted successfully."}, nil
}

func (h *AgentHandler) TrustSkill(ctx context.Context, req *v1.TrustSkillRequest) (*v1.TrustSkillResponse, error) {
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
