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

const (
	defaultManifestMemoryMB     = 128
	maxManifestMemoryMB         = 128
	defaultManifestMaxRuntimeMS = 30000
	maxManifestMaxRuntimeMS     = 60000
)

// sanitizeSkillID produces a safe, single-segment lowercase identifier for skill files and DB keys.
func sanitizeSkillID(name string) string {
	id := strings.ToLower(strings.TrimSpace(name))
	id = strings.ReplaceAll(id, " ", "-")
	id = strings.ReplaceAll(id, "/", "-")
	id = strings.ReplaceAll(id, "\\", "-")
	id = strings.ReplaceAll(id, "..", "")
	id = strings.ReplaceAll(id, "\x00", "")
	id = strings.Trim(id, "-.")
	if id == "" {
		return "unnamed-skill"
	}
	return id
}

// BuildAndSaveV1Manifest constructs a formal V1 Skill Manifest, serializes it to disk
// as <id>.manifest.json alongside the .wasm binary, and returns the runtime SkillManifest.
func BuildAndSaveV1Manifest(
	skillsDir, skillID, name, description, wasmPath, checksum, paramsSchema, lang string,
	permissions []string,
) types.SkillManifest {
	return BuildAndSaveV1ManifestWithTemplate(skillsDir, skillID, name, description, wasmPath, checksum, paramsSchema, lang, permissions, nil)
}

// BuildAndSaveV1ManifestWithTemplate constructs a formal V1 Skill Manifest, preserving
// valid fields from templateV1 (mounts, network allowlists, IO output paths, routing, limits)
// while enforcing immutable system metadata and Tier3Unverified fail-closed governance.
func BuildAndSaveV1ManifestWithTemplate(
	skillsDir, skillID, name, description, wasmPath, checksum, paramsSchema, lang string,
	permissions []string,
	templateV1 *types.SkillManifestV1,
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
		Tier:         types.Tier3Unverified,
		IsOfficial:   false,
		Checksum:     checksum,
		Permissions:  append([]string(nil), permissions...),
	}

	var schemaRaw json.RawMessage
	if trimmed := strings.TrimSpace(paramsSchema); trimmed != "" {
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			schemaRaw = json.RawMessage(trimmed)
		} else if quoted, err := json.Marshal(trimmed); err == nil {
			schemaRaw = quoted
		}
	}

	var v1 *types.SkillManifestV1
	if templateV1 != nil {
		// Deep copy to avoid mutating caller-owned pointers/slices.
		if raw, err := json.Marshal(templateV1); err == nil {
			var cloned types.SkillManifestV1
			if err := json.Unmarshal(raw, &cloned); err == nil {
				v1 = &cloned
			}
		}
	}

	if v1 == nil {
		v1 = &types.SkillManifestV1{}
	}

	// Enforce immutable system identity & module integrity fields
	v1.Schema = types.ManifestSchemaV1URI
	v1.ManifestVersion = types.ManifestVersionV1
	v1.ID = skillID
	if strings.TrimSpace(name) != "" {
		v1.Name = name
	} else if v1.Name == "" {
		v1.Name = skillID
	}
	if strings.TrimSpace(v1.Version) == "" {
		v1.Version = "1.0.0"
	}
	manifest.Version = v1.Version
	if strings.TrimSpace(description) != "" {
		v1.Description = description
	} else if v1.Description != "" {
		manifest.Description = v1.Description
	}

	if strings.TrimSpace(v1.Agent.Risk) == "" {
		v1.Agent.Risk = "medium"
	}
	if strings.TrimSpace(v1.Agent.Approval) == "" {
		v1.Agent.Approval = "on_risk"
	}

	v1.Module.File = filepath.Base(wasmPath)
	v1.Module.SHA256 = checksum
	if strings.TrimSpace(v1.Module.ABI) == "" {
		v1.Module.ABI = "wasip1"
	}

	// Ensure IO section has valid defaults while preserving authored file outputs/schemas
	if v1.IO == nil {
		v1.IO = &types.IOSpec{
			Input: types.InputSpec{
				Via:    "stdin",
				Format: "json",
				Schema: schemaRaw,
			},
			Output: types.OutputSpec{
				Via:    "stdout",
				Format: "json",
			},
		}
	} else {
		if strings.TrimSpace(v1.IO.Input.Via) == "" {
			v1.IO.Input.Via = "stdin"
		}
		if strings.TrimSpace(v1.IO.Input.Format) == "" {
			v1.IO.Input.Format = "json"
		}
		if len(v1.IO.Input.Schema) == 0 && len(schemaRaw) > 0 {
			v1.IO.Input.Schema = schemaRaw
		} else if len(v1.IO.Input.Schema) > 0 && strings.TrimSpace(manifest.ParamsSchema) == "" {
			manifest.ParamsSchema = string(v1.IO.Input.Schema)
		}
		if strings.TrimSpace(v1.IO.Output.Via) == "" {
			v1.IO.Output.Via = "stdout"
		}
		if strings.TrimSpace(v1.IO.Output.Format) == "" {
			v1.IO.Output.Format = "json"
		}
	}

	// Clamp resource limits to safe sandbox ceilings
	if v1.Limits == nil {
		v1.Limits = &types.LimitsSpec{
			MemoryMB:     defaultManifestMemoryMB,
			MaxRuntimeMS: defaultManifestMaxRuntimeMS,
		}
	} else {
		if v1.Limits.MemoryMB <= 0 || v1.Limits.MemoryMB > maxManifestMemoryMB {
			v1.Limits.MemoryMB = defaultManifestMemoryMB
		}
		if v1.Limits.MaxRuntimeMS <= 0 {
			v1.Limits.MaxRuntimeMS = defaultManifestMaxRuntimeMS
		} else if v1.Limits.MaxRuntimeMS > maxManifestMaxRuntimeMS {
			v1.Limits.MaxRuntimeMS = maxManifestMaxRuntimeMS
		}
	}

	// Propagate routing metadata onto SkillManifest for Intent Resolver
	if v1.Routing != nil {
		if len(v1.Routing.Keywords) > 0 {
			manifest.Keywords = append([]string(nil), v1.Routing.Keywords...)
		}
		if len(v1.Routing.Tags) > 0 {
			manifest.Tags = append([]string(nil), v1.Routing.Tags...)
		}
	}

	// If no template capabilities were provided, synthesize them from legacy/shorthand permissions
	hasTemplateCaps := templateV1 != nil && v1.Capabilities != nil
	if !hasTemplateCaps {
		mounts, hasNetwork := parsePermissionStrings(permissions)
		if len(mounts) > 0 || hasNetwork {
			v1.Capabilities = &types.CapabilitiesSpec{}
			if len(mounts) > 0 {
				v1.Capabilities.FS = &types.FSSpec{Mounts: mounts}
			}
			if hasNetwork {
				v1.Capabilities.Network = &types.NetworkSpec{Mode: "fetch"}
			}
		}
	} else {
		// Keep manifest.Permissions consistent with preserved V1 capabilities
		manifest.Permissions = syncPermissionsFromV1(manifest.Permissions, v1.Capabilities)
	}

	manifest.ManifestV1 = v1

	manifestPath := filepath.Join(skillsDir, skillID+".manifest.json")
	if data, err := json.MarshalIndent(v1, "", "  "); err == nil {
		_ = os.WriteFile(manifestPath, data, 0600)
	}

	return manifest
}

// parsePermissionStrings translates legacy and shorthand permission entries
// (network, fs_ro:, fs_rw:, fs_read:, fs_write:, fs:ro:, fs:rw:, fs:read:, fs:write:)
// into SkillManifestV1 MountSpecs and network flags.
func parsePermissionStrings(permissions []string) ([]types.MountSpec, bool) {
	var mounts []types.MountSpec
	hasNetwork := false

	for _, p := range permissions {
		pClean := strings.TrimSpace(p)
		pLow := strings.ToLower(pClean)
		if pLow == "" {
			continue
		}
		if pLow == "network" || strings.HasPrefix(pLow, "network:") || strings.HasPrefix(pLow, "net:") {
			hasNetwork = true
			continue
		}

		var mode string
		var rawDir string
		switch {
		case strings.HasPrefix(pLow, "fs_ro:"):
			mode, rawDir = "ro", pClean[len("fs_ro:"):]
		case strings.HasPrefix(pLow, "fs:ro:"):
			mode, rawDir = "ro", pClean[len("fs:ro:"):]
		case strings.HasPrefix(pLow, "fs_read:"):
			mode, rawDir = "ro", pClean[len("fs_read:"):]
		case strings.HasPrefix(pLow, "fs:read:"):
			mode, rawDir = "ro", pClean[len("fs:read:"):]
		case strings.HasPrefix(pLow, "filesystem:read:"):
			mode, rawDir = "ro", pClean[len("filesystem:read:"):]
		case strings.HasPrefix(pLow, "fs_rw:"):
			mode, rawDir = "rw", pClean[len("fs_rw:"):]
		case strings.HasPrefix(pLow, "fs:rw:"):
			mode, rawDir = "rw", pClean[len("fs:rw:"):]
		case strings.HasPrefix(pLow, "fs_write:"):
			mode, rawDir = "rw", pClean[len("fs_write:"):]
		case strings.HasPrefix(pLow, "fs:write:"):
			mode, rawDir = "rw", pClean[len("fs:write:"):]
		case strings.HasPrefix(pLow, "filesystem:write:"):
			mode, rawDir = "rw", pClean[len("filesystem:write:"):]
		default:
			continue
		}

		dir := strings.TrimSpace(rawDir)
		if dir == "" {
			continue
		}
		mounts = append(mounts, buildMountFromShorthand(dir, mode))
	}

	return mounts, hasNetwork
}

// buildMountFromShorthand maps well-known virtual guest mount targets to their
// sandboxed V1 storage pools instead of defaulting to blocked "host" mounts.
func buildMountFromShorthand(dir, mode string) types.MountSpec {
	norm := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(dir)), "/")
	switch norm {
	case "/workspace":
		return types.MountSpec{Guest: "/workspace", Source: "workspace", Mode: mode}
	case "/input":
		return types.MountSpec{Guest: "/input", Source: "input", Mode: mode}
	case "/output":
		return types.MountSpec{Guest: "/output", Source: "output", Mode: mode}
	case "/skill_data", "/cache":
		return types.MountSpec{Guest: dir, Source: "skill_data", Mode: mode}
	case "/tmp", "/ephemeral":
		return types.MountSpec{Guest: dir, Source: "ephemeral", Mode: mode}
	default:
		return types.MountSpec{
			Guest:    dir,
			Source:   "host",
			HostPath: dir,
			Mode:     mode,
		}
	}
}

func syncPermissionsFromV1(existing []string, caps *types.CapabilitiesSpec) []string {
	seen := make(map[string]struct{}, len(existing))
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	for _, p := range existing {
		add(p)
	}
	if caps == nil {
		return out
	}
	if caps.FS != nil {
		for _, m := range caps.FS.Mounts {
			mode := strings.ToLower(strings.TrimSpace(m.Mode))
			if mode == "" {
				mode = "ro"
			}
			add("fs_" + mode + ":" + m.Guest)
		}
	}
	if caps.Network != nil && caps.Network.Mode != "" && strings.ToLower(caps.Network.Mode) != "none" {
		add("network")
	}
	return out
}

// dryRunSandboxManifest strips all host/network capabilities for dry-run verification so
// freshly compiled Tier3Unverified modules are smoke-tested in a zero-capability sandbox.
func dryRunSandboxManifest(m types.SkillManifest) types.SkillManifest {
	dryRun := m
	dryRun.Permissions = nil
	if m.ManifestV1 != nil {
		v1Copy := *m.ManifestV1
		v1Copy.Capabilities = nil
		dryRun.ManifestV1 = &v1Copy
	}
	return dryRun
}
