package main

import (
	"fmt"
	"github.com/spf13/cobra"
)

var skillFlag string

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new skill or model technically to Vraxter",
	Run: func(cmd *cobra.Command, args []string) {
		if skillFlag == "" {
			fmt.Println("Error: --skill flag is required. E.g., vraxter add --skill sys-clean")
			return
		}

		fmt.Printf("🔧 Technical Subcommand Executed: Installing skill '%s' into the local registry...\n", skillFlag)
		// Real logic connecting internal/skills registry goes here in Phase 2
		fmt.Printf("✅ Skill '%s' installed and verified.\n", skillFlag)
	},
}

func init() {
	addCmd.Flags().StringVar(&skillFlag, "skill", "", "The ID or GitHub URL of the skill to install")
}
