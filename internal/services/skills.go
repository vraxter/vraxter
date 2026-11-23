// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/internal/utils"
	"github.com/vraxter/vraxter/pkg/types"
)

// SkillService handles installation and registration of system skills
type SkillService struct {
	repo                *db.SkillRepository
	skillsDir           string
	trustedSignersDir   string
	requireSignedSkills bool
	mu                  sync.RWMutex
}

func NewSkillService(repo *db.SkillRepository, skillsDir string) *SkillService {
	defaultTrustDir := ""
	if strings.TrimSpace(skillsDir) != "" {
		defaultTrustDir = filepath.Join(filepath.Dir(filepath.Clean(skillsDir)), "trusted_signers")
	}
	return &SkillService{
		repo:              repo,
		skillsDir:         skillsDir,
		trustedSignersDir: defaultTrustDir,
	}
}

// SetTrustedSignersDir configures the directory containing trusted Ed25519 public keys.
func (s *SkillService) SetTrustedSignersDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trustedSignersDir = strings.TrimSpace(dir)
}

// TrustedSignersDir returns the configured directory of trusted Ed25519 public keys.
func (s *SkillService) TrustedSignersDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.trustedSignersDir
}

// SetRequireSignedSkills enforces that every installed skill carries a valid Ed25519 signature.
func (s *SkillService) SetRequireSignedSkills(require bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requireSignedSkills = require
}

// RequireSignedSkills reports whether unsigned skills are blocked at installation time.
func (s *SkillService) RequireSignedSkills() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.requireSignedSkills
}

// InstallSkill persists a new skill to the database after copying the binary to the internal storage
func (s *SkillService) InstallSkill(m types.SkillManifest) error {
	// 1. Ensure the destination directory exists
	if err := os.MkdirAll(s.skillsDir, 0700); err != nil {
		return fmt.Errorf("failed to ensure skills directory: %w", err)
	}

	// 2. Identify source and destination
	srcPath := m.Command
	filename := m.ID + filepath.Ext(srcPath)
	destPath := filepath.Join(s.skillsDir, filename)

	// 2b. Static WASM bytecode import verification (Zero Over-Privilege) before installation
	if strings.ToLower(filepath.Ext(srcPath)) == ".wasm" && (m.Engine == "" || m.Engine == "wasm") {
		if err := skills.VerifyWASMFile(context.Background(), srcPath, m); err != nil {
			return fmt.Errorf("static WASM capability verification failed: %w", err)
		}
	}

	// 2c. Calculate Integrity Checksum before copying
	checksum, err := s.CalculateHash(srcPath)
	if err != nil {
		return fmt.Errorf("failed to verify skill binary size/hash: %w", err)
	}
	m.Checksum = checksum

	// 2d. Ensure ManifestV1 is synthesized if missing so canonical verification and persistence are consistent
	if m.ManifestV1 == nil {
		var schemaRaw json.RawMessage
		if trimmed := strings.TrimSpace(m.ParamsSchema); trimmed != "" {
			if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
				schemaRaw = json.RawMessage(trimmed)
			} else if quoted, qErr := json.Marshal(trimmed); qErr == nil {
				schemaRaw = quoted
			}
		}
		m.ManifestV1 = &types.SkillManifestV1{
			Schema:          types.ManifestSchemaV1URI,
			ManifestVersion: types.ManifestVersionV1,
			ID:              m.ID,
			Name:            m.Name,
			Version:         m.Version,
			Description:     m.Description,
			Agent: types.AgentGovernance{
				Risk:     "medium",
				Approval: "on_risk",
			},
			Module: types.ModuleSpec{
				File:   filepath.Base(destPath),
				SHA256: m.Checksum,
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
		if m.TimeoutSeconds > 0 {
			m.ManifestV1.Limits.MaxRuntimeMS = m.TimeoutSeconds * 1000
		}
	} else {
		m.ManifestV1.Module.File = filepath.Base(destPath)
		m.ManifestV1.Module.SHA256 = m.Checksum
	}

	// 2e. Pre-installation Ed25519 signature verification against trusted_signers/
	sigPath := security.DiscoverSkillSignaturePath(srcPath, "", m.ID)
	if sigPath != "" {
		canonicalBytes, err := security.CanonicalizeSkillManifestV1(m.ManifestV1)
		if err != nil {
			return fmt.Errorf("failed canonicalizing skill manifest for signature verification: %w", err)
		}
		store := security.NewTrustedSignerStore(s.TrustedSignersDir())
		signer, _, err := store.VerifySkillAgainstStore(srcPath, canonicalBytes, sigPath)
		if err != nil {
			return err
		}
		m.Tier = signer.TrustTier
		m.IsOfficial = (signer.TrustTier == types.Tier1Official)
	} else if s.RequireSignedSkills() {
		return fmt.Errorf("VRAXTER SHIELD: skill '%s' is unsigned (.sig missing) and require_signed_skills is enabled (Tier3Unverified blocked)", m.ID)
	}

	// 3. Copy binary atomically if needed
	if srcPath != destPath {
		if err := copyFile(srcPath, destPath); err != nil {
			return fmt.Errorf("failed to copy skill binary: %w", err)
		}
	}
	m.Command = destPath

	// 4. Copy verified signature envelope atomically if present
	if sigPath != "" {
		sigDest := filepath.Join(s.skillsDir, m.ID+".sig")
		if filepath.Clean(sigPath) != filepath.Clean(sigDest) {
			if err := utils.AtomicCopyFile(sigPath, sigDest, 0600); err != nil {
				return fmt.Errorf("failed to persist skill signature: %w", err)
			}
		}
	}

	// 5. Persist <id>.manifest.json atomically
	manifestDest := filepath.Join(s.skillsDir, m.ID+".manifest.json")
	if manifestData, err := json.MarshalIndent(m.ManifestV1, "", "  "); err == nil {
		if err := utils.AtomicWriteFile(manifestDest, manifestData, 0600); err != nil {
			return fmt.Errorf("failed to persist skill manifest: %w", err)
		}
	}

	return s.repo.UpsertSkill(m)
}


func (s *SkillService) CalculateHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if fi.Size() > skills.MaxWASMBinaryBytes {
		return "", fmt.Errorf("WASM binary %s exceeds maximum allowed size (%d > %d bytes)", path, fi.Size(), skills.MaxWASMBinaryBytes)
	}

	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, skills.MaxWASMBinaryBytes+1))
	if err != nil {
		return "", err
	}
	if n > skills.MaxWASMBinaryBytes {
		return "", fmt.Errorf("WASM binary %s exceeds maximum allowed size (%d bytes)", path, skills.MaxWASMBinaryBytes)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	// WASM bytecode is executed by Wazero, never directly by the OS; copy atomically with 0600 permissions.
	return utils.AtomicCopyFileBounded(src, dst, skills.MaxWASMBinaryBytes, 0600)
}


// LoadAllIntoRegistry fetches all skills from DB and fills the memory registry
func (s *SkillService) LoadAllIntoRegistry(reg *skills.Registry) error {
	all, err := s.repo.GetAllSkills()
	if err != nil {
		return err
	}
	for _, sm := range all {
		reg.Register(sm)
	}
	log.Printf("Skills: Loaded %d tools from DB into registry", len(all))
	return nil
}

// ListSkills returns all installed metadata
func (s *SkillService) ListSkills() ([]types.SkillManifest, error) {
	return s.repo.GetAllSkills()
}

func (s *SkillService) GetSkill(id string) (types.SkillManifest, error) {
	all, err := s.ListSkills()
	if err != nil {
		return types.SkillManifest{}, err
	}
	for _, sm := range all {
		if sm.ID == id {
			return sm, nil
		}
	}
	return types.SkillManifest{}, fmt.Errorf("skill '%s' not found", id)
}

func (s *SkillService) UpdateSkill(m types.SkillManifest) error {
	return s.repo.UpsertSkill(m)
}

func (s *SkillService) DeleteSkill(id string) error {
	// Ideally we would also delete the binary here
	return s.repo.DeleteSkill(id)
}

type rawManifest struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Desc           string          `json:"desc"`
	Version        string          `json:"version"`
	Language       string          `json:"language"`
	Tier           types.SkillTier `json:"tier"`
	Permissions    []string        `json:"permissions"`
	TimeoutSeconds int             `json:"timeout_seconds"`
	Keywords       []string        `json:"keywords"`
	Examples       []string        `json:"examples"`
	Tags           []string        `json:"tags"`
	ParamRegex     string          `json:"param_regex"`
	ParamsSchema   json.RawMessage `json:"params_schema"`
	Parameters     json.RawMessage `json:"parameters"`
}

func parseManifestData(data []byte, defaultID string) (types.SkillManifest, error) {
	// First: Check if this is a formal skill-manifest/1
	var probe struct {
		ManifestVersion string `json:"manifest_version"`
	}
	_ = json.Unmarshal(data, &probe)

	if probe.ManifestVersion == types.ManifestVersionV1 {
		var v1 types.SkillManifestV1
		if err := json.Unmarshal(data, &v1); err != nil {
			return types.SkillManifest{}, fmt.Errorf("failed to parse skill-manifest/1: %w", err)
		}
		if strings.TrimSpace(v1.ID) == "" {
			v1.ID = defaultID
		}
		if strings.TrimSpace(v1.Name) == "" {
			v1.Name = v1.ID
		}
		if strings.TrimSpace(v1.Version) == "" {
			v1.Version = "1.0.0"
		}
		if strings.TrimSpace(v1.Description) == "" {
			v1.Description = fmt.Sprintf("Injected skill (%s)", v1.ID)
		}
		if strings.TrimSpace(v1.Module.File) == "" {
			v1.Module.File = defaultID + ".wasm"
		}
		if err := v1.Validate(); err != nil {
			return types.SkillManifest{}, fmt.Errorf("invalid skill-manifest/1 specification: %w", err)
		}
		m := v1.ToLegacyManifest()
		m.ManifestV1 = &v1
		return m, nil
	}

	var raw rawManifest
	if err := json.Unmarshal(data, &raw); err != nil {
		return types.SkillManifest{}, fmt.Errorf("failed to parse manifest json: %w", err)
	}

	id := strings.TrimSpace(raw.ID)
	if id == "" {
		id = defaultID
	}
	name := strings.TrimSpace(raw.Name)
	if name == "" {
		name = id
	}
	desc := strings.TrimSpace(raw.Description)
	if desc == "" {
		desc = strings.TrimSpace(raw.Desc)
	}
	if desc == "" {
		desc = fmt.Sprintf("Injected local skill (%s)", id)
	}
	version := strings.TrimSpace(raw.Version)
	if version == "" {
		version = "1.0.0"
	}
	// External/injected skills always start as Tier3Unverified until explicitly trusted by an operator.
	tier := types.Tier3Unverified

	// Handle parameters schema flexibly: could be raw string or JSON object/array
	schemaBytes := raw.ParamsSchema
	if len(schemaBytes) == 0 {
		schemaBytes = raw.Parameters
	}
	var paramsSchemaStr string
	if len(schemaBytes) > 0 {
		trimmed := strings.TrimSpace(string(schemaBytes))
		if strings.HasPrefix(trimmed, "\"") {
			var unquoted string
			if err := json.Unmarshal(schemaBytes, &unquoted); err == nil {
				paramsSchemaStr = unquoted
			} else {
				paramsSchemaStr = trimmed
			}
		} else {
			paramsSchemaStr = trimmed
		}
	}

	return types.SkillManifest{
		ID:             id,
		Name:           name,
		Description:    desc,
		Version:        version,
		Language:       raw.Language,
		Tier:           tier,
		Permissions:    raw.Permissions,
		TimeoutSeconds: raw.TimeoutSeconds,
		Keywords:       raw.Keywords,
		Examples:       raw.Examples,
		Tags:           raw.Tags,
		ParamRegex:     raw.ParamRegex,
		ParamsSchema:   paramsSchemaStr,
	}, nil
}

func validateWASMHeader(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	header := make([]byte, 4)
	n, err := io.ReadFull(f, header)
	if err != nil || n < 4 {
		return fmt.Errorf("file is too short to be a valid WebAssembly binary")
	}
	if header[0] != 0x00 || header[1] != 0x61 || header[2] != 0x73 || header[3] != 0x6d {
		return fmt.Errorf("file is not a valid WebAssembly binary (missing '\\x00asm' magic header)")
	}
	return nil
}

// RegisterFromWASM imports an offline .wasm skill file into the engine, automatically discovering
// adjacent manifests or parsing an optional custom manifest, and persisting the skill.
func (s *SkillService) RegisterFromWASM(path string, manifestPath string) (types.SkillManifest, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	fi, err := os.Stat(absPath)
	if err != nil {
		return types.SkillManifest{}, fmt.Errorf("skill path not found: %w", err)
	}

	// 0. If path is a json file, treat it as the manifest and find the wasm binary
	if !fi.IsDir() && strings.ToLower(filepath.Ext(absPath)) == ".json" {
		manifestPath = absPath
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			return types.SkillManifest{}, fmt.Errorf("failed to read manifest file: %w", err)
		}
		var probe struct {
			Module struct {
				File string `json:"file"`
			} `json:"module"`
			Command string `json:"command"`
		}
		_ = json.Unmarshal(manifestData, &probe)
		wasmFile := probe.Module.File
		if wasmFile == "" {
			wasmFile = probe.Command
		}
		if wasmFile == "" {
			wasmFile = strings.TrimSuffix(filepath.Base(manifestPath), filepath.Ext(manifestPath)) + ".wasm"
		}
		manifestDir := filepath.Clean(filepath.Dir(manifestPath))
		resolvedWASM := filepath.Clean(filepath.Join(manifestDir, wasmFile))
		relWASM, relErr := filepath.Rel(manifestDir, resolvedWASM)
		if relErr != nil || relWASM == ".." || strings.HasPrefix(relWASM, ".."+string(os.PathSeparator)) || filepath.IsAbs(wasmFile) {
			return types.SkillManifest{}, fmt.Errorf("invalid wasm module path '%s': escapes manifest directory", wasmFile)
		}
		if _, err := os.Stat(resolvedWASM); err != nil {
			return types.SkillManifest{}, fmt.Errorf("wasm module '%s' referenced in manifest was not found at '%s'", wasmFile, resolvedWASM)
		}
		absPath = resolvedWASM
	}

	// 1. If path is a directory, auto-detect the single .wasm file and optional manifest
	if fi.IsDir() {
		entries, err := os.ReadDir(absPath)
		if err != nil {
			return types.SkillManifest{}, fmt.Errorf("failed to read skill directory: %w", err)
		}
		var wasmFiles []string
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".wasm") {
				wasmFiles = append(wasmFiles, filepath.Join(absPath, entry.Name()))
			}
		}
		if len(wasmFiles) == 0 {
			return types.SkillManifest{}, fmt.Errorf("no .wasm file found in directory: %s", absPath)
		}
		if len(wasmFiles) > 1 {
			return types.SkillManifest{}, fmt.Errorf("multiple .wasm files found in directory (%d found), please specify the exact .wasm file", len(wasmFiles))
		}
		if manifestPath == "" {
			dirManifest := filepath.Join(absPath, "manifest.json")
			if _, err := os.Stat(dirManifest); err == nil {
				manifestPath = dirManifest
			}
		}
		absPath = wasmFiles[0]
	}

	// 2. Validate file extension and binary header
	if strings.ToLower(filepath.Ext(absPath)) != ".wasm" {
		return types.SkillManifest{}, fmt.Errorf("file must have a .wasm extension, got '%s'", filepath.Ext(absPath))
	}
	if err := validateWASMHeader(absPath); err != nil {
		return types.SkillManifest{}, err
	}

	baseName := filepath.Base(absPath)
	id := strings.TrimSuffix(baseName, filepath.Ext(baseName))

	// 3. Auto-discover adjacent manifest if not explicitly provided
	if manifestPath == "" {
		adj1 := filepath.Join(filepath.Dir(absPath), "manifest.json")
		adj2 := filepath.Join(filepath.Dir(absPath), id+".manifest.json")
		adj3 := filepath.Join(filepath.Dir(absPath), id+".json")
		if _, err := os.Stat(adj1); err == nil {
			manifestPath = adj1
		} else if _, err := os.Stat(adj2); err == nil {
			manifestPath = adj2
		} else if _, err := os.Stat(adj3); err == nil {
			manifestPath = adj3
		}
	}

	var m types.SkillManifest
	if manifestPath != "" {
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return types.SkillManifest{}, fmt.Errorf("failed to read manifest file '%s': %w", manifestPath, err)
		}
		m, err = parseManifestData(data, id)
		if err != nil {
			return types.SkillManifest{}, err
		}
	} else {
		m = types.SkillManifest{
			ID:          id,
			Name:        id,
			Description: fmt.Sprintf("Injected local skill (%s)", id),
			Version:     "1.0.0",
			Tier:        types.Tier3Unverified,
		}
	}

	// 4. Enforce security constraints (external/injected skills always start unverified)
	m.Command = absPath
	m.Engine = "wasm"
	m.IsOfficial = false
	m.Tier = types.Tier3Unverified

	// 5. Install (copy to internal skills storage, calculate SHA-256 hash, persist to DB)
	if err := s.InstallSkill(m); err != nil {
		return types.SkillManifest{}, err
	}

	// Re-fetch to get updated Command path and calculated Checksum
	installed, err := s.GetSkill(m.ID)
	if err == nil {
		m = installed
	}

	return m, nil
}
