// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vraxter/vraxter/internal/utils"
	"github.com/vraxter/vraxter/pkg/types"
)

const (
	// SkillSignatureDomain is the domain separator prepended to all V1 skill signing payloads.
	SkillSignatureDomain = "VRAXTER-SKILL-V1"
	// SkillSignatureVersion identifies the envelope format version in <skill-id>.sig files.
	SkillSignatureVersion = "vraxter-sig/1"
	// SkillSignatureAlgorithm is the mandatory asymmetric signature algorithm for skills.
	SkillSignatureAlgorithm = "ed25519"
	// maxSignableWASMBytes caps the size of a .wasm binary read during signing/verification (50 MB).
	maxSignableWASMBytes int64 = 50 * 1024 * 1024
)

// Ed25519KeyPair holds an Ed25519 signing keypair and its deterministic 16-hex-char KeyID.
type Ed25519KeyPair struct {
	KeyID      string
	SignerName string
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// SkillSignatureEnvelope is the serialized JSON structure stored in <skill-id>.sig.
type SkillSignatureEnvelope struct {
	Version        string `json:"version"`
	Algorithm      string `json:"algorithm"`
	KeyID          string `json:"key_id"`
	SignerName     string `json:"signer_name,omitempty"`
	WASMSha256     string `json:"wasm_sha256"`
	ManifestSha256 string `json:"manifest_sha256"`
	Signature      string `json:"signature"` // base64-encoded 64-byte Ed25519 signature
}

// TrustedSigner represents an authorized public key entry in <AppDir>/trusted_signers/.
type TrustedSigner struct {
	KeyID     string          `json:"key_id"`
	Name      string          `json:"name"`
	Algorithm string          `json:"algorithm"`
	PublicKey string          `json:"public_key"` // base64-encoded 32-byte Ed25519 public key
	TrustTier types.SkillTier `json:"trust_tier"` // Tier1Official (1) or Tier2CommunityVerified (2)
	AddedAt   string          `json:"added_at,omitempty"`
}

// ComputeEd25519KeyID returns the first 16 lowercase hex characters of SHA-256(pub).
func ComputeEd25519KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])[:16]
}

// GenerateEd25519KeyPair generates a fresh Ed25519 keypair with its derived KeyID.
func GenerateEd25519KeyPair(signerName string) (*Ed25519KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate Ed25519 keypair: %w", err)
	}
	return &Ed25519KeyPair{
		KeyID:      ComputeEd25519KeyID(pub),
		SignerName: strings.TrimSpace(signerName),
		PublicKey:  pub,
		PrivateKey: priv,
	}, nil
}

// WriteEd25519KeyPairFiles atomically persists the private key (0600) and public key (0644)
// as PKCS#8 / PKIX PEM files in outDir.
func WriteEd25519KeyPairFiles(outDir, signerName string, kp *Ed25519KeyPair) (privPath, pubPath string, err error) {
	if kp == nil || len(kp.PrivateKey) != ed25519.PrivateKeySize || len(kp.PublicKey) != ed25519.PublicKeySize {
		return "", "", fmt.Errorf("invalid Ed25519 keypair")
	}
	if err := os.MkdirAll(outDir, 0700); err != nil {
		return "", "", fmt.Errorf("failed to create key output directory %s: %w", outDir, err)
	}

	base := sanitizeSignerSlug(signerName)
	if base == "" {
		base = "vraxter_ed25519_" + kp.KeyID
	}

	privDER, err := x509.MarshalPKCS8PrivateKey(kp.PrivateKey)
	if err != nil {
		return "", "", fmt.Errorf("failed marshaling Ed25519 private key: %w", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type: "PRIVATE KEY",
		Headers: map[string]string{
			"Key-ID":      kp.KeyID,
			"Signer-Name": kp.SignerName,
		},
		Bytes: privDER,
	})

	pubDER, err := x509.MarshalPKIXPublicKey(kp.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("failed marshaling Ed25519 public key: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type: "PUBLIC KEY",
		Headers: map[string]string{
			"Key-ID":      kp.KeyID,
			"Signer-Name": kp.SignerName,
		},
		Bytes: pubDER,
	})

	privPath = filepath.Join(outDir, base+".key")
	pubPath = filepath.Join(outDir, base+".pub")

	if err := utils.AtomicWriteFile(privPath, privPEM, 0600); err != nil {
		return "", "", fmt.Errorf("failed writing private key %s: %w", privPath, err)
	}
	if err := utils.AtomicWriteFile(pubPath, pubPEM, 0644); err != nil {
		return "", "", fmt.Errorf("failed writing public key %s: %w", pubPath, err)
	}
	return privPath, pubPath, nil
}

// LoadEd25519PrivateKey reads an Ed25519 private key from a PEM file (or raw base64/hex seed)
// along with any embedded Signer-Name header.
func LoadEd25519PrivateKey(path string) (ed25519.PrivateKey, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read private key %s: %w", path, err)
	}

	if block, _ := pem.Decode(data); block != nil {
		signerName := strings.TrimSpace(block.Headers["Signer-Name"])
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err == nil {
			if edPriv, ok := parsed.(ed25519.PrivateKey); ok {
				return edPriv, signerName, nil
			}
		}
		if len(block.Bytes) == ed25519.PrivateKeySize {
			return ed25519.PrivateKey(block.Bytes), signerName, nil
		}
		if len(block.Bytes) == ed25519.SeedSize {
			return ed25519.NewKeyFromSeed(block.Bytes), signerName, nil
		}
		return nil, "", fmt.Errorf("PEM file %s does not contain a valid Ed25519 private key", path)
	}

	trimmed := strings.TrimSpace(string(data))
	if raw, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		if len(raw) == ed25519.PrivateKeySize {
			return ed25519.PrivateKey(raw), "", nil
		}
		if len(raw) == ed25519.SeedSize {
			return ed25519.NewKeyFromSeed(raw), "", nil
		}
	}
	if raw, err := hex.DecodeString(trimmed); err == nil {
		if len(raw) == ed25519.PrivateKeySize {
			return ed25519.PrivateKey(raw), "", nil
		}
		if len(raw) == ed25519.SeedSize {
			return ed25519.NewKeyFromSeed(raw), "", nil
		}
	}

	return nil, "", fmt.Errorf("unrecognized Ed25519 private key format in %s", path)
}

// LoadEd25519PublicKey reads an Ed25519 public key from a PEM file, TrustedSigner JSON file,
// or raw base64/hex string.
func LoadEd25519PublicKey(path string) (ed25519.PublicKey, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read public key %s: %w", path, err)
	}
	return ParseEd25519PublicKeyBytes(data)
}

// ParseEd25519PublicKeyBytes parses an Ed25519 public key from PEM, JSON, base64, or hex bytes.
func ParseEd25519PublicKeyBytes(data []byte) (ed25519.PublicKey, string, error) {
	if block, _ := pem.Decode(data); block != nil {
		signerName := strings.TrimSpace(block.Headers["Signer-Name"])
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err == nil {
			if edPub, ok := parsed.(ed25519.PublicKey); ok {
				return edPub, signerName, nil
			}
		}
		if len(block.Bytes) == ed25519.PublicKeySize {
			return ed25519.PublicKey(block.Bytes), signerName, nil
		}
		return nil, "", fmt.Errorf("PEM block does not contain a valid Ed25519 public key")
	}

	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		var ts TrustedSigner
		if err := json.Unmarshal(data, &ts); err == nil && ts.PublicKey != "" {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ts.PublicKey))
			if err == nil && len(raw) == ed25519.PublicKeySize {
				return ed25519.PublicKey(raw), ts.Name, nil
			}
		}
	}

	if raw, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(raw) == ed25519.PublicKeySize {
		return ed25519.PublicKey(raw), "", nil
	}
	if raw, err := hex.DecodeString(trimmed); err == nil && len(raw) == ed25519.PublicKeySize {
		return ed25519.PublicKey(raw), "", nil
	}

	return nil, "", fmt.Errorf("invalid Ed25519 public key format")
}

// BuildSkillSigningPayload constructs the domain-separated message signed by Ed25519:
// "VRAXTER-SKILL-V1\n" + sha256_hex(wasmBytes) + "\n" + sha256_hex(canonicalManifestBytes)
func BuildSkillSigningPayload(wasmSHA256Hex, manifestSHA256Hex string) []byte {
	w := strings.ToLower(strings.TrimSpace(wasmSHA256Hex))
	m := strings.ToLower(strings.TrimSpace(manifestSHA256Hex))
	return []byte(fmt.Sprintf("%s\n%s\n%s", SkillSignatureDomain, w, m))
}

// CanonicalizeManifestJSON produces a byte-for-byte deterministic JSON representation
// of a skill manifest (either skill-manifest/1 or legacy manifest JSON):
//   - Excludes any embedded signature fields ("signature", "sig", "$schema")
//   - Excludes module.sha256 and volatile file directory prefixes (since wasm_sha256 is
//     already bound on line 2 of the signing payload and InstallSkill updates module.file/sha256)
//   - Normalizes and sorts permission/capability arrays and object keys recursively.
func CanonicalizeManifestJSON(rawManifest []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(rawManifest)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("cannot canonicalize empty manifest")
	}

	var probe struct {
		ManifestVersion string `json:"manifest_version"`
	}
	_ = json.Unmarshal(trimmed, &probe)

	if probe.ManifestVersion == types.ManifestVersionV1 {
		var v1 types.SkillManifestV1
		if err := json.Unmarshal(trimmed, &v1); err != nil {
			return nil, fmt.Errorf("invalid skill-manifest/1 JSON: %w", err)
		}
		return CanonicalizeSkillManifestV1(&v1)
	}

	// Legacy manifest JSON: parse into generic map, strip volatile/signature fields, and canonicalize.
	var generic map[string]interface{}
	if err := json.Unmarshal(trimmed, &generic); err != nil {
		return nil, fmt.Errorf("invalid manifest JSON: %w", err)
	}
	delete(generic, "signature")
	delete(generic, "sig")
	delete(generic, "checksum")
	delete(generic, "command")
	return marshalCanonicalValue(generic)
}

// CanonicalizeSkillManifestV1 produces a deterministic canonical JSON byte slice from a SkillManifestV1.
func CanonicalizeSkillManifestV1(v1 *types.SkillManifestV1) ([]byte, error) {
	if v1 == nil {
		return nil, fmt.Errorf("cannot canonicalize nil SkillManifestV1")
	}

	// Deep-copy via JSON to avoid mutating caller state
	raw, err := json.Marshal(v1)
	if err != nil {
		return nil, err
	}
	var copyV1 types.SkillManifestV1
	if err := json.Unmarshal(raw, &copyV1); err != nil {
		return nil, err
	}

	// Normalize fields that InstallSkill canonicalizes upon ingestion
	copyV1.Schema = ""
	copyV1.ManifestVersion = types.ManifestVersionV1
	copyV1.ID = strings.TrimSpace(copyV1.ID)
	copyV1.Name = strings.TrimSpace(copyV1.Name)
	if copyV1.Name == "" {
		copyV1.Name = copyV1.ID
	}
	copyV1.Version = strings.TrimSpace(copyV1.Version)
	if copyV1.Version == "" {
		copyV1.Version = "1.0.0"
	}
	copyV1.Description = strings.TrimSpace(copyV1.Description)
	// wasm_sha256 is bound directly in the domain-separated payload; clear module.sha256 & module.file
	// so pre-install and post-install manifest representations hash identically.
	copyV1.Module.SHA256 = ""
	copyV1.Module.File = ""
	if strings.TrimSpace(copyV1.Module.ABI) == "" {
		copyV1.Module.ABI = "wasip1"
	}

	if copyV1.Routing != nil {
		sort.Strings(copyV1.Routing.Keywords)
		sort.Strings(copyV1.Routing.Tags)
		sort.Strings(copyV1.Routing.Examples)
	}
	if copyV1.Capabilities != nil {
		if copyV1.Capabilities.FS != nil && len(copyV1.Capabilities.FS.Mounts) > 0 {
			for i := range copyV1.Capabilities.FS.Mounts {
				sort.Strings(copyV1.Capabilities.FS.Mounts[i].AllowExtensions)
			}
			sort.Slice(copyV1.Capabilities.FS.Mounts, func(i, j int) bool {
				if copyV1.Capabilities.FS.Mounts[i].Guest != copyV1.Capabilities.FS.Mounts[j].Guest {
					return copyV1.Capabilities.FS.Mounts[i].Guest < copyV1.Capabilities.FS.Mounts[j].Guest
				}
				return copyV1.Capabilities.FS.Mounts[i].Source < copyV1.Capabilities.FS.Mounts[j].Source
			})
		}
		if copyV1.Capabilities.Network != nil {
			sort.Strings(copyV1.Capabilities.Network.AllowedHosts)
			sort.Strings(copyV1.Capabilities.Network.AllowedSchemes)
			sort.Ints(copyV1.Capabilities.Network.AllowedPorts)
		}
	}

	normalizedRaw, err := json.Marshal(copyV1)
	if err != nil {
		return nil, err
	}
	var generic interface{}
	if err := json.Unmarshal(normalizedRaw, &generic); err != nil {
		return nil, err
	}
	return marshalCanonicalValue(generic)
}

func marshalCanonicalValue(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonicalJSON(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonicalJSON(buf *bytes.Buffer, v interface{}) error {
	switch val := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			if k == "signature" || k == "sig" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeCanonicalJSON(buf, val[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil

	case []interface{}:
		buf.WriteByte('[')
		for i, elem := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonicalJSON(buf, elem); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil

	default:
		var raw bytes.Buffer
		enc := json.NewEncoder(&raw)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(val); err != nil {
			return err
		}
		buf.Write(bytes.TrimRight(raw.Bytes(), "\n"))
		return nil
	}
}

// HashFileSHA256 computes the lowercase hex SHA-256 of path bounded by maxSignableWASMBytes.
func HashFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if fi.Size() > maxSignableWASMBytes {
		return "", fmt.Errorf("file %s exceeds maximum allowed size (%d > %d bytes)", path, fi.Size(), maxSignableWASMBytes)
	}

	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxSignableWASMBytes+1))
	if err != nil {
		return "", err
	}
	if n > maxSignableWASMBytes {
		return "", fmt.Errorf("file %s exceeds maximum allowed size (%d bytes)", path, maxSignableWASMBytes)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SignSkillArtifacts signs a .wasm binary and manifest bytes with an Ed25519 private key.
func SignSkillArtifacts(wasmPath string, manifestData []byte, privKey ed25519.PrivateKey, signerName string) (*SkillSignatureEnvelope, error) {
	if len(privKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid Ed25519 private key length (%d)", len(privKey))
	}

	wasmHash, err := HashFileSHA256(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("failed hashing WASM binary: %w", err)
	}

	canonicalManifest, err := CanonicalizeManifestJSON(manifestData)
	if err != nil {
		return nil, fmt.Errorf("failed canonicalizing manifest: %w", err)
	}
	mSum := sha256.Sum256(canonicalManifest)
	manifestHash := hex.EncodeToString(mSum[:])

	pubKey := privKey.Public().(ed25519.PublicKey)
	keyID := ComputeEd25519KeyID(pubKey)

	payload := BuildSkillSigningPayload(wasmHash, manifestHash)
	sig := ed25519.Sign(privKey, payload)

	return &SkillSignatureEnvelope{
		Version:        SkillSignatureVersion,
		Algorithm:      SkillSignatureAlgorithm,
		KeyID:          keyID,
		SignerName:     strings.TrimSpace(signerName),
		WASMSha256:     wasmHash,
		ManifestSha256: manifestHash,
		Signature:      base64.StdEncoding.EncodeToString(sig),
	}, nil
}

// SignSkillFiles reads wasmPath and manifestPath, signs them with the Ed25519 private key
// at privKeyPath, and writes the signature envelope atomically to outSigPath (0600).
func SignSkillFiles(wasmPath, manifestPath, privKeyPath, outSigPath, signerNameOverride string) (*SkillSignatureEnvelope, error) {
	privKey, headerSigner, err := LoadEd25519PrivateKey(privKeyPath)
	if err != nil {
		return nil, err
	}
	signerName := strings.TrimSpace(signerNameOverride)
	if signerName == "" {
		signerName = headerSigner
	}

	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed reading manifest file %s: %w", manifestPath, err)
	}

	env, err := SignSkillArtifacts(wasmPath, manifestBytes, privKey, signerName)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(outSigPath) != "" {
		raw, err := json.MarshalIndent(env, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := utils.AtomicWriteFile(outSigPath, raw, 0600); err != nil {
			return nil, fmt.Errorf("failed writing signature file %s: %w", outSigPath, err)
		}
	}
	return env, nil
}

// LoadSignatureEnvelope reads and validates the structure of a <skill-id>.sig file.
func LoadSignatureEnvelope(sigPath string) (*SkillSignatureEnvelope, error) {
	raw, err := os.ReadFile(sigPath)
	if err != nil {
		return nil, err
	}
	var env SkillSignatureEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("corrupt signature envelope %s: %w", sigPath, err)
	}
	if strings.ToLower(strings.TrimSpace(env.Algorithm)) != SkillSignatureAlgorithm {
		return nil, fmt.Errorf("unsupported signature algorithm %q in %s (expected ed25519)", env.Algorithm, sigPath)
	}
	if strings.TrimSpace(env.KeyID) == "" || strings.TrimSpace(env.WASMSha256) == "" || strings.TrimSpace(env.ManifestSha256) == "" || strings.TrimSpace(env.Signature) == "" {
		return nil, fmt.Errorf("incomplete signature envelope in %s", sigPath)
	}
	return &env, nil
}

// VerifyEnvelopeWithPublicKey verifies an envelope against a specific Ed25519 public key,
// expected WASM SHA-256 hex, and canonical manifest bytes.
func VerifyEnvelopeWithPublicKey(env *SkillSignatureEnvelope, pub ed25519.PublicKey, actualWASMHash string, canonicalManifest []byte) error {
	if env == nil {
		return fmt.Errorf("nil signature envelope")
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid Ed25519 public key size (%d)", len(pub))
	}
	if strings.ToLower(strings.TrimSpace(env.Algorithm)) != SkillSignatureAlgorithm {
		return fmt.Errorf("unsupported signature algorithm %q", env.Algorithm)
	}

	derivedKeyID := ComputeEd25519KeyID(pub)
	if !strings.EqualFold(strings.TrimSpace(env.KeyID), derivedKeyID) {
		return fmt.Errorf("signature key_id mismatch: envelope has %s, public key is %s", env.KeyID, derivedKeyID)
	}

	if !strings.EqualFold(strings.TrimSpace(env.WASMSha256), strings.TrimSpace(actualWASMHash)) {
		return fmt.Errorf("WASM binary SHA-256 mismatch against signature (signed=%s, actual=%s)", env.WASMSha256, actualWASMHash)
	}

	mSum := sha256.Sum256(canonicalManifest)
	actualManifestHash := hex.EncodeToString(mSum[:])
	if !strings.EqualFold(strings.TrimSpace(env.ManifestSha256), actualManifestHash) {
		return fmt.Errorf("manifest SHA-256 mismatch against signature (signed=%s, actual=%s)", env.ManifestSha256, actualManifestHash)
	}

	sigBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(env.Signature))
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return fmt.Errorf("invalid base64 Ed25519 signature in envelope")
	}

	payload := BuildSkillSigningPayload(actualWASMHash, actualManifestHash)
	if !ed25519.Verify(pub, payload, sigBytes) {
		return fmt.Errorf("cryptographic Ed25519 signature verification failed")
	}
	return nil
}

// TrustedSignerStore manages the <AppDir>/trusted_signers/ directory of trusted Ed25519 public keys.
type TrustedSignerStore struct {
	dir string
}

// NewTrustedSignerStore creates a TrustedSignerStore rooted at dir.
func NewTrustedSignerStore(dir string) *TrustedSignerStore {
	return &TrustedSignerStore{dir: dir}
}

// Dir returns the filesystem path of the trusted_signers directory.
func (s *TrustedSignerStore) Dir() string {
	return s.dir
}

// AddSigner registers a trusted Ed25519 public key with its associated TrustTier (Tier1Official or Tier2CommunityVerified).
func (s *TrustedSignerStore) AddSigner(pub ed25519.PublicKey, name string, tier types.SkillTier) (TrustedSigner, string, error) {
	if len(pub) != ed25519.PublicKeySize {
		return TrustedSigner{}, "", fmt.Errorf("invalid Ed25519 public key length (%d)", len(pub))
	}
	if tier != types.Tier1Official && tier != types.Tier2CommunityVerified {
		return TrustedSigner{}, "", fmt.Errorf("invalid signer trust tier %d (must be 1 for Official or 2 for Verified)", tier)
	}
	if strings.TrimSpace(s.dir) == "" {
		return TrustedSigner{}, "", fmt.Errorf("trusted_signers directory is not configured")
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return TrustedSigner{}, "", fmt.Errorf("failed creating trusted_signers directory: %w", err)
	}

	keyID := ComputeEd25519KeyID(pub)
	signerName := strings.TrimSpace(name)
	if signerName == "" {
		signerName = "signer-" + keyID
	}

	entry := TrustedSigner{
		KeyID:     keyID,
		Name:      signerName,
		Algorithm: SkillSignatureAlgorithm,
		PublicKey: base64.StdEncoding.EncodeToString(pub),
		TrustTier: tier,
		AddedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	raw, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return TrustedSigner{}, "", err
	}

	destPath := filepath.Join(s.dir, keyID+".json")
	if err := utils.AtomicWriteFile(destPath, raw, 0600); err != nil {
		return TrustedSigner{}, "", fmt.Errorf("failed persisting trusted signer: %w", err)
	}
	return entry, destPath, nil
}

// ListSigners returns all valid trusted signers stored in trusted_signers/.
func (s *TrustedSignerStore) ListSigners() ([]TrustedSigner, error) {
	if strings.TrimSpace(s.dir) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []TrustedSigner
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".json" && ext != ".pub" {
			continue
		}
		fullPath := filepath.Join(s.dir, e.Name())
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		if ext == ".json" {
			var ts TrustedSigner
			if err := json.Unmarshal(data, &ts); err == nil && ts.PublicKey != "" {
				pub, _, parseErr := ParseEd25519PublicKeyBytes(data)
				if parseErr == nil {
					ts.KeyID = ComputeEd25519KeyID(pub)
					if ts.TrustTier != types.Tier1Official && ts.TrustTier != types.Tier2CommunityVerified {
						ts.TrustTier = types.Tier2CommunityVerified
					}
					out = append(out, ts)
				}
			}
			continue
		}

		// Standalone .pub file support (defaults to Tier2CommunityVerified)
		pub, headerName, parseErr := ParseEd25519PublicKeyBytes(data)
		if parseErr == nil {
			keyID := ComputeEd25519KeyID(pub)
			if headerName == "" {
				headerName = strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			}
			out = append(out, TrustedSigner{
				KeyID:     keyID,
				Name:      headerName,
				Algorithm: SkillSignatureAlgorithm,
				PublicKey: base64.StdEncoding.EncodeToString(pub),
				TrustTier: types.Tier2CommunityVerified,
			})
		}
	}
	return out, nil
}

// FindSignerByKeyID looks up a trusted signer by its 16-char hex KeyID.
func (s *TrustedSignerStore) FindSignerByKeyID(keyID string) (*TrustedSigner, ed25519.PublicKey, error) {
	target := strings.ToLower(strings.TrimSpace(keyID))
	if target == "" {
		return nil, nil, fmt.Errorf("empty key_id")
	}
	signers, err := s.ListSigners()
	if err != nil {
		return nil, nil, err
	}
	for i := range signers {
		if strings.EqualFold(signers[i].KeyID, target) {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signers[i].PublicKey))
			if err != nil || len(raw) != ed25519.PublicKeySize {
				return nil, nil, fmt.Errorf("corrupt public key for trusted signer %s", target)
			}
			return &signers[i], ed25519.PublicKey(raw), nil
		}
	}
	return nil, nil, fmt.Errorf("signer key_id %q is not registered in trusted_signers", keyID)
}

// VerifySkillAgainstStore verifies a skill's WASM binary, canonical manifest bytes, and signature
// envelope against the TrustedSignerStore and returns the authenticated TrustedSigner.
func (s *TrustedSignerStore) VerifySkillAgainstStore(wasmPath string, canonicalManifest []byte, sigPath string) (*TrustedSigner, *SkillSignatureEnvelope, error) {
	env, err := LoadSignatureEnvelope(sigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("VRAXTER SHIELD: invalid skill signature: %w", err)
	}

	signer, pubKey, err := s.FindSignerByKeyID(env.KeyID)
	if err != nil {
		return nil, nil, fmt.Errorf("VRAXTER SHIELD: untrusted skill signer (key_id=%s): %w", env.KeyID, err)
	}

	wasmHash, err := HashFileSHA256(wasmPath)
	if err != nil {
		return nil, nil, fmt.Errorf("VRAXTER SHIELD: failed hashing WASM binary: %w", err)
	}

	if err := VerifyEnvelopeWithPublicKey(env, pubKey, wasmHash, canonicalManifest); err != nil {
		return nil, nil, fmt.Errorf("VRAXTER SHIELD: skill signature verification failed: %w", err)
	}
	return signer, env, nil
}

// DiscoverSkillSignaturePath checks standard adjacent locations for a skill's .sig file.
func DiscoverSkillSignaturePath(wasmPath, manifestPath, skillID string) string {
	var candidates []string
	if strings.TrimSpace(wasmPath) != "" {
		dir := filepath.Dir(wasmPath)
		baseNoExt := strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath))
		candidates = append(candidates,
			filepath.Join(dir, baseNoExt+".sig"),
			wasmPath+".sig",
		)
		if strings.TrimSpace(skillID) != "" {
			candidates = append(candidates, filepath.Join(dir, skillID+".sig"))
		}
	}
	if strings.TrimSpace(manifestPath) != "" {
		dir := filepath.Dir(manifestPath)
		if strings.TrimSpace(skillID) != "" {
			candidates = append(candidates, filepath.Join(dir, skillID+".sig"))
		}
		baseNoExt := strings.TrimSuffix(filepath.Base(manifestPath), filepath.Ext(manifestPath))
		baseNoExt = strings.TrimSuffix(baseNoExt, ".manifest")
		candidates = append(candidates, filepath.Join(dir, baseNoExt+".sig"))
	}

	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

func sanitizeSignerSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if s == "" {
		return ""
	}
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			out = append(out, c)
		} else if c == '-' || c == '_' || c == ' ' || c == '.' {
			out = append(out, '_')
		}
	}
	return strings.Trim(string(out), "_")
}
