package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-runewidth"
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
		header := fmt.Sprintf("%-15s | %-18s | %-12s | %-6s | %-4s | %-10s | %-6s", "ID", "NAME", "TRUST", "SCORE", "DL", "ENGINE", "SIG")
		fmt.Println(header)
		fmt.Println(strings.Repeat("―", 90))

		for _, s := range all {
			trust := "Community"
			if s.IsOfficial {
				trust = "[OFFICIAL]"
			} else if s.Score >= 4.0 && s.Downloads > 100 {
				trust = "High Rep."
			} else if len(s.Permissions) > 0 {
				trust = "⚠️  Restricted"
			}

			checksumOK := "✓"
			if s.Checksum == "" {
				checksumOK = "?"
			}

			// Cell-aware padding for perfect alignment
			idStr := runewidth.FillRight(runewidth.Truncate(s.ID, 15, ".."), 15)
			nameStr := runewidth.FillRight(runewidth.Truncate(s.Name, 18, ".."), 18)
			trustStr := runewidth.FillRight(runewidth.Truncate(trust, 12, ".."), 12)
			scoreStr := runewidth.FillRight(fmt.Sprintf("%.1f", s.Score), 6)
			dlStr := runewidth.FillRight(fmt.Sprintf("%d", s.Downloads), 4)
			engineStr := runewidth.FillRight(s.Engine, 10)
			sigStr := runewidth.FillRight(checksumOK, 6)

			fmt.Printf("%s | %s | %s | %s | %s | %s | %s\n",
				idStr, nameStr, trustStr, scoreStr, dlStr, engineStr, sigStr)

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

var execSkillCmd = &cobra.Command{
	Use:   "exec [skill_id] [params...]",
	Short: "Execute a skill completely natively without involving the LLM",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		skillID := args[0]
		paramStr := ""
		if len(args) > 1 {
			paramStr = strings.Join(args[1:], " ")
		}

		ctx := cmd.Context()
		sessionID := resolveSession()

		// Bootstrap the entire engine quietly if needed
		// (main.go PersistentPreRun should cover it, but just in case)

		fmt.Printf("🚀 Firing native local execution for [%s]...\n", skillID)

		query := fmt.Sprintf("!%s %s", skillID, paramStr)
		stream, err := appEngine.ProcessRawIntent(ctx, sessionID, query, agentFlag, "")
		if err != nil {
			fmt.Printf("❌ Engine Fast-Path Error: %v\n", err)
			return
		}

		processStream(stream)
	},
}

var deleteSkillCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a skill from Vraxter",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		repo := db.NewSkillRepository(appStore)
		err := repo.DeleteSkill(id)
		if err != nil {
			fmt.Printf("❌ Failed to delete skill: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Skill '%s' deleted successfully.\n", id)
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
	skillsCmd.AddCommand(execSkillCmd)
	skillsCmd.AddCommand(deleteSkillCmd)
}
