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

	"github.com/google/uuid"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/spf13/cobra"
)

var userCmd = &cobra.Command{
	Use:     "user",
	Aliases: []string{"operator"},
	Short:   "Manage local operator profile and enclave context",
}

var userProfileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Display the active operator profile",
	Run: func(cmd *cobra.Command, args []string) {
		ensureLocalStore()
		repo := db.NewUserRepository(appStore)
		user, err := repo.GetDefaultUser(context.Background())
		if err != nil {
			fmt.Printf("❌ Error fetching operator profile: %v\n", err)
			return
		}
		if user == nil {
			fmt.Println("No operator profile configured. Run 'vraxter user setup' or 'vraxter user update --name <callsign>'.")
			return
		}

		fmt.Println("\n◈ VRAXTER OPERATOR PROFILE")
		fmt.Println("────────────────────────────────────────────")
		fmt.Printf("  Operator:     %s\n", user.Name)
		fmt.Printf("  Role/Domain:  %s\n", user.Expertise)
		fmt.Printf("  Focus Areas:  %s\n", user.Interests)
		fmt.Printf("  Notes:        %s\n", user.Bio)
		fmt.Printf("  Language:     %s\n", user.Language)
		fmt.Printf("  Theme:        %s\n", user.ThemePreference)
		fmt.Println("────────────────────────────────────────────")
		fmt.Println("\nTip: Use 'vraxter user update' to modify operator metadata.")
	},
}

var userUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update operator profile metadata",
	Run: func(cmd *cobra.Command, args []string) {
		ensureLocalStore()
		ctx := context.Background()
		repo := db.NewUserRepository(appStore)
		user, err := repo.GetDefaultUser(ctx)
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			return
		}

		name, _ := cmd.Flags().GetString("name")
		expertise, _ := cmd.Flags().GetString("expertise")
		interests, _ := cmd.Flags().GetString("interests")
		bio, _ := cmd.Flags().GetString("bio")

		if user == nil {
			if name == "" {
				fmt.Println("❌ No operator profile found. Pass --name or run 'vraxter user setup' first.")
				return
			}
			user = &db.UserProfile{
				ID:              uuid.New().String(),
				Name:            name,
				Language:        "en",
				ThemePreference: "system",
				Expertise:       expertise,
				Interests:       interests,
				Bio:             bio,
			}
			if err := repo.CreateUser(ctx, user); err != nil {
				fmt.Printf("❌ Failed to create operator profile: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✅ Operator profile created.")
			return
		}

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

		if err := repo.UpdateUser(ctx, user); err != nil {
			fmt.Printf("❌ Failed to update operator profile: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("✅ Operator profile updated.")
	},
}

var userSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive operator profile initialization",
	Run: func(cmd *cobra.Command, args []string) {
		var name, expertise, interests, bio string

		fmt.Println("\n◈ VRAXTER OPERATOR PROFILE SETUP")
		fmt.Println("--------------------------------------------------------------------------------")

		fmt.Print("1. Operator Callsign / Name: ")
		fmt.Scanln(&name)

		fmt.Print("2. Role / Engineering Domain (e.g. SRE, SecOps, Telemetry Lead): ")
		fmt.Scanln(&expertise)

		fmt.Print("3. Operational Focus (e.g. Incident Response, Air-Gapped Systems): ")
		fmt.Scanln(&interests)

		fmt.Print("4. Operational Context / Notes (Optional): ")
		fmt.Scanln(&bio)

		if name == "" {
			fmt.Println("❌ Error: Operator name is required.")
			return
		}

		ensureLocalStore()
		ctx := context.Background()
		repo := db.NewUserRepository(appStore)
		user, err := repo.GetDefaultUser(ctx)
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			return
		}

		if user == nil {
			user = &db.UserProfile{
				ID:              uuid.New().String(),
				Name:            name,
				Language:        "en",
				ThemePreference: "system",
				Expertise:       expertise,
				Interests:       interests,
				Bio:             bio,
			}
			if err := repo.CreateUser(ctx, user); err != nil {
				fmt.Printf("❌ Failed to save operator profile: %v\n", err)
				os.Exit(1)
			}
		} else {
			user.Name = name
			user.Expertise = expertise
			user.Interests = interests
			user.Bio = bio

			if err := repo.UpdateUser(ctx, user); err != nil {
				fmt.Printf("❌ Failed to save operator profile: %v\n", err)
				os.Exit(1)
			}
		}

		fmt.Println("\n✅ Operator profile persisted to encrypted local store.")
	},
}

func init() {
	userCmd.AddCommand(userProfileCmd)
	userCmd.AddCommand(userUpdateCmd)
	userCmd.AddCommand(userSetupCmd)

	userUpdateCmd.Flags().String("name", "", "Operator callsign or display name")
	userUpdateCmd.Flags().String("expertise", "", "Operator role or engineering domain")
	userUpdateCmd.Flags().String("interests", "", "Operational focus areas (e.g. SecOps, telemetry, forensics)")
	userUpdateCmd.Flags().String("bio", "", "Operational context notes for local prompt assembly")

	rootCmd.AddCommand(userCmd)
}
