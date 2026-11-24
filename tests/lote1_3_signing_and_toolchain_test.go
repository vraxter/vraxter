// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package tests

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/pkg/types"
)

// minimalValidWASIModule returns a valid WASM binary exporting memory and _start() -> ()
// that writes "OK" to stdout via wasi_snapshot_preview1.fd_write.
func minimalValidWASIModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic + version
		// Type section: type 0 = (i32,i32,i32,i32)->i32, type 1 = ()->()
		0x01, 0x0c, 0x02,
		0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f,
		0x60, 0x00, 0x00,
		// Import section: wasi_snapshot_preview1.fd_write (func 0, type 0)
		0x02, 0x23, 0x01,
		0x16, 'w', 'a', 's', 'i', '_', 's', 'n', 'a', 'p', 's', 'h', 'o', 't', '_', 'p', 'r', 'e', 'v', 'i', 'e', 'w', '1',
		0x08, 'f', 'd', '_', 'w', 'r', 'i', 't', 'e',
		0x00, 0x00,
		// Function section: func 1 uses type 1
		0x03, 0x02, 0x01, 0x01,
		// Memory section: min 1 page
		0x05, 0x03, 0x01, 0x00, 0x01,
		// Export section: memory (mem 0), _start (func 1)
		0x07, 0x13, 0x02,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x01,
		// Code section: _start calls fd_write(1, 0, 1, 16)
		0x0a, 0x0f, 0x01,
		0x0d, 0x00,
		0x41, 0x01, // fd = 1 (stdout)
		0x41, 0x00, // iovs_ptr = 0
		0x41, 0x01, // iovs_len = 1
		0x41, 0x10, // nwritten_ptr = 16
		0x10, 0x00, // call fd_write
		0x1a,       // drop
		0x0b,       // end
		// Data section: iov={ptr:8, len:2} at 0, "OK" at 8
		0x0b, 0x10, 0x01,
		0x00, 0x41, 0x00, 0x0b,
		0x0a,
		0x08, 0x00, 0x00, 0x00,
		0x02, 0x00, 0x00, 0x00,
		'O', 'K',
	}
}

func buildPrivilegedManifestV1(id, name string) types.SkillManifestV1 {
	return types.SkillManifestV1{
		ManifestVersion: types.ManifestVersionV1,
		ID:              id,
		Name:            name,
		Version:         "1.0.0",
		Description:     "Signed privileged test skill",
		Module: types.ModuleSpec{
			File: id + ".wasm",
			ABI:  "wasip1",
		},
		IO: &types.IOSpec{
			Output: types.OutputSpec{
				Via:    "stdout",
				Format: "text",
			},
		},
		Capabilities: &types.CapabilitiesSpec{
			FS: &types.FSSpec{
				Mounts: []types.MountSpec{
					{
						Guest:   "/workspace",
						Source:  "workspace",
						Subpath: ".",
						Mode:    "rw",
					},
				},
			},
		},
	}
}

func buildZeroPrivilegeManifestV1(id, name string) types.SkillManifestV1 {
	return types.SkillManifestV1{
		ManifestVersion: types.ManifestVersionV1,
		ID:              id,
		Name:            name,
		Version:         "1.0.0",
		Description:     "Unsigned zero-privilege test skill",
		Module: types.ModuleSpec{
			File: id + ".wasm",
			ABI:  "wasip1",
		},
		IO: &types.IOSpec{
			Output: types.OutputSpec{
				Via:    "stdout",
				Format: "text",
			},
		},
	}
}

func TestLote1_3_Ed25519Lifecycle_KeygenSignTrustVerifyInstallExecute(t *testing.T) {
	tmpDir := t.TempDir()
	keysDir := filepath.Join(tmpDir, "keys")
	trustedDir := filepath.Join(tmpDir, "trusted_signers")
	stagingDir := filepath.Join(tmpDir, "staging")
	skillsDir := filepath.Join(tmpDir, "skills")
	workspaceDir := filepath.Join(tmpDir, "workspace")

	for _, d := range []string{keysDir, trustedDir, stagingDir, skillsDir, workspaceDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatalf("MkdirAll(%s) failed: %v", d, err)
		}
	}

	// 1. Keygen (Tier1Official signer)
	kp1, err := security.GenerateEd25519KeyPair("Vraxter Official Release Key")
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPair failed: %v", err)
	}
	if len(kp1.KeyID) != 16 {
		t.Fatalf("expected 16-char hex key_id, got %q", kp1.KeyID)
	}
	priv1Path, pub1Path, err := security.WriteEd25519KeyPairFiles(keysDir, "official", kp1)
	if err != nil {
		t.Fatalf("WriteEd25519KeyPairFiles failed: %v", err)
	}

	if runtime.GOOS != "windows" {
		fiPriv, err := os.Stat(priv1Path)
		if err != nil {
			t.Fatalf("stat private key: %v", err)
		}
		if fiPriv.Mode().Perm() != 0600 {
			t.Fatalf("expected private key mode 0600, got %o", fiPriv.Mode().Perm())
		}
		fiPub, err := os.Stat(pub1Path)
		if err != nil {
			t.Fatalf("stat public key: %v", err)
		}
		if fiPub.Mode().Perm() != 0644 {
			t.Fatalf("expected public key mode 0644, got %o", fiPub.Mode().Perm())
		}
	}

	// 2. Trust Add into trusted_signers/
	store := security.NewTrustedSignerStore(trustedDir)
	loadedPub1, _, err := security.LoadEd25519PublicKey(pub1Path)
	if err != nil {
		t.Fatalf("LoadEd25519PublicKey failed: %v", err)
	}
	entry1, _, err := store.AddSigner(loadedPub1, "Vraxter Official Release Key", types.Tier1Official)
	if err != nil {
		t.Fatalf("AddSigner Tier1Official failed: %v", err)
	}
	if entry1.KeyID != kp1.KeyID {
		t.Fatalf("expected signer KeyID %s, got %s", kp1.KeyID, entry1.KeyID)
	}

	// 3. Prepare WASM + manifest.json and Sign
	skillID := "signed-official-skill"
	wasmPath := filepath.Join(stagingDir, skillID+".wasm")
	manifestPath := filepath.Join(stagingDir, "manifest.json")
	sigPath := filepath.Join(stagingDir, skillID+".sig")

	if err := os.WriteFile(wasmPath, minimalValidWASIModule(), 0600); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	v1 := buildPrivilegedManifestV1(skillID, "Signed Official Skill")
	manifestBytes, err := json.MarshalIndent(v1, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	env, err := security.SignSkillFiles(wasmPath, manifestPath, priv1Path, sigPath, "Vraxter Official Release Key")
	if err != nil {
		t.Fatalf("SignSkillFiles failed: %v", err)
	}
	if env.Algorithm != security.SkillSignatureAlgorithm {
		t.Fatalf("unexpected algorithm: %s", env.Algorithm)
	}
	if env.KeyID != kp1.KeyID {
		t.Fatalf("unexpected key_id in signature envelope: %s", env.KeyID)
	}

	// 4. Verify against trusted_signers/
	canonicalManifest, err := security.CanonicalizeManifestJSON(manifestBytes)
	if err != nil {
		t.Fatalf("CanonicalizeManifestJSON failed: %v", err)
	}
	verifiedSigner, verifiedEnv, err := store.VerifySkillAgainstStore(wasmPath, canonicalManifest, sigPath)
	if err != nil {
		t.Fatalf("VerifySkillAgainstStore failed: %v", err)
	}
	if verifiedSigner.TrustTier != types.Tier1Official || verifiedEnv.KeyID != kp1.KeyID {
		t.Fatalf("unexpected verified signer result: %+v", verifiedSigner)
	}

	// 5. RegisterFromWASM should automatically verify <skill>.sig and assign Tier1Official + IsOfficial=true
	dbStore, err := db.NewStore(filepath.Join(tmpDir, "vraxter.db"))
	if err != nil {
		t.Fatalf("db.NewStore failed: %v", err)
	}
	defer dbStore.Close()

	skillRepo := db.NewSkillRepository(dbStore)
	skillSvc := services.NewSkillService(skillRepo, skillsDir)
	skillSvc.SetTrustedSignersDir(trustedDir)
	skillSvc.SetRequireSignedSkills(true)

	installed, err := skillSvc.RegisterFromWASM(wasmPath, "")
	if err != nil {
		t.Fatalf("RegisterFromWASM failed for signed official skill: %v", err)
	}
	if installed.Tier != types.Tier1Official {
		t.Fatalf("expected installed skill Tier1Official, got %d", installed.Tier)
	}
	if !installed.IsOfficial {
		t.Fatalf("expected installed skill IsOfficial=true for Tier1Official signer")
	}
	if _, err := os.Stat(filepath.Join(skillsDir, skillID+".sig")); err != nil {
		t.Fatalf("expected signature file copied into skillsDir: %v", err)
	}

	// 6. Execute via Runner with RequireSignedSkills=true
	runner := skills.NewRunner()
	runner.SetTrustedSignersDir(trustedDir)
	runner.SetRequireSignedSkills(true)

	res, err := runner.Execute(context.Background(), installed, map[string]interface{}{"input": "hello"})
	if err != nil {
		t.Fatalf("Runner.Execute failed for valid signed official skill: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil execution result")
	}

	// 7. Test Tier2CommunityVerified automatic assignment as well
	kp2, err := security.GenerateEd25519KeyPair("Trusted Enterprise Partner")
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPair partner: %v", err)
	}
	priv2Path, pub2Path, err := security.WriteEd25519KeyPairFiles(keysDir, "partner", kp2)
	if err != nil {
		t.Fatalf("WriteEd25519KeyPairFiles partner: %v", err)
	}
	loadedPub2, _, err := security.LoadEd25519PublicKey(pub2Path)
	if err != nil {
		t.Fatalf("LoadEd25519PublicKey partner: %v", err)
	}
	if _, _, err := store.AddSigner(loadedPub2, "Trusted Enterprise Partner", types.Tier2CommunityVerified); err != nil {
		t.Fatalf("AddSigner Tier2CommunityVerified: %v", err)
	}

	staging2 := filepath.Join(tmpDir, "staging2")
	_ = os.MkdirAll(staging2, 0700)
	skill2ID := "signed-partner-skill"
	wasm2Path := filepath.Join(staging2, skill2ID+".wasm")
	manifest2Path := filepath.Join(staging2, "manifest.json")
	sig2Path := filepath.Join(staging2, skill2ID+".sig")

	_ = os.WriteFile(wasm2Path, minimalValidWASIModule(), 0600)
	v1Partner := buildPrivilegedManifestV1(skill2ID, "Signed Partner Skill")
	mb2, _ := json.MarshalIndent(v1Partner, "", "  ")
	_ = os.WriteFile(manifest2Path, mb2, 0600)

	if _, err := security.SignSkillFiles(wasm2Path, manifest2Path, priv2Path, sig2Path, "Trusted Enterprise Partner"); err != nil {
		t.Fatalf("SignSkillFiles partner: %v", err)
	}

	installed2, err := skillSvc.RegisterFromWASM(wasm2Path, "")
	if err != nil {
		t.Fatalf("RegisterFromWASM partner failed: %v", err)
	}
	if installed2.Tier != types.Tier2CommunityVerified {
		t.Fatalf("expected Tier2CommunityVerified, got %d", installed2.Tier)
	}
	if installed2.IsOfficial {
		t.Fatalf("expected IsOfficial=false for Tier2CommunityVerified signer")
	}
	if _, err := runner.Execute(context.Background(), installed2, nil); err != nil {
		t.Fatalf("Runner.Execute partner skill failed: %v", err)
	}
}

func TestLote1_3_FailClosedOnTamperedWASMOrManifestAndRequireSignedSkills(t *testing.T) {
	tmpDir := t.TempDir()
	keysDir := filepath.Join(tmpDir, "keys")
	trustedDir := filepath.Join(tmpDir, "trusted_signers")
	skillsDir := filepath.Join(tmpDir, "skills")
	workspaceDir := filepath.Join(tmpDir, "workspace")

	for _, d := range []string{keysDir, trustedDir, skillsDir, workspaceDir} {
		_ = os.MkdirAll(d, 0700)
	}

	kp, err := security.GenerateEd25519KeyPair("Official Signer")
	if err != nil {
		t.Fatalf("keygen failed: %v", err)
	}
	privPath, pubPath, err := security.WriteEd25519KeyPairFiles(keysDir, "signer", kp)
	if err != nil {
		t.Fatalf("WriteEd25519KeyPairFiles failed: %v", err)
	}
	loadedPub, _, err := security.LoadEd25519PublicKey(pubPath)
	if err != nil {
		t.Fatalf("LoadEd25519PublicKey failed: %v", err)
	}
	store := security.NewTrustedSignerStore(trustedDir)
	if _, _, err := store.AddSigner(loadedPub, "Official Signer", types.Tier1Official); err != nil {
		t.Fatalf("AddSigner failed: %v", err)
	}

	dbStore, err := db.NewStore(filepath.Join(tmpDir, "vraxter.db"))
	if err != nil {
		t.Fatalf("db.NewStore failed: %v", err)
	}
	defer dbStore.Close()
	skillRepo := db.NewSkillRepository(dbStore)
	skillSvc := services.NewSkillService(skillRepo, skillsDir)
	skillSvc.SetTrustedSignersDir(trustedDir)

	runner := skills.NewRunner()
	runner.SetTrustedSignersDir(trustedDir)

	// Case A: 1-byte tampered WASM after signing is rejected at install time
	t.Run("TamperedWASMAtInstall", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "case-wasm-tamper")
		_ = os.MkdirAll(dir, 0700)
		wasmPath := filepath.Join(dir, "tampered-wasm.wasm")
		manifestPath := filepath.Join(dir, "manifest.json")
		sigPath := filepath.Join(dir, "tampered-wasm.sig")

		wasmBytes := minimalValidWASIModule()
		_ = os.WriteFile(wasmPath, wasmBytes, 0600)
		v1 := buildPrivilegedManifestV1("tampered-wasm", "Tampered WASM")
		mb, _ := json.MarshalIndent(v1, "", "  ")
		_ = os.WriteFile(manifestPath, mb, 0600)

		if _, err := security.SignSkillFiles(wasmPath, manifestPath, privPath, sigPath, "Official Signer"); err != nil {
			t.Fatalf("SignSkillFiles: %v", err)
		}

		// Append a valid WASM custom section (id=0, len=2, namelen=1, 'x') so the binary remains valid WASM
		// while changing the binary content by bytes after signing.
		tampered := append([]byte{}, wasmBytes...)
		tampered = append(tampered, 0x00, 0x02, 0x01, 'x')
		_ = os.WriteFile(wasmPath, tampered, 0600)

		canonical, _ := security.CanonicalizeManifestJSON(mb)
		if _, _, err := store.VerifySkillAgainstStore(wasmPath, canonical, sigPath); err == nil {
			t.Fatalf("expected VerifySkillAgainstStore to fail when WASM binary is altered")
		}
		if _, err := skillSvc.RegisterFromWASM(wasmPath, ""); err == nil {
			t.Fatalf("expected RegisterFromWASM to reject tampered WASM signature")
		}
	})

	// Case B: Modified permissions/capabilities in manifest.json after signing is rejected
	t.Run("TamperedManifestAtInstall", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "case-manifest-tamper")
		_ = os.MkdirAll(dir, 0700)
		wasmPath := filepath.Join(dir, "tampered-manifest.wasm")
		manifestPath := filepath.Join(dir, "manifest.json")
		sigPath := filepath.Join(dir, "tampered-manifest.sig")

		_ = os.WriteFile(wasmPath, minimalValidWASIModule(), 0600)
		v1 := buildPrivilegedManifestV1("tampered-manifest", "Tampered Manifest")
		mb, _ := json.MarshalIndent(v1, "", "  ")
		_ = os.WriteFile(manifestPath, mb, 0600)

		if _, err := security.SignSkillFiles(wasmPath, manifestPath, privPath, sigPath, "Official Signer"); err != nil {
			t.Fatalf("SignSkillFiles: %v", err)
		}

		// Escalate network permission in manifest.json after signing
		v1.Capabilities.Network = &types.NetworkSpec{
			Mode:         "fetch",
			AllowedHosts: []string{"evil.example.com"},
		}
		mbTampered, _ := json.MarshalIndent(v1, "", "  ")
		_ = os.WriteFile(manifestPath, mbTampered, 0600)

		canonicalTampered, _ := security.CanonicalizeManifestJSON(mbTampered)
		if _, _, err := store.VerifySkillAgainstStore(wasmPath, canonicalTampered, sigPath); err == nil {
			t.Fatalf("expected VerifySkillAgainstStore to fail when manifest permissions are altered")
		}
		if _, err := skillSvc.RegisterFromWASM(wasmPath, ""); err == nil {
			t.Fatalf("expected RegisterFromWASM to reject tampered manifest signature")
		}
	})

	// Case C: Tampering with installed .wasm or ManifestV1 before Runner.Execute is rejected
	t.Run("TamperedAfterInstallBeforeExecute", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "case-post-install")
		_ = os.MkdirAll(dir, 0700)
		skillID := "post-install-tamper"
		wasmPath := filepath.Join(dir, skillID+".wasm")
		manifestPath := filepath.Join(dir, "manifest.json")
		sigPath := filepath.Join(dir, skillID+".sig")

		_ = os.WriteFile(wasmPath, minimalValidWASIModule(), 0600)
		v1 := buildPrivilegedManifestV1(skillID, "Post Install Tamper")
		mb, _ := json.MarshalIndent(v1, "", "  ")
		_ = os.WriteFile(manifestPath, mb, 0600)

		if _, err := security.SignSkillFiles(wasmPath, manifestPath, privPath, sigPath, "Official Signer"); err != nil {
			t.Fatalf("SignSkillFiles: %v", err)
		}

		installed, err := skillSvc.RegisterFromWASM(wasmPath, "")
		if err != nil {
			t.Fatalf("RegisterFromWASM: %v", err)
		}

		// Mutate in-memory ManifestV1 capabilities passed to Runner.Execute
		tamperedManifest := installed
		v1Copy := *installed.ManifestV1
		capCopy := *v1Copy.Capabilities
		capCopy.Env = &types.EnvSpec{Vars: map[string]string{"SECRET_TOKEN": "leaked"}}
		v1Copy.Capabilities = &capCopy
		tamperedManifest.ManifestV1 = &v1Copy

		if _, err := runner.Execute(context.Background(), tamperedManifest, nil); err == nil {
			t.Fatalf("expected Runner.Execute to reject mutated in-memory ManifestV1 capabilities")
		}
	})

	// Case D: require_signed_skills: true blocks unsigned Tier3Unverified skills at install and execution
	t.Run("RequireSignedSkillsBlocksUnsigned", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "case-unsigned")
		_ = os.MkdirAll(dir, 0700)
		skillID := "unsigned-skill"
		wasmPath := filepath.Join(dir, skillID+".wasm")
		manifestPath := filepath.Join(dir, "manifest.json")

		_ = os.WriteFile(wasmPath, minimalValidWASIModule(), 0600)
		v1 := buildZeroPrivilegeManifestV1(skillID, "Unsigned Skill")
		mb, _ := json.MarshalIndent(v1, "", "  ")
		_ = os.WriteFile(manifestPath, mb, 0600)

		// With RequireSignedSkills = true, RegisterFromWASM must fail closed.
		skillSvc.SetRequireSignedSkills(true)
		if _, err := skillSvc.RegisterFromWASM(wasmPath, ""); err == nil {
			t.Fatalf("expected RegisterFromWASM to block unsigned skill when require_signed_skills=true")
		}

		// Allow installing when require_signed_skills=false, then verify Runner blocks it when require_signed_skills=true.
		skillSvc.SetRequireSignedSkills(false)
		installedUnsigned, err := skillSvc.RegisterFromWASM(wasmPath, "")
		if err != nil {
			t.Fatalf("RegisterFromWASM with require_signed_skills=false failed: %v", err)
		}
		if installedUnsigned.Tier != types.Tier3Unverified {
			t.Fatalf("expected Tier3Unverified for unsigned skill, got %d", installedUnsigned.Tier)
		}

		runner.SetRequireSignedSkills(true)
		if _, err := runner.Execute(context.Background(), installedUnsigned, nil); err == nil {
			t.Fatalf("expected Runner.Execute to block unsigned Tier3Unverified skill when require_signed_skills=true")
		}
	})
}

func buildMockSDKZip(t *testing.T, innerFileRel, content string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create(innerFileRel)
	if err != nil {
		t.Fatalf("zip.Create: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("zip.Write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	raw := buf.Bytes()
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:])
}

func buildMockSDKTarGz(t *testing.T, innerFileRel, content string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	hdr := &tar.Header{
		Name: innerFileRel,
		Mode: 0755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar.WriteHeader: %v", err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatalf("tar.Write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar.Close: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("gzip.Close: %v", err)
	}
	raw := buf.Bytes()
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:])
}

func TestLote1_3_ToolchainManager_MandatorySHA256Verification(t *testing.T) {
	archiveBytes, validSHA256 := buildMockSDKZip(t, "go/bin/mock-go.txt", "verified-go-sdk-binary")
	if runtime.GOOS != "windows" {
		archiveBytes, validSHA256 = buildMockSDKTarGz(t, "go/bin/mock-go.txt", "verified-go-sdk-binary")
	}

	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(archiveBytes)
	}))
	defer srv.Close()

	downloadURL := srv.URL + "/go1.22.5-test" + ext

	assertNoResidualFiles := func(t *testing.T, sdkDir string) {
		t.Helper()
		entries, err := os.ReadDir(sdkDir)
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("ReadDir(%s): %v", sdkDir, err)
		}
		for _, e := range entries {
			t.Fatalf("expected zero residual files in %s after failed verification, found %s", sdkDir, e.Name())
		}
	}

	// 1. Missing SHA-256 checksum -> Fail-Closed without downloading or extracting
	t.Run("MissingChecksumFailsClosed", func(t *testing.T) {
		sdkDir := filepath.Join(t.TempDir(), "sdks")
		tm := services.NewToolchainManager(sdkDir, false)
		tm.ClearSDKChecksums()
		tm.SetSDKURLOverride("go", downloadURL)

		err := tm.SetupSDK("go")
		if err == nil || !strings.Contains(err.Error(), "missing official SHA-256 checksum") {
			t.Fatalf("expected missing SHA-256 error, got: %v", err)
		}
		assertNoResidualFiles(t, sdkDir)
	})

	// 2. Mismatched/corrupt SHA-256 checksum -> Aborts, deletes temporary archive, extracts nothing
	t.Run("CorruptChecksumFailsClosedAndCleansTemp", func(t *testing.T) {
		sdkDir := filepath.Join(t.TempDir(), "sdks")
		tm := services.NewToolchainManager(sdkDir, false)
		tm.ClearSDKChecksums()
		tm.SetSDKURLOverride("go", downloadURL)

		badSHA := strings.Repeat("a", 64)
		if badSHA == validSHA256 {
			badSHA = strings.Repeat("b", 64)
		}
		tm.SetSDKChecksum(downloadURL, badSHA)

		err := tm.SetupSDK("go")
		if err == nil || !strings.Contains(err.Error(), "SHA-256 checksum mismatch") {
			t.Fatalf("expected SHA-256 checksum mismatch error, got: %v", err)
		}
		assertNoResidualFiles(t, sdkDir)
	})

	// 3. Valid SHA-256 checksum -> Downloads, verifies in streaming, extracts, and removes temp archive
	t.Run("ValidChecksumExtractsAndCleansTemp", func(t *testing.T) {
		sdkDir := filepath.Join(t.TempDir(), "sdks")
		tm := services.NewToolchainManager(sdkDir, false)
		tm.ClearSDKChecksums()
		tm.SetSDKURLOverride("go", downloadURL)
		tm.SetSDKChecksum(downloadURL, validSHA256)

		if err := tm.SetupSDK("go"); err != nil {
			t.Fatalf("SetupSDK with valid SHA-256 failed: %v", err)
		}

		installedFile := filepath.Join(sdkDir, "go", "bin", "mock-go.txt")
		data, err := os.ReadFile(installedFile)
		if err != nil {
			t.Fatalf("expected extracted file at %s: %v", installedFile, err)
		}
		if string(data) != "verified-go-sdk-binary" {
			t.Fatalf("unexpected extracted content: %q", string(data))
		}

		// Ensure no temporary archive files remain in sdkDir
		entries, err := os.ReadDir(sdkDir)
		if err != nil {
			t.Fatalf("ReadDir(%s): %v", sdkDir, err)
		}
		for _, e := range entries {
			if e.Name() != "go" {
				t.Fatalf("unexpected residual temporary file in sdkDir: %s", e.Name())
			}
		}
	})
}
