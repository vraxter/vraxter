package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/llm"
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

		fmt.Println("\n◈ VRAXTER SKILLS")
		fmt.Println("------------------------------------------------------------------------------------------------")
		fmt.Printf("%-20s %-30s %-15s %-10s %-8s\n", "ID", "NAME", "TRUST", "ENGINE", "PARAMS")
		fmt.Println("------------------------------------------------------------------------------------------------")

		for _, s := range all {
			trust := "Community"
			if s.IsOfficial {
				trust = "🟢 Official"
			} else if s.Score >= 4.0 && s.Downloads > 100 {
				trust = "💎 High Rep."
			} else if len(s.Permissions) > 0 {
				trust = "⚠️ Restricted"
			}

			params := "No"
			if s.ParamsSchema != "" {
				params = "Yes"
			}

			fmt.Printf("%-20s %-30s %-15s %-10s %-8s\n",
				s.ID, s.Name, trust, s.Engine, params)
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
		repo := db.NewSkillRepository(appStore)
		s, err := repo.FindSkill(id)
		if err != nil {
			fmt.Printf("❌ Skill '%s' not found.\n", id)
			return
		}

		fmt.Printf("\n◈ SKILL INFO: %s\n", s.Name)
		fmt.Println("------------------------------------------------------------------------------------------------")
		fmt.Printf("%-20s: %s\n", "ID", s.ID)
		fmt.Printf("%-20s: %s\n", "Description", s.Description)
		fmt.Printf("%-20s: %s (%s)\n", "Language", s.Language, s.Engine)
		fmt.Printf("%-20s: %s\n", "Version", s.Version)
		fmt.Printf("%-20s: %s\n", "Path", s.Command)
		
		trust := "Community"
		if s.IsOfficial {
			trust = "🛡️  Vraxter Official (Trusted)"
		}
		fmt.Printf("%-20s: %s\n", "Trust Level", trust)

		if s.ParamsSchema != "" {
			fmt.Printf("\n📋 PARAMETERS SCHEMA:\n%s\n", s.ParamsSchema)
		} else if s.ParamRegex != "" {
			fmt.Printf("\n📋 PARAMETERS (Regex Matcher):\n   %s\n", s.ParamRegex)
		} else {
			fmt.Printf("\n📋 PARAMETERS: No strict schema defined (uses Natural Language).\n")
		}

		if len(s.Permissions) > 0 {
			fmt.Printf("\n%-20s: %s\n", "PERMISSIONS", strings.Join(s.Permissions, ", "))
		}

		if len(s.Examples) > 0 {
			fmt.Printf("\n💡 USAGE EXAMPLES:\n")
			for _, ex := range s.Examples {
				fmt.Printf("  • %s\n", ex)
			}
		}
		fmt.Println("------------------------------------------------------------------------------------------------")
	},
}

var registerSkillCmd = &cobra.Command{
	Use:   "register",
	Short: "Manually register a pre-compiled binary or WASM skill",
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

var autoAddSkillCmd = &cobra.Command{
	Use:   "add <name> <description>",
	Short: "Auto-generate and compile a new WASM skill from a natural language description",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		bootstrap()

		name := args[0]
		desc := strings.Join(args[1:], " ")

		codegen := appEngine.GetCodeGen()
		if codegen == nil {
			fmt.Println("❌ Error: CodeGen engine not available.")
			return
		}

		out := make(chan llm.StreamEvent, 10)
		ctx := context.Background()

		go func() {
			_ = codegen.GenerateAndCompile(ctx, out, name, desc, desc, "go")
			close(out)
		}()

		for event := range out {
			switch event.Type {
			case llm.EventTypeStatus:
				fmt.Printf("💡 %s\n", event.Content)
			case llm.EventTypeToken:
				fmt.Print(event.Content)
			case llm.EventTypeError:
				fmt.Printf("\n❌ Error: %v\n", event.Err)
			}
		}
		fmt.Println()
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
	registerSkillCmd.Flags().StringVar(&sID, "id", "", "Unique Alias ID for the skill (e.g., hello-tool)")
	registerSkillCmd.Flags().StringVar(&sName, "name", "", "Display name")
	registerSkillCmd.Flags().StringVar(&sDesc, "desc", "", "Description for the AI to understand use-case")
	registerSkillCmd.Flags().StringVar(&sCommand, "path", "", "Path to .wasm file or system command")
	registerSkillCmd.Flags().StringVar(&sEngine, "engine", "wasm", "Execution engine (wasm, native)")
	registerSkillCmd.Flags().BoolVar(&sOfficial, "official", false, "Mark as official Vraxter tool")

	registerSkillCmd.MarkFlagRequired("id")
	registerSkillCmd.MarkFlagRequired("name")
	registerSkillCmd.MarkFlagRequired("path")

	skillsCmd.AddCommand(listSkillsCmd)
	skillsCmd.AddCommand(infoSkillCmd)
	skillsCmd.AddCommand(autoAddSkillCmd)
	skillsCmd.AddCommand(registerSkillCmd)
	skillsCmd.AddCommand(trustSkillCmd)
	skillsCmd.AddCommand(execSkillCmd)
	skillsCmd.AddCommand(deleteSkillCmd)
}
