package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/services"
	"github.com/patagonicrune/vraxter/pkg/types"
	"github.com/spf13/cobra"
)

var (
	sID       string
	sName     string
	sDesc     string
	sCommand  string
	sEngine   string
	sOfficial bool
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage Vraxter capabilities and AI-enabled tools",
}

var listSkillsCmd = &cobra.Command{
	Use:   "list",
	Short: "List all installed skills with their Vraxter Shield trust status",
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewSkillRepository(appStore)
		service := services.NewSkillService(repo, appConfig.SkillsDir)

		all, err := service.ListSkills()
		if err != nil {
			fmt.Printf("❌ Error listing skills: %v\n", err)
			return
		}

		if len(all) == 0 {
			fmt.Println("\n📭 No skills installed yet. Let Vraxter create one for you!")
			return
		}

		fmt.Printf("\n🛡️  VRAXTER SHIELD — Installed Skills (%d)\n", len(all))
		fmt.Println(strings.Repeat("─", 85))
		fmt.Printf("%-18s %-22s %-10s %-8s %-8s %-12s\n",
			"ID", "NAME", "TRUST", "SCORE", "DL", "ENGINE")
		fmt.Println(strings.Repeat("─", 85))

		for _, s := range all {
			trust := "Community"
			trustIcon := "⚙️ "
			if s.IsOfficial {
				trust = "[OFFICIAL]"
				trustIcon = "✅"
			} else if s.Score >= 4.0 && s.Downloads > 100 {
				trust = "High Rep."
				trustIcon = "⭐"
			} else if len(s.Permissions) > 0 {
				trust = "⚠️  Restricted"
				trustIcon = "🔒"
			}

			checksumOK := "✓"
			if s.Checksum == "" {
				checksumOK = "?"
			}

			fmt.Printf("%s %-17s %-22s %-10s %-8.1f %-8d %-12s [SHA:%s]\n",
				trustIcon, s.ID, s.Name, trust, s.Score, s.Downloads, s.Engine, checksumOK)

			if len(s.Permissions) > 0 {
				fmt.Printf("   └─ Permissions: %s\n", strings.Join(s.Permissions, ", "))
			}
		}
		fmt.Println()
	},
}

var addSkillCmd = &cobra.Command{
	Use:   "add",
	Short: "Register a new binary or WASM skill into Vraxter",
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewSkillRepository(appStore)
		service := services.NewSkillService(repo, appConfig.SkillsDir)

		// Create Manifest
		manifest := types.SkillManifest{
			ID:          sID,
			Name:        sName,
			Description: sDesc,
			Command:     sCommand,
			Engine:      sEngine,
			IsOfficial:  sOfficial,
			Version:     "1.0.0",
			Language:    "go/wasm",
			Tier:        types.Tier3Unverified,
		}

		if manifest.IsOfficial {
			manifest.Tier = types.Tier1Official
		}

		err := service.InstallSkill(manifest)
		if err != nil {
			fmt.Printf("❌ Failed to install skill: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("✅ Skill '%s' [%s] installed successfully.\n", sName, sID)
		fmt.Printf("🛡️  Vraxter Shield is protecting this skill with Checksum validation.\n")
	},
}

var trustSkillCmd = &cobra.Command{
	Use:   "trust <id>",
	Short: "Locally trust a skill, bypassing the Community Shield restrictions",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		repo := db.NewSkillRepository(appStore)

		all, err := repo.GetAllSkills()
		if err != nil {
			fmt.Printf("❌ Error accessing skills db: %v\n", err)
			os.Exit(1)
		}

		var target *types.SkillManifest
		for _, s := range all {
			if s.ID == id {
				target = &s
				break
			}
		}

		if target == nil {
			fmt.Printf("❌ Skill '%s' not found.\n", id)
			os.Exit(1)
		}

		target.IsOfficial = true
		target.Tier = types.Tier1Official

		err = repo.UpsertSkill(*target)
		if err != nil {
			fmt.Printf("❌ Failed to update skill trust status: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("✅ VRAXTER SHIELD: Skill '%s' is now locally trusted and authorized to run.\n", id)
	},
}


func init() {
	addSkillCmd.Flags().StringVar(&sID, "id", "", "Unique Alias ID for the skill (e.g., hello-tool)")
	addSkillCmd.Flags().StringVar(&sName, "name", "", "Display name")
	addSkillCmd.Flags().StringVar(&sDesc, "desc", "", "Description for the AI to understand use-case")
	addSkillCmd.Flags().StringVar(&sCommand, "path", "", "Path to .wasm file or system command")
	addSkillCmd.Flags().StringVar(&sEngine, "engine", "wasm", "Execution engine (wasm, native)")
	addSkillCmd.Flags().BoolVar(&sOfficial, "official", false, "Mark as official Vraxter tool")

	addSkillCmd.MarkFlagRequired("id")
	addSkillCmd.MarkFlagRequired("name")
	addSkillCmd.MarkFlagRequired("path")

	skillsCmd.AddCommand(listSkillsCmd)
	skillsCmd.AddCommand(addSkillCmd)
	skillsCmd.AddCommand(trustSkillCmd)
	rootCmd.AddCommand(skillsCmd)
}
