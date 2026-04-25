package main

import (
	"context"
	"fmt"
	"os"

	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/tui"
	"github.com/spf13/cobra"
	tea "github.com/charmbracelet/bubbletea"
)

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Manage user profile and personalization",
}

var userProfileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Display your current identity and preferences",
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewUserRepository(appStore)
		user, err := repo.GetDefaultUser(context.Background())
		if err != nil {
			fmt.Printf("❌ Error fetching user: %v\n", err)
			return
		}
		if user == nil {
			fmt.Println("❌ No user profile found. Vraxter is running in anonymous mode.")
			return
		}

		fmt.Println("\n👤 Vraxter User Profile")
		fmt.Println("────────────────────────────────────────")
		fmt.Printf("  Name:      %s\n", user.Name)
		fmt.Printf("  Expertise: %s\n", user.Expertise)
		fmt.Printf("  Interests: %s\n", user.Interests)
		fmt.Printf("  Bio:       %s\n", user.Bio)
		fmt.Printf("  Language:  %s\n", user.Language)
		fmt.Printf("  Theme:     %s\n", user.ThemePreference)
		fmt.Println("────────────────────────────────────────")
		fmt.Println("\nTip: Use 'vraxter user update' to change these settings.")
	},
}

var userUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update your profile information",
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewUserRepository(appStore)
		user, err := repo.GetDefaultUser(context.Background())
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			return
		}

		name, _ := cmd.Flags().GetString("name")
		expertise, _ := cmd.Flags().GetString("expertise")
		interests, _ := cmd.Flags().GetString("interests")
		bio, _ := cmd.Flags().GetString("bio")

		if name != "" {
			user.Name = name
		}
		if expertise != "" {
			user.Expertise = expertise
		}
		if interests != "" {
			user.Interests = interests
		}
		if bio != "" {
			user.Bio = bio
		}

		if err := repo.UpdateUser(context.Background(), user); err != nil {
			fmt.Printf("❌ Failed to update profile: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("✅ Profile updated successfully!")
	},
}

var userSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Guided interactive profile setup",
	Run: func(cmd *cobra.Command, args []string) {
		m := tui.NewSetupModel()
		p := tea.NewProgram(m)

		finalModel, err := p.Run()
		if err != nil {
			fmt.Printf("❌ Critical TUI Error: %v\n", err)
			os.Exit(1)
		}

		setup := finalModel.(*tui.SetupModel)
		if !setup.Done {
			fmt.Println("❌ Setup cancelled.")
			return
		}

		repo := db.NewUserRepository(appStore)
		user, err := repo.GetDefaultUser(context.Background())
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			return
		}

		user.Name = setup.Result.Name
		user.Expertise = setup.Result.Expertise
		user.Interests = setup.Result.Interests
		user.Bio = setup.Result.Bio

		if err := repo.UpdateUser(context.Background(), user); err != nil {
			fmt.Printf("❌ Failed to save profile: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("\n✨ Profile saved! Vraxter is now personalized to your needs.")
	},
}

func init() {
	userCmd.AddCommand(userProfileCmd)
	userCmd.AddCommand(userUpdateCmd)
	userCmd.AddCommand(userSetupCmd)

	userUpdateCmd.Flags().String("name", "", "Your display name")
	userUpdateCmd.Flags().String("expertise", "", "Your technical expertise (coding styles, languages)")
	userUpdateCmd.Flags().String("interests", "", "Topics you are interested in")
	userUpdateCmd.Flags().String("bio", "", "A short biography for context injection")
}
