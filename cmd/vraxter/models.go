package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/services"
	"github.com/spf13/cobra"
)

var (
	modelProvider string
	modelName     string
	modelAPIKey   string
	modelBaseURL  string
	modelPriority int

	// Update flags
	upTitle    string
	upModel    string
	upKey      string
	upPriority int
	upActive   bool
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Manage Vraxter LLM providers and AI integrations",
}

var listModelsCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured model providers",
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewModelRepository(appStore, appCrypto)
		service := services.NewModelService(repo)

		models, err := service.ListModels()
		if err != nil {
			fmt.Println("❌ Error listing models:", err)
			return
		}

		fmt.Printf("\n%-8s | %-20s | %-10s | %-20s | %-8s | %-6s\n", "ID", "ALIAS", "PROVIDER", "MODEL", "PRIORITY", "ACTIVE")
		fmt.Println(strings.Repeat("-", 85))
		for _, m := range models {
			activeStr := "✅"
			if !m.IsActive {
				activeStr = "❌"
			}
			fmt.Printf("%-8s | %-20s | %-10s | %-20s | %-8d | %-6s\n", m.ID[:8], m.Alias, m.Provider, m.Model, m.Priority, activeStr)
		}
		fmt.Println()
	},
}

var addModelCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new LLM provider model (OpenAI, Anthropic, Ollama)",
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewModelRepository(appStore, appCrypto)
		service := services.NewModelService(repo)

		fmt.Printf("\n☁️  Validating %s API reachability and Keys...\n", modelProvider)
		err := service.AddAndVerifyModel(context.Background(), modelProvider, modelName, modelAPIKey, modelBaseURL, modelPriority)
		if err != nil {
			fmt.Println("❌ Error:", err)
			os.Exit(1)
		}

		fmt.Println("✅ Model configured and API key secured via AES-256 successfully.")
	},
}

var updateModelCmd = &cobra.Command{
	Use:   "update [ID]",
	Short: "Update an existing model configuration",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewModelRepository(appStore, appCrypto)
		service := services.NewModelService(repo)

		var pAlias *string
		var pModel *string
		var pKey *string
		var pPriority *int
		var pActive *bool

		if cmd.Flags().Changed("alias") {
			pAlias = &upTitle
		}
		if cmd.Flags().Changed("model") {
			pModel = &upModel
		}
		if cmd.Flags().Changed("apikey") {
			pKey = &upKey
		}
		if cmd.Flags().Changed("priority") {
			pPriority = &upPriority
		}
		if cmd.Flags().Changed("active") {
			pActive = &upActive
		}

		err := service.UpdateModel(context.Background(), args[0], pAlias, pModel, pKey, pPriority, pActive)
		if err != nil {
			fmt.Println("❌ Error updating model:", err)
			return
		}

		fmt.Printf("✅ Model %s updated successfully.\n", args[0])
	},
}

var checkModelCmd = &cobra.Command{
	Use:   "check [ID]",
	Short: "Verify if a model provider and API key are working correctly",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewModelRepository(appStore, appCrypto)
		service := services.NewModelService(repo)

		fmt.Printf("🔍 Checking health for model %s...\n", args[0])
		err := service.CheckModelHealth(context.Background(), args[0])
		if err != nil {
			fmt.Println("❌ Health check FAILED:", err)
			return
		}

		fmt.Println("✅ Health check PASSED. Model is ready to use.")
	},
}

func init() {
	// Add flags
	addModelCmd.Flags().StringVarP(&modelProvider, "provider", "p", "", "Provider (openai, anthropic, ollama, gemini)")
	addModelCmd.Flags().StringVarP(&modelName, "model", "m", "", "Model Name (e.g., gpt-4o, llama3)")
	addModelCmd.Flags().StringVarP(&modelAPIKey, "apikey", "k", "", "API Key")
	addModelCmd.Flags().StringVar(&modelBaseURL, "url", "", "Custom Base URL")
	addModelCmd.Flags().IntVar(&modelPriority, "priority", 0, "Priority (lower is higher)")

	addModelCmd.MarkFlagRequired("provider")
	addModelCmd.MarkFlagRequired("model")

	// Update flags
	updateModelCmd.Flags().StringVar(&upTitle, "alias", "", "New display name")
	updateModelCmd.Flags().StringVar(&upModel, "model", "", "New model ID")
	updateModelCmd.Flags().StringVarP(&upKey, "apikey", "k", "", "New API Key")
	updateModelCmd.Flags().IntVar(&upPriority, "priority", 0, "New Priority")
	updateModelCmd.Flags().BoolVar(&upActive, "active", true, "Set active/inactive")

	modelsCmd.AddCommand(listModelsCmd)
	modelsCmd.AddCommand(addModelCmd)
	modelsCmd.AddCommand(updateModelCmd)
	modelsCmd.AddCommand(checkModelCmd)
	modelsCmd.AddCommand(deleteModelCmd)
	modelsCmd.AddCommand(deactivateModelCmd)
	modelsCmd.AddCommand(activateModelCmd)
	modelsCmd.AddCommand(priorityModelCmd)
	// rootCmd.AddCommand(modelsCmd)
}

var deleteModelCmd = &cobra.Command{
	Use:   "delete [ID]",
	Short: "Permanently remove a model from the registry",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewModelRepository(appStore, appCrypto)
		if err := repo.DeleteModel(args[0]); err != nil {
			fmt.Println("❌ Error:", err)
			return
		}
		fmt.Printf("✅ Model '%s' permanently deleted.\n", args[0])
	},
}

var deactivateModelCmd = &cobra.Command{
	Use:   "deactivate [ID]",
	Short: "Disable a model without deleting it",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewModelRepository(appStore, appCrypto)
		if err := repo.SetActive(args[0], false); err != nil {
			fmt.Println("❌ Error:", err)
			return
		}
		fmt.Printf("⏸️ Model '%s' deactivated. It will no longer be used.\n", args[0])
	},
}

var activateModelCmd = &cobra.Command{
	Use:   "activate [ID]",
	Short: "Re-enable a previously deactivated model",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewModelRepository(appStore, appCrypto)
		if err := repo.SetActive(args[0], true); err != nil {
			fmt.Println("❌ Error:", err)
			return
		}
		fmt.Printf("✅ Model '%s' activated successfully.\n", args[0])
	},
}
var priorityModelCmd = &cobra.Command{
	Use:   "priority [ID] [VALUE]",
	Short: "Quickly change the priority of a model (lower = higher preference)",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewModelRepository(appStore, appCrypto)
		service := services.NewModelService(repo)

		var p int
		if _, err := fmt.Sscanf(args[1], "%d", &p); err != nil {
			fmt.Println("❌ Priority must be an integer")
			return
		}
		if err := service.UpdateModel(cmd.Context(), args[0], nil, nil, nil, &p, nil); err != nil {
			fmt.Println("❌ Error:", err)
			return
		}
		fmt.Printf("✅ Model '%s' priority set to %d.\n", args[0], p)
	},
}
