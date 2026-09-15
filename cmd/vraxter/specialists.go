// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/patagonicrune/vraxter/pkg/types"
	"github.com/spf13/cobra"
)

var specialistsCmd = &cobra.Command{
	Use:   "specialists",
	Short: "Manage Vraxter sub-agents (Specialists)",
}

var createSpecialistModel string

var specialistsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available specialists",
	Run: func(cmd *cobra.Command, args []string) {
		repo := appEngine.SpecialistRepo
		list, err := repo.GetAllSpecialists()
		if err != nil {
			fmt.Printf("Error fetching specialists: %v\n", err)
			return
		}

		if len(list) == 0 {
			fmt.Println("No specialists found. Vraxter can create them autonomously, or you can use 'vraxter specialists create'.")
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tEXPERTISE")
		fmt.Fprintln(w, "--\t----\t---------")
		for _, s := range list {
			fmt.Fprintf(w, "%s\t%s\t%s\n", s.ID, s.Name, s.Expertise)
		}
		w.Flush()
	},
}

var specialistsCreateCmd = &cobra.Command{
	Use:   "create <name> <expertise>",
	Short: "Manually create a specialist profile",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		repo := appEngine.SpecialistRepo
		name := args[0]
		expertise := args[1]

		// 1. Model Validation
		if createSpecialistModel != "" {
			m, err := appEngine.ModelsRepo.GetModelByID(createSpecialistModel)
			if err != nil {
				fmt.Printf("❌ Model '%s' not found in registry.\n", createSpecialistModel)
				printAvailableModels()
				return
			}
			
			// Hard-checks
			if m.Provider == "ollama" && m.BaseURL == "" {
				fmt.Printf("❌ Model '%s' (Ollama) requires a 'base_url' configuration. Please update it.\n", m.ID)
				return
			} else if (m.Provider == "openai" || m.Provider == "gemini" || m.Provider == "anthropic") && m.APIKey == "" {
				fmt.Printf("❌ Model '%s' (%s) requires an API Key. Please add it via 'vraxter models add'.\n", m.ID, m.Provider)
				return
			}
			if !m.IsActive {
				fmt.Printf("❌ Model '%s' is marked as inactive.\n", m.ID)
				return
			}
		}

		s := types.Specialist{
			ID:           uuid.New().String(),
			Name:         name,
			Expertise:    expertise,
			ModelID:      createSpecialistModel,
		}
		
		if err := repo.CreateSpecialist(s); err != nil {
			fmt.Printf("❌ Error creating specialist: %v\n", err)
			return
		}
		modelMsg := "Default Engine Model"
		if createSpecialistModel != "" {
			modelMsg = createSpecialistModel
		}
		fmt.Printf("✅ Specialist '%s' created successfully! (ID: %s, Brain: %s)\n", name, s.ID, modelMsg)
		fmt.Printf("You can now talk to it: vraxter -a %s \"Your message\"\n", s.ID)
	},
}

func printAvailableModels() {
	all, err := appEngine.ModelsRepo.GetAllModels()
	if err != nil || len(all) == 0 {
		fmt.Println("No models configured.")
		return
	}
	fmt.Println("\nAvailable Models:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPROVIDER\tMODEL\tSTATUS")
	for _, m := range all {
		status := "✅ READY"
		if m.Provider == "ollama" && m.BaseURL == "" {
			status = "❌ MISSING BaseURL"
		} else if (m.Provider == "openai" || m.Provider == "gemini" || m.Provider == "anthropic") && m.APIKey == "" {
			status = "❌ MISSING API Key"
		} else if !m.IsActive {
			status = "⏸️ INACTIVE"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.ID, m.Provider, m.Model, status)
	}
	w.Flush()
}

var specialistsDeleteCmd = &cobra.Command{
	Use:   "delete <identifier>",
	Short: "Delete a specialist profile (ID or Name)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		identifier := args[0]

		if appEngine.GetResolver() == nil {
			fmt.Println("❌ Error: Engine resolver not initialized.")
			return
		}

		// Use the same safe semantic search fallback used by the LLM
		spec, err := appEngine.GetResolver().FindSpecialistSemantically(cmd.Context(), identifier)
		if err != nil {
			fmt.Printf("❌ Failed to isolate specialist safely: %v\n", err)
			return
		}

		if err := appEngine.SpecialistRepo.DeleteSpecialist(spec.ID); err != nil {
			fmt.Printf("❌ Error deleting specialist: %v\n", err)
			return
		}

		fmt.Printf("🗑️ Specialist '%s' (ID: %s) permanently deleted.\n", spec.Name, spec.ID)
	},
}

func init() {
	specialistsCreateCmd.Flags().StringVarP(&createSpecialistModel, "model", "m", "", "Specific Model ID to assign to this specialist's isolated context")

	specialistsCmd.AddCommand(specialistsListCmd)
	specialistsCmd.AddCommand(specialistsCreateCmd)
	specialistsCmd.AddCommand(specialistsDeleteCmd)
	rootCmd.AddCommand(specialistsCmd)
}
