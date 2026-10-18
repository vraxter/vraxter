// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package coders

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/vraxter/vraxter/pkg/types"
)

// BuildAndSaveV1Manifest constructs a formal V1 Skill Manifest, serializes it to disk
// as <id>.manifest.json alongside the .wasm binary, and returns the runtime SkillManifest.
func BuildAndSaveV1Manifest(
	skillsDir, skillID, name, description, wasmPath, checksum, paramsSchema, lang string,
	permissions []string,
) types.SkillManifest {
	manifest := types.SkillManifest{
		ID:           skillID,
		Name:         name,
		Description:  description,
		ParamsSchema: paramsSchema,
		Command:      wasmPath,
		Engine:       "wasm",
		Language:     lang,
		Version:      "1.0.0",
		Tier:         types.Tier2CommunityVerified,
		Checksum:     checksum,
		Permissions:  permissions,
	}

	var schemaRaw json.RawMessage
	if trimmed := strings.TrimSpace(paramsSchema); trimmed != "" {
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			schemaRaw = json.RawMessage(trimmed)
		} else {
			unquoted, err := json.Marshal(trimmed)
			if err == nil {
				schemaRaw = unquoted
			}
		}
	}

	v1 := &types.SkillManifestV1{
		Schema:          types.ManifestSchemaV1URI,
		ManifestVersion: types.ManifestVersionV1,
		ID:              skillID,
		Name:            name,
		Version:         "1.0.0",
		Description:     description,
		Agent: types.AgentGovernance{
			Risk:     "medium",
			Approval: "on_risk",
		},
		Module: types.ModuleSpec{
			File:   filepath.Base(wasmPath),
			SHA256: checksum,
			ABI:    "wasip1",
		},
		IO: &types.IOSpec{
			Input: types.InputSpec{
				Via:    "stdin",
				Format: "json",
				Schema: schemaRaw,
			},
			Output: types.OutputSpec{
				Via:    "stdout",
				Format: "json",
			},
		},
		Limits: &types.LimitsSpec{
			MemoryMB:     128,
			MaxRuntimeMS: 30000,
		},
	}

	var mounts []types.MountSpec
	hasNetwork := false
	for _, p := range permissions {
		pClean := strings.TrimSpace(p)
		pLow := strings.ToLower(pClean)
		if pLow == "network" || strings.HasPrefix(pLow, "network:") || strings.HasPrefix(pLow, "net:") {
			hasNetwork = true
		} else if strings.HasPrefix(pLow, "fs_read:") || strings.HasPrefix(pLow, "fs:read:") {
			idx := strings.LastIndex(pClean, ":")
			dir := strings.TrimSpace(pClean[idx+1:])
			mounts = append(mounts, types.MountSpec{
				Guest:    dir,
				Source:   "host",
				HostPath: dir,
				Mode:     "ro",
			})
		} else if strings.HasPrefix(pLow, "fs_write:") || strings.HasPrefix(pLow, "fs:write:") {
			idx := strings.LastIndex(pClean, ":")
			dir := strings.TrimSpace(pClean[idx+1:])
			mounts = append(mounts, types.MountSpec{
				Guest:    dir,
				Source:   "host",
				HostPath: dir,
				Mode:     "rw",
			})
		}
	}

	if len(mounts) > 0 || hasNetwork {
		v1.Capabilities = &types.CapabilitiesSpec{}
		if len(mounts) > 0 {
			v1.Capabilities.FS = &types.FSSpec{Mounts: mounts}
		}
		if hasNetwork {
			v1.Capabilities.Network = &types.NetworkSpec{Mode: "fetch"}
		}
	}

	manifest.ManifestV1 = v1

	manifestPath := filepath.Join(skillsDir, skillID+".manifest.json")
	if data, err := json.MarshalIndent(v1, "", "  "); err == nil {
		_ = os.WriteFile(manifestPath, data, 0644)
	}

	return manifest
}

