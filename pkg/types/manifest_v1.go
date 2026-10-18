// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package types

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

// ManifestVersionV1 is the required manifest_version identifier for V1 skill manifests.
const ManifestVersionV1 = "skill-manifest/1"

// ManifestSchemaV1URI is the canonical URI for the V1 JSON schema.
const ManifestSchemaV1URI = "https://vraxter.io/schemas/skill-manifest.v1.json"

var (
	validIDRegex      = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	validSemverRegex  = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)
	validSHA256Regex  = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
)

// SkillManifestV1 represents the formal capability-based specification (skill-manifest/1).
type SkillManifestV1 struct {
	Schema          string            `json:"$schema,omitempty"`
	ManifestVersion string            `json:"manifest_version"`
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Description     string            `json:"description"`
	Agent           AgentGovernance   `json:"agent,omitempty"`
	Module          ModuleSpec        `json:"module"`
	Routing         *RoutingSpec      `json:"routing,omitempty"`
	IO              *IOSpec           `json:"io,omitempty"`
	Capabilities    *CapabilitiesSpec `json:"capabilities,omitempty"`
	Limits          *LimitsSpec       `json:"limits,omitempty"`
}

// AgentGovernance controls execution policy and approval gates.
type AgentGovernance struct {
	Risk     string `json:"risk,omitempty"`     // "low", "medium", "high", "critical"
	Approval string `json:"approval,omitempty"` // "never", "always", "on_risk", "conditional"
}

// ModuleSpec defines the WASM compilation target and integrity verification.
type ModuleSpec struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256,omitempty"`
	ABI    string `json:"abi,omitempty"` // "wasip1", "wasip2", "generic"
}

// RoutingSpec contains metadata for intent resolution and fast-path matching.
type RoutingSpec struct {
	Keywords   []string `json:"keywords,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Examples   []string `json:"examples,omitempty"`
	ParamRegex string   `json:"param_regex,omitempty"`
}

// IOSpec configures input and output channels.
type IOSpec struct {
	Input  InputSpec  `json:"input,omitempty"`
	Output OutputSpec `json:"output,omitempty"`
}

// InputSpec configures how input parameters are passed into the WASM module.
type InputSpec struct {
	Via    string          `json:"via,omitempty"`    // "stdin", "args", "env"
	Format string          `json:"format,omitempty"` // "json", "text", "binary"
	Schema json.RawMessage `json:"schema,omitempty"`
}

// OutputSpec configures how output is retrieved from the WASM module.
type OutputSpec struct {
	Via    string `json:"via,omitempty"`    // "stdout", "file"
	Format string `json:"format,omitempty"` // "json", "text", "binary"
	Path   string `json:"path,omitempty"`   // Required when via is "file", e.g. "/output/report.json"
}

// CapabilitiesSpec declares the explicit permissions requested by the skill.
type CapabilitiesSpec struct {
	FS      *FSSpec           `json:"fs,omitempty"`
	Network *NetworkSpec      `json:"network,omitempty"`
	Env     *EnvSpec          `json:"env,omitempty"`
	Clock   *ClockSpec        `json:"clock,omitempty"`
	Random  *bool             `json:"random,omitempty"`
}

// FSSpec configures guest filesystem isolation and mounts.
type FSSpec struct {
	Mounts []MountSpec `json:"mounts,omitempty"`
}

// MountSpec defines an individual filesystem mount into the guest sandbox.
type MountSpec struct {
	Guest           string   `json:"guest"`            // E.g. "/input", "/output", "/tmp"
	Source          string   `json:"source"`           // "workspace", "input", "output", "skill_data", "ephemeral", "host"
	HostPath        string   `json:"host_path,omitempty"`
	Subpath         string   `json:"subpath,omitempty"`
	Mode            string   `json:"mode"`             // "ro", "rw"
	AllowExtensions []string `json:"allow_extensions,omitempty"`
	MaxFileMB       float64  `json:"max_file_mb,omitempty"`
	MaxTotalMB      float64  `json:"max_total_mb,omitempty"`
	MaxFiles        int      `json:"max_files,omitempty"`
	AllowDelete     bool     `json:"allow_delete,omitempty"`
}

// NetworkSpec defines outbound networking constraints.
type NetworkSpec struct {
	Mode            string   `json:"mode,omitempty"` // "none", "fetch", "egress"
	AllowedHosts    []string `json:"allowed_hosts,omitempty"`
	AllowedPorts    []int    `json:"allowed_ports,omitempty"`
	AllowedSchemes  []string `json:"allowed_schemes,omitempty"`
	AllowRawSockets bool     `json:"allow_raw_sockets,omitempty"`
}

// EnvSpec defines environment variables exposed to the module.
type EnvSpec struct {
	Vars map[string]string `json:"vars,omitempty"`
}

// ClockSpec defines clock accuracy and monotonicity permissions.
type ClockSpec struct {
	Wall      bool `json:"wall,omitempty"`
	Monotonic bool `json:"monotonic,omitempty"`
}

// LimitsSpec specifies runtime resource limits.
type LimitsSpec struct {
	MemoryMB     int `json:"memory_mb,omitempty"`     // 1 to 2048 MB
	MaxRuntimeMS int `json:"max_runtime_ms,omitempty"` // In milliseconds
}

// Validate checks the structural validity of the manifest against the V1 specification.
func (m *SkillManifestV1) Validate() error {
	if m.ManifestVersion != ManifestVersionV1 {
		return fmt.Errorf("invalid manifest_version: expected '%s', got '%s'", ManifestVersionV1, m.ManifestVersion)
	}

	id := strings.TrimSpace(m.ID)
	if id == "" {
		return fmt.Errorf("manifest id is required")
	}
	if !validIDRegex.MatchString(id) {
		return fmt.Errorf("invalid manifest id '%s': must contain only letters, numbers, dots, underscores, or hyphens", id)
	}

	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("manifest name is required")
	}

	version := strings.TrimSpace(m.Version)
	if version == "" {
		return fmt.Errorf("manifest version is required")
	}
	if !validSemverRegex.MatchString(version) {
		return fmt.Errorf("invalid semantic version '%s'", version)
	}

	if strings.TrimSpace(m.Description) == "" {
		return fmt.Errorf("manifest description is required")
	}

	if strings.TrimSpace(m.Module.File) == "" {
		return fmt.Errorf("module.file is required")
	}

	if m.Module.SHA256 != "" && !validSHA256Regex.MatchString(m.Module.SHA256) {
		return fmt.Errorf("invalid module.sha256 '%s': must be a 64-character hex string", m.Module.SHA256)
	}

	if m.Module.ABI != "" {
		switch m.Module.ABI {
		case "wasip1", "wasip2", "generic":
		default:
			return fmt.Errorf("invalid module.abi '%s': must be 'wasip1', 'wasip2', or 'generic'", m.Module.ABI)
		}
	}

	if m.Agent.Risk != "" {
		switch strings.ToLower(m.Agent.Risk) {
		case "low", "medium", "high", "critical":
		default:
			return fmt.Errorf("invalid agent.risk '%s': must be 'low', 'medium', 'high', or 'critical'", m.Agent.Risk)
		}
	}

	if m.Agent.Approval != "" {
		switch strings.ToLower(m.Agent.Approval) {
		case "never", "always", "on_risk", "conditional":
		default:
			return fmt.Errorf("invalid agent.approval '%s': must be 'never', 'always', 'on_risk', or 'conditional'", m.Agent.Approval)
		}
	}

	if m.IO != nil {
		if m.IO.Input.Via != "" {
			switch m.IO.Input.Via {
			case "stdin", "args", "env":
			default:
				return fmt.Errorf("invalid io.input.via '%s': must be 'stdin', 'args', or 'env'", m.IO.Input.Via)
			}
		}

		if m.IO.Output.Via != "" {
			switch m.IO.Output.Via {
			case "stdout", "file":
			default:
				return fmt.Errorf("invalid io.output.via '%s': must be 'stdout' or 'file'", m.IO.Output.Via)
			}
			if m.IO.Output.Via == "file" {
				if strings.TrimSpace(m.IO.Output.Path) == "" {
					return fmt.Errorf("io.output.path is required when io.output.via is 'file'")
				}
				if !strings.HasPrefix(m.IO.Output.Path, "/") {
					return fmt.Errorf("io.output.path must be an absolute guest path starting with '/', got '%s'", m.IO.Output.Path)
				}
			}
		}
	}

	if m.Capabilities != nil && m.Capabilities.FS != nil {
		for i, mount := range m.Capabilities.FS.Mounts {
			if !strings.HasPrefix(mount.Guest, "/") {
				return fmt.Errorf("capabilities.fs.mounts[%d].guest must be an absolute path starting with '/', got '%s'", i, mount.Guest)
			}
			switch mount.Source {
			case "workspace", "input", "output", "skill_data", "ephemeral", "host":
			default:
				return fmt.Errorf("capabilities.fs.mounts[%d].source '%s' is invalid", i, mount.Source)
			}
			if mount.Source == "host" && strings.TrimSpace(mount.HostPath) == "" {
				return fmt.Errorf("capabilities.fs.mounts[%d].host_path is required when source is 'host'", i)
			}
			switch mount.Mode {
			case "ro", "rw":
			default:
				return fmt.Errorf("capabilities.fs.mounts[%d].mode '%s' must be 'ro' or 'rw'", i, mount.Mode)
			}
		}
	}

	if m.Limits != nil {
		if m.Limits.MemoryMB < 0 || m.Limits.MemoryMB > 2048 {
			return fmt.Errorf("limits.memory_mb (%d) must be between 1 and 2048", m.Limits.MemoryMB)
		}
		if m.Limits.MaxRuntimeMS < 0 || m.Limits.MaxRuntimeMS > 300000 {
			return fmt.Errorf("limits.max_runtime_ms (%d) must be between 100 and 300000", m.Limits.MaxRuntimeMS)
		}
	}

	return nil
}

// ToLegacyManifest converts a SkillManifestV1 into the runtime SkillManifest struct.
func (m *SkillManifestV1) ToLegacyManifest() SkillManifest {
	legacy := SkillManifest{
		ID:          m.ID,
		Name:        m.Name,
		Description: m.Description,
		Version:     m.Version,
		Command:     m.Module.File,
		Checksum:    m.Module.SHA256,
		Engine:      "wasm",
		Tier:        Tier2CommunityVerified,
		ManifestV1:  m,
	}

	if m.Limits != nil && m.Limits.MaxRuntimeMS > 0 {
		legacy.TimeoutSeconds = int(math.Ceil(float64(m.Limits.MaxRuntimeMS) / 1000.0))
	}

	if m.Routing != nil {
		legacy.Keywords = m.Routing.Keywords
		legacy.Tags = m.Routing.Tags
		legacy.Examples = m.Routing.Examples
		legacy.ParamRegex = m.Routing.ParamRegex
	}

	if m.IO != nil && len(m.IO.Input.Schema) > 0 {
		trimmed := strings.TrimSpace(string(m.IO.Input.Schema))
		if strings.HasPrefix(trimmed, "\"") {
			var unquoted string
			if err := json.Unmarshal(m.IO.Input.Schema, &unquoted); err == nil {
				legacy.ParamsSchema = unquoted
			} else {
				legacy.ParamsSchema = trimmed
			}
		} else {
			legacy.ParamsSchema = trimmed
		}
	}

	// Translate capabilities to legacy permissions strings for backward compatibility
	var perms []string
	if m.Capabilities != nil {
		if m.Capabilities.FS != nil {
			for _, mount := range m.Capabilities.FS.Mounts {
				perms = append(perms, fmt.Sprintf("fs_%s:%s", mount.Mode, mount.Guest))
			}
		}
		if m.Capabilities.Network != nil && m.Capabilities.Network.Mode != "" && m.Capabilities.Network.Mode != "none" {
			perms = append(perms, "network")
		}
	}
	legacy.Permissions = perms

	return legacy
}

// RequiresApproval evaluates whether this skill requires user approval before execution.
func (m *SkillManifest) RequiresApproval(globalRequireApproval bool) bool {
	if m.ManifestV1 != nil && m.ManifestV1.Agent.Approval != "" {
		switch strings.ToLower(m.ManifestV1.Agent.Approval) {
		case "never":
			return false
		case "always":
			return true
		case "on_risk":
			risk := strings.ToLower(m.ManifestV1.Agent.Risk)
			if risk == "high" || risk == "critical" {
				return true
			}
			return globalRequireApproval
		case "conditional":
			return globalRequireApproval
		}
	}
	return globalRequireApproval
}

// GetExecutionTimeout returns the effective timeout duration for the skill.
func (m *SkillManifest) GetExecutionTimeout(defaultTimeout time.Duration) time.Duration {
	if m.ManifestV1 != nil && m.ManifestV1.Limits != nil && m.ManifestV1.Limits.MaxRuntimeMS > 0 {
		return time.Duration(m.ManifestV1.Limits.MaxRuntimeMS) * time.Millisecond
	}
	if m.TimeoutSeconds > 0 {
		return time.Duration(m.TimeoutSeconds) * time.Second
	}
	if defaultTimeout > 0 {
		return defaultTimeout
	}
	return 30 * time.Second
}

// GetMemoryLimitPages returns the wazero memory limit in 64KB pages.
func (m *SkillManifest) GetMemoryLimitPages(defaultPages uint32) uint32 {
	if m.ManifestV1 != nil && m.ManifestV1.Limits != nil && m.ManifestV1.Limits.MemoryMB > 0 {
		return uint32(m.ManifestV1.Limits.MemoryMB * 16)
	}
	if defaultPages > 0 {
		return defaultPages
	}
	return 2048 // 128MB default
}

