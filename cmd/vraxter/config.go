package main

import (
	"context"
	"fmt"
	"os"

	"github.com/patagonicrune/vraxter/internal/client"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage Vraxter global configuration",
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all active configuration values",
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
		if err != nil {
			fmt.Printf("❌ Failed to connect to engine: %v\n", err)
			os.Exit(1)
		}
		defer gClient.Close()

		res, err := gClient.GetConfig(context.Background())
		if err != nil {
			fmt.Printf("❌ Error fetching config: %v\n", err)
			return
		}

		fmt.Println("\n⚙️ VRAXTER GLOBAL CONFIGURATION")
		fmt.Println("--------------------------------------------------")
		fmt.Printf("Privacy Policy         : %s\n", res.PrivacyPolicy)
		fmt.Printf("Hub URL                : %s\n", res.HubUrl)
		fmt.Printf("Require Skill Approval : %v\n", res.RequireSkillApproval)
		fmt.Println("--------------------------------------------------")
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value (privacy_policy, hub_url)",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		key := args[0]
		val := args[1]

		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
		if err != nil {
			fmt.Printf("❌ Failed to connect to engine: %v\n", err)
			os.Exit(1)
		}
		defer gClient.Close()

		var pp, hurl *string
		var rsa *bool
		if key == "privacy_policy" {
			pp = &val
		} else if key == "hub_url" {
			hurl = &val
		} else if key == "require_skill_approval" {
			b := val == "true"
			rsa = &b
		} else {
			fmt.Printf("❌ Unknown configuration key '%s'\n", key)
			return
		}

		_, err = gClient.UpdateConfig(context.Background(), pp, hurl, rsa)
		if err != nil {
			fmt.Printf("❌ Error updating config: %v\n", err)
			return
		}

		fmt.Printf("✅ Successfully updated %s to %s\n", key, val)
	},
}

func init() {
	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configSetCmd)
	rootCmd.AddCommand(configCmd)
}
