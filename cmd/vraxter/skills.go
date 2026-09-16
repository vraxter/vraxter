// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package main

import (
	"context"
	"fmt"
	"os"

	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/client"
	"github.com/spf13/cobra"
)

var (
	sID       string
	sName     string
	sDesc     string
	sCommand  string
	sEngine   string
	sOfficial bool
	
	downloadDest string
	downloadSrc  bool
	injectManifest string
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage Vraxter capabilities and AI-enabled tools",
}

var listSkillsCmd = &cobra.Command{
	Use:   "list",
	Short: "List all installed skills with their Vraxter Shield trust status",
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
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
			fmt.Println("\n📭 No skills installed yet. Let Vraxter create one for you!")
			return
		}

		fmt.Println("\n◈ VRAXTER SKILLS")
		fmt.Println("------------------------------------------------------------------------------------------------")
		fmt.Printf("%-20s %-30s %-15s %-10s %-8s\n", "ID", "NAME", "TRUST", "ENGINE", "PARAMS")
		fmt.Println("------------------------------------------------------------------------------------------------")

		for _, s := range res.Skills {
			trust := "Community"
			if s.IsOfficial {
				trust = "🟢 Official"
			} else if s.Score >= 4.0 && s.Downloads > 100 {
				trust = "💎 High Rep."
			} else if len(s.Permissions) > 0 {
				trust = "⚠️ Restricted"
			}

			params := "No"
			if s.HasParams {
				params = "Yes"
			}

			fmt.Printf("%-20s %-30s %-15s %-10s %-8s\n",
				s.Id, s.Name, trust, s.Engine, params)
		}
		fmt.Println("------------------------------------------------------------------------------------------------")
	},
}

var infoSkillCmd = &cobra.Command{
	Use:   "info <id>",
	Short: "Display detailed information about a specific skill",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
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
	Use:   "install <hub_id>",
	Short: "Install a skill from the Vraxter Hub",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
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
	Use:   "download <hub_id>",
	Short: "Download a skill without installing it",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
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
	Use:   "inject <path_to_wasm>",
	Short: "Locally inject an offline .wasm skill file into the engine",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
		if err != nil {
			fmt.Printf("❌ Failed to connect to engine: %v\n", err)
			os.Exit(1)
		}
		defer gClient.Close()

		var manifestPtr *string
		if injectManifest != "" {
			manifestPtr = &injectManifest
		}
		res, err := gClient.InjectSkill(context.Background(), &v1.InjectSkillRequest{
			FilePath: args[0],
			ManifestPath: manifestPtr,
		})
		if err != nil {
			fmt.Printf("❌ Injection failed: %v\n", err)
			return
		}

		if !res.Success {
			fmt.Printf("🚫 Failed: %s\n", res.Message)
		} else {
			fmt.Printf("✅ Success: %s (ID: %s)\n", res.Message, res.SkillId)
		}
	},
}

var inspectSkillCmd = &cobra.Command{
	Use:   "inspect <id>",
	Short: "Verify the cryptographic hash of an installed skill",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
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
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
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
	Short: "Manually elevate a skill's tier to Community Verified",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
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

func init() {
	downloadSkillCmd.Flags().StringVar(&downloadDest, "dest", "./", "Destination directory to download the skill")
	downloadSkillCmd.Flags().BoolVar(&downloadSrc, "include-source", false, "Download the source code zip as well")
	injectSkillCmd.Flags().StringVarP(&injectManifest, "manifest", "m", "", "Optional path to a custom manifest.json file for the skill")
	
	skillsCmd.AddCommand(listSkillsCmd)
	skillsCmd.AddCommand(infoSkillCmd)
	skillsCmd.AddCommand(installSkillCmd)
	skillsCmd.AddCommand(downloadSkillCmd)
	skillsCmd.AddCommand(injectSkillCmd)
	skillsCmd.AddCommand(inspectSkillCmd)
	skillsCmd.AddCommand(deleteSkillCmd)
	skillsCmd.AddCommand(trustSkillCmd)

	rootCmd.AddCommand(skillsCmd)
}
