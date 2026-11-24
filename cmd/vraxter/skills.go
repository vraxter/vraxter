// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/client"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/pkg/types"
	"github.com/spf13/cobra"
)

var (
	sID       string
	sName     string
	sDesc     string
	sCommand  string
	sEngine   string
	sOfficial bool

	downloadDest   string
	downloadSrc    bool
	injectManifest string

	keygenOutDir   string
	keygenName     string
	signWasmPath   string
	signManifest   string
	signKeyPath    string
	signOutPath    string
	signSignerName string
	verifyWasmPath string
	verifyManifest string
	verifySigPath  string
	verifyPubPath  string
	trustPubPath   string
	trustSigner    string
	trustTierFlag  int
)

var skillsCmd = &cobra.Command{
	Use:     "skills",
	Aliases: []string{"skill"},
	Short:   "Manage signed WASM/WASI skills, Ed25519 PKI keys, and trusted signers",
	Long: `Manage capability-bounded WASM/WASI skills and cryptographic supply-chain trust.

Trust is determined exclusively by Ed25519 signatures verified against local
trusted_signers/ keys and enclave policy — never by external reputation scores.

Offline PKI commands (keygen, sign, verify, trust add) run directly without
opening the runtime database or starting a daemon.`,
}

var listSkillsCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered WASM skills and their Ed25519 trust tier",
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient(client.DefaultEndpoint(), ensureDaemonKey())
		if err != nil {
			fmt.Printf("❌ Failed to connect to engine: %v\n", err)
			os.Exit(1)
		}
		defer gClient.Close()

		res, err := gClient.ListSkills(context.Background(), &v1.ListSkillsRequest{})
		if err != nil {
			fmt.Printf("❌ Error listing skills: %v\n", err)
			return
		}

		if len(res.Skills) == 0 {
			fmt.Println("\nNo WASM skills registered. Inject a signed artifact with 'vraxter skill inject <skill.wasm> --manifest <manifest.json>'.")
			return
		}

		fmt.Println("\n◈ VRAXTER REGISTERED SKILLS")
		fmt.Println("----------------------------------------------------------------------------------------------------")
		fmt.Printf("%-20s %-30s %-20s %-10s %-8s\n", "ID", "NAME", "TRUST TIER", "ENGINE", "PARAMS")
		fmt.Println("----------------------------------------------------------------------------------------------------")

		for _, s := range res.Skills {
			trust := "Untrusted (Tier 3)"
			if s.IsOfficial || s.Tier == int32(types.Tier1Official) {
				trust = "Tier 1 (Official)"
			} else if s.Tier == int32(types.Tier2CommunityVerified) {
				trust = "Tier 2 (Verified)"
			} else if len(s.Permissions) > 0 {
				trust = "Restricted (Tier 3)"
			}

			params := "No"
			if s.HasParams {
				params = "Yes"
			}

			fmt.Printf("%-20s %-30s %-20s %-10s %-8s\n",
				s.Id, s.Name, trust, s.Engine, params)
		}
		fmt.Println("----------------------------------------------------------------------------------------------------")
	},
}

var infoSkillCmd = &cobra.Command{
	Use:   "info <id>",
	Short: "Display detailed information about a specific skill",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		gClient, err := client.NewManagementClient(client.DefaultEndpoint(), ensureDaemonKey())
		if err != nil {
			fmt.Printf("❌ Failed to connect to engine: %v\n", err)
			os.Exit(1)
		}
		defer gClient.Close()

		res, err := gClient.GetSkillInfo(context.Background(), &v1.GetSkillInfoRequest{Id: id})
		if err != nil {
			fmt.Printf("❌ Skill not found: %v\n", err)
			return
		}

		s := res.Skill
		fmt.Printf("\n📦 Skill: %s\n", s.Id)
		fmt.Println("----------------------------------------")
		fmt.Printf("Name:        %s\n", s.Name)
		fmt.Printf("Description: %s\n", s.Description)
		fmt.Printf("Version:     %s\n", s.Version)
		fmt.Printf("Engine:      %s\n", s.Engine)
		fmt.Printf("Checksum:    %s\n", s.Checksum)
		fmt.Printf("Official:    %v\n", s.IsOfficial)
		fmt.Printf("Tier:        %d\n", s.Tier)

		if len(s.Permissions) > 0 {
			fmt.Println("\n🔒 Permissions Requested:")
			for _, p := range s.Permissions {
				fmt.Printf("  - %s\n", p)
			}
		}

		if res.Schema != "" {
			fmt.Println("\n🔧 Parameter Schema:")
			fmt.Println(res.Schema)
		}
	},
}

var installSkillCmd = &cobra.Command{
	Use:   "install <artifact_id>",
	Short: "Install a skill from a configured private enterprise registry (blocked in airgap mode)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient(client.DefaultEndpoint(), ensureDaemonKey())
		if err != nil {
			fmt.Printf("❌ Failed to connect to engine: %v\n", err)
			os.Exit(1)
		}
		defer gClient.Close()

		res, err := gClient.InstallSkill(context.Background(), &v1.InstallSkillRequest{Id: args[0]})
		if err != nil {
			fmt.Printf("❌ Installation failed: %v\n", err)
			return
		}

		if !res.Success {
			fmt.Printf("🚫 Blocked: %s\n", res.Message)
		} else {
			fmt.Printf("✅ Success: %s\n", res.Message)
		}
	},
}

var downloadSkillCmd = &cobra.Command{
	Use:   "download <artifact_id>",
	Short: "Download a skill from a configured private enterprise registry (blocked in airgap mode)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient(client.DefaultEndpoint(), ensureDaemonKey())
		if err != nil {
			fmt.Printf("❌ Failed to connect to engine: %v\n", err)
			os.Exit(1)
		}
		defer gClient.Close()

		res, err := gClient.DownloadSkill(context.Background(), &v1.DownloadSkillRequest{
			Id: args[0],
			DestPath: downloadDest,
			IncludeSource: downloadSrc,
		})
		if err != nil {
			fmt.Printf("❌ Download failed: %v\n", err)
			return
		}

		if !res.Success {
			fmt.Printf("🚫 Blocked: %s\n", res.Message)
		} else {
			fmt.Printf("✅ Success: %s\n", res.Message)
		}
	},
}

var injectSkillCmd = &cobra.Command{
	Use:   "inject <path_to_wasm_or_dir> [path_to_manifest]",
	Short: "Locally inject an offline .wasm skill file into the engine",
	Args:  cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient(client.DefaultEndpoint(), ensureDaemonKey())
		if err != nil {
			fmt.Printf("❌ Failed to connect to engine: %v\n", err)
			os.Exit(1)
		}
		defer gClient.Close()

		var manifestPtr *string
		if len(args) == 2 && injectManifest == "" {
			injectManifest = args[1]
		}
		if injectManifest != "" {
			manifestPtr = &injectManifest
		}
		res, err := gClient.InjectSkill(context.Background(), &v1.InjectSkillRequest{
			FilePath:     args[0],
			ManifestPath: manifestPtr,
		})
		if err != nil {
			fmt.Printf("❌ Injection failed: %v\n", err)
			return
		}

		if !res.Success {
			fmt.Printf("🚫 Failed: %s\n", res.Message)
		} else {
			if res.SkillId != "" && !strings.Contains(res.Message, res.SkillId) {
				fmt.Printf("✅ %s (ID: %s)\n", res.Message, res.SkillId)
			} else {
				fmt.Printf("✅ %s\n", res.Message)
			}
		}
	},
}

var inspectSkillCmd = &cobra.Command{
	Use:   "inspect <id>",
	Short: "Verify the cryptographic hash of an installed skill",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient(client.DefaultEndpoint(), ensureDaemonKey())
		if err != nil {
			fmt.Printf("❌ Failed to connect to engine: %v\n", err)
			os.Exit(1)
		}
		defer gClient.Close()

		res, err := gClient.InspectSkill(context.Background(), &v1.InspectSkillRequest{Id: args[0]})
		if err != nil {
			fmt.Printf("❌ Inspection failed: %v\n", err)
			return
		}

		if !res.IsValid {
			fmt.Printf("🛑 SECURITY WARNING: %s\n", res.Message)
			fmt.Printf("Expected: %s\nActual:   %s\n", res.ExpectedChecksum, res.ActualChecksum)
		} else {
			fmt.Printf("🛡️ Validated: %s\n", res.Message)
		}
	},
}

var deleteSkillCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete an installed skill",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient(client.DefaultEndpoint(), ensureDaemonKey())
		if err != nil {
			fmt.Printf("❌ Failed to connect to engine: %v\n", err)
			os.Exit(1)
		}
		defer gClient.Close()

		res, err := gClient.DeleteSkill(context.Background(), &v1.DeleteSkillRequest{Id: args[0]})
		if err != nil {
			fmt.Printf("❌ Deletion failed: %v\n", err)
			return
		}

		if !res.Success {
			fmt.Printf("🚫 Failed: %s\n", res.Message)
		} else {
			fmt.Printf("🗑️ %s\n", res.Message)
		}
	},
}

var trustSkillCmd = &cobra.Command{
	Use:   "trust <id>",
	Short: "Manage trusted Ed25519 signers (trust add) or locally elevate a skill to Verified Tier 2",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient(client.DefaultEndpoint(), ensureDaemonKey())
		if err != nil {
			fmt.Printf("❌ Failed to connect to engine: %v\n", err)
			os.Exit(1)
		}
		defer gClient.Close()

		res, err := gClient.TrustSkill(context.Background(), &v1.TrustSkillRequest{Id: args[0]})
		if err != nil {
			fmt.Printf("❌ Trust update failed: %v\n", err)
			return
		}

		if !res.Success {
			fmt.Printf("🚫 Failed: %s\n", res.Message)
		} else {
			fmt.Printf("✅ %s\n", res.Message)
		}
	},
}

var keygenSkillCmd = &cobra.Command{
	Use:   "keygen",
	Short: "Generate an Ed25519 keypair for signing Vraxter skills",
	Run: func(cmd *cobra.Command, args []string) {
		outDir := strings.TrimSpace(keygenOutDir)
		if outDir == "" {
			outDir = filepath.Join(appConfig.AppDir, "keys")
		}
		name := strings.TrimSpace(keygenName)
		if name == "" {
			name = "vraxter-developer"
		}
		kp, err := security.GenerateEd25519KeyPair(name)
		if err != nil {
			fmt.Printf("❌ Key generation failed: %v\n", err)
			os.Exit(1)
		}
		privPath, pubPath, err := security.WriteEd25519KeyPairFiles(outDir, name, kp)
		if err != nil {
			fmt.Printf("❌ Failed saving keypair: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🔑 Ed25519 Keypair generated (Key ID: %s)\n", kp.KeyID)
		fmt.Printf("   Private Key (0600): %s\n", privPath)
		fmt.Printf("   Public Key  (0644): %s\n", pubPath)
	},
}

var signSkillCmd = &cobra.Command{
	Use:   "sign",
	Short: "Sign a .wasm binary and skill-manifest/1 with an Ed25519 private key",
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(signWasmPath) == "" || strings.TrimSpace(signManifest) == "" || strings.TrimSpace(signKeyPath) == "" {
			fmt.Println("❌ Error: --wasm, --manifest, and --key are required")
			os.Exit(1)
		}
		outPath := strings.TrimSpace(signOutPath)
		if outPath == "" {
			base := strings.TrimSuffix(filepath.Base(signWasmPath), filepath.Ext(signWasmPath))
			outPath = filepath.Join(filepath.Dir(signWasmPath), base+".sig")
		}
		env, err := security.SignSkillFiles(signWasmPath, signManifest, signKeyPath, outPath, signSignerName)
		if err != nil {
			fmt.Printf("❌ Signing failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Skill signed with Ed25519 (Key ID: %s)\n", env.KeyID)
		fmt.Printf("   WASM SHA-256:     %s\n", env.WASMSha256)
		fmt.Printf("   Manifest SHA-256: %s\n", env.ManifestSha256)
		fmt.Printf("   Signature File:   %s\n", outPath)
	},
}

var verifySkillCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify a skill's .wasm binary and manifest against an Ed25519 .sig file",
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(verifyWasmPath) == "" || strings.TrimSpace(verifyManifest) == "" {
			fmt.Println("❌ Error: --wasm and --manifest are required")
			os.Exit(1)
		}
		sigPath := strings.TrimSpace(verifySigPath)
		if sigPath == "" {
			sigPath = security.DiscoverSkillSignaturePath(verifyWasmPath, verifyManifest, "")
		}
		if sigPath == "" {
			fmt.Println("❌ Error: --sig not provided and no adjacent .sig file found")
			os.Exit(1)
		}

		manifestBytes, err := os.ReadFile(verifyManifest)
		if err != nil {
			fmt.Printf("❌ Failed reading manifest: %v\n", err)
			os.Exit(1)
		}
		canonicalManifest, err := security.CanonicalizeManifestJSON(manifestBytes)
		if err != nil {
			fmt.Printf("❌ Failed canonicalizing manifest: %v\n", err)
			os.Exit(1)
		}

		if strings.TrimSpace(verifyPubPath) != "" {
			pubKey, _, err := security.LoadEd25519PublicKey(verifyPubPath)
			if err != nil {
				fmt.Printf("❌ Failed loading public key: %v\n", err)
				os.Exit(1)
			}
			env, err := security.LoadSignatureEnvelope(sigPath)
			if err != nil {
				fmt.Printf("❌ Invalid signature envelope: %v\n", err)
				os.Exit(1)
			}
			wasmHash, err := security.HashFileSHA256(verifyWasmPath)
			if err != nil {
				fmt.Printf("❌ Failed hashing WASM binary: %v\n", err)
				os.Exit(1)
			}
			if err := security.VerifyEnvelopeWithPublicKey(env, pubKey, wasmHash, canonicalManifest); err != nil {
				fmt.Printf("🛑 Verification FAILED: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("🛡️ Verified Ed25519 signature (Key ID: %s)\n", env.KeyID)
			return
		}

		store := security.NewTrustedSignerStore(appConfig.TrustedSignersDir)
		signer, env, err := store.VerifySkillAgainstStore(verifyWasmPath, canonicalManifest, sigPath)
		if err != nil {
			fmt.Printf("🛑 Verification FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🛡️ Verified Ed25519 signature against trusted signer '%s' (Key ID: %s, Tier: %d)\n", signer.Name, env.KeyID, signer.TrustTier)
	},
}

var trustAddSkillCmd = &cobra.Command{
	Use:   "add",
	Short: "Register a trusted Ed25519 public key in trusted_signers/",
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(trustPubPath) == "" {
			fmt.Println("❌ Error: --pub is required")
			os.Exit(1)
		}
		pubKey, headerName, err := security.LoadEd25519PublicKey(trustPubPath)
		if err != nil {
			fmt.Printf("❌ Failed loading public key: %v\n", err)
			os.Exit(1)
		}
		name := strings.TrimSpace(trustSigner)
		if name == "" {
			name = headerName
		}
		tier := types.SkillTier(trustTierFlag)
		if tier != types.Tier1Official && tier != types.Tier2CommunityVerified {
			fmt.Println("❌ Error: --tier must be 1 (Official) or 2 (Verified)")
			os.Exit(1)
		}
		store := security.NewTrustedSignerStore(appConfig.TrustedSignersDir)
		entry, destPath, err := store.AddSigner(pubKey, name, tier)
		if err != nil {
			fmt.Printf("❌ Failed adding trusted signer: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Trusted signer '%s' registered (Key ID: %s, Tier: %d)\n   Stored at: %s\n", entry.Name, entry.KeyID, entry.TrustTier, destPath)
	},
}

func init() {
	downloadSkillCmd.Flags().StringVar(&downloadDest, "dest", "./", "Destination directory to download the skill")
	downloadSkillCmd.Flags().BoolVar(&downloadSrc, "include-source", false, "Download the source code zip as well")
	injectSkillCmd.Flags().StringVarP(&injectManifest, "manifest", "M", "", "Optional path to a custom manifest.json file for the skill")

	keygenSkillCmd.Flags().StringVar(&keygenOutDir, "out", "", "Output directory for generated Ed25519 keypair")
	keygenSkillCmd.Flags().StringVar(&keygenName, "name", "vraxter-developer", "Signer identity name")

	signSkillCmd.Flags().StringVar(&signWasmPath, "wasm", "", "Path to the .wasm binary to sign")
	signSkillCmd.Flags().StringVar(&signManifest, "manifest", "", "Path to the skill manifest.json to sign")
	signSkillCmd.Flags().StringVar(&signKeyPath, "key", "", "Path to the Ed25519 private key file")
	signSkillCmd.Flags().StringVar(&signOutPath, "out", "", "Output path for the .sig signature file")
	signSkillCmd.Flags().StringVar(&signSignerName, "name", "", "Optional signer name override")

	verifySkillCmd.Flags().StringVar(&verifyWasmPath, "wasm", "", "Path to the .wasm binary to verify")
	verifySkillCmd.Flags().StringVar(&verifyManifest, "manifest", "", "Path to the skill manifest.json to verify")
	verifySkillCmd.Flags().StringVar(&verifySigPath, "sig", "", "Path to the .sig signature file")
	verifySkillCmd.Flags().StringVar(&verifyPubPath, "pub", "", "Optional path to an explicit Ed25519 public key (defaults to trusted_signers/ store)")

	trustAddSkillCmd.Flags().StringVar(&trustPubPath, "pub", "", "Path to the Ed25519 public key file (.pub)")
	trustAddSkillCmd.Flags().StringVar(&trustSigner, "name", "", "Trusted signer name")
	trustAddSkillCmd.Flags().IntVar(&trustTierFlag, "tier", int(types.Tier2CommunityVerified), "Trust tier to grant (1 = Official, 2 = Verified)")
	trustSkillCmd.AddCommand(trustAddSkillCmd)

	skillsCmd.AddCommand(listSkillsCmd)
	skillsCmd.AddCommand(infoSkillCmd)
	skillsCmd.AddCommand(installSkillCmd)
	skillsCmd.AddCommand(downloadSkillCmd)
	skillsCmd.AddCommand(injectSkillCmd)
	skillsCmd.AddCommand(inspectSkillCmd)
	skillsCmd.AddCommand(deleteSkillCmd)
	skillsCmd.AddCommand(trustSkillCmd)
	skillsCmd.AddCommand(keygenSkillCmd)
	skillsCmd.AddCommand(signSkillCmd)
	skillsCmd.AddCommand(verifySkillCmd)

	rootCmd.AddCommand(skillsCmd)
}
