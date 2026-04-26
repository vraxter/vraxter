package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

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
	upUseCases string

	modelUseCases string
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

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "\nID\tALIAS\tPROVIDER\tMODEL\tPRIORITY\tACTIVE\tUSE-CASES")
		fmt.Fprintln(w, "──\t─────\t────────\t─────\t────────\t──────\t─────────")

		for _, m := range models {
			activeStr := "✅"
			if !m.IsActive {
				activeStr = "❌"
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
				m.ID, m.Alias, m.Provider, m.Model, m.Priority, activeStr, m.UseCases)
		}
		w.Flush()
	},
}

var addModelCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new LLM provider model (OpenAI, Anthropic, Ollama)",
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewModelRepository(appStore, appCrypto)
		service := services.NewModelService(repo)

		fmt.Printf("\n☁️  Validating %s API reachability and Keys...\n", modelProvider)
		err := service.AddAndVerifyModel(context.Background(), modelProvider, modelName, modelAPIKey, modelBaseURL, modelPriority, modelUseCases)
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
		var pUseCases *string

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
		if cmd.Flags().Changed("use-cases") {
			pUseCases = &upUseCases
		}

		err := service.UpdateModel(context.Background(), args[0], pAlias, pModel, pKey, pPriority, pActive, pUseCases)
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
	addModelCmd.Flags().StringVar(&modelUseCases, "use-cases", "", "Comma-separated use-case tags (coding,writing,etc)")

	addModelCmd.MarkFlagRequired("provider")
	addModelCmd.MarkFlagRequired("model")

	// Update flags
	updateModelCmd.Flags().StringVar(&upTitle, "alias", "", "New display name")
	updateModelCmd.Flags().StringVar(&upModel, "model", "", "New model ID")
	updateModelCmd.Flags().StringVarP(&upKey, "apikey", "k", "", "New API Key")
	updateModelCmd.Flags().IntVar(&upPriority, "priority", 0, "New Priority")
	updateModelCmd.Flags().BoolVar(&upActive, "active", true, "Set active/inactive")
	updateModelCmd.Flags().StringVar(&upUseCases, "use-cases", "", "New use-case tags")

	modelsCmd.AddCommand(listModelsCmd)
	modelsCmd.AddCommand(addModelCmd)
	modelsCmd.AddCommand(updateModelCmd)
	modelsCmd.AddCommand(checkModelCmd)
	modelsCmd.AddCommand(deleteModelCmd)
	modelsCmd.AddCommand(deactivateModelCmd)
	modelsCmd.AddCommand(activateModelCmd)
	modelsCmd.AddCommand(restoreModelCmd)
	modelsCmd.AddCommand(priorityModelCmd)
	modelsCmd.AddCommand(localModelsCmd)
	// rootCmd.AddCommand(modelsCmd)
}

var localModelsCmd = &cobra.Command{
	Use:   "local",
	Short: "Manage local LLM models using Ollama",
}

var localStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check if Ollama is installed and running",
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewModelRepository(appStore, appCrypto)
		ls := services.NewLocalLLMService(repo)
		status, _ := ls.GetOllamaStatus(cmd.Context())

		fmt.Println("\n🦙 Ollama Status Check:")
		if status.Installed {
			fmt.Println("  ✅ Installed")
		} else {
			fmt.Println("  ❌ Not Installed (Visit https://ollama.com)")
			return
		}

		if status.Running {
			fmt.Printf("  ✅ Running (Version: %s)\n", status.Version)
			fmt.Printf("  📍 Address: %s\n", status.ListenAddr)
			if len(status.Models) > 0 {
				fmt.Println("\n  Available Models:")
				for _, m := range status.Models {
					fmt.Printf("    - %s\n", m)
				}
			}
		} else {
			fmt.Println("  ⚠️  Not Running (Try: vraxter models local start)")
		}
		fmt.Println()
	},
}

var localStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Attempt to start the local Ollama server",
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewModelRepository(appStore, appCrypto)
		ls := services.NewLocalLLMService(repo)
		fmt.Println("🚀 Starting local Ollama server...")
		if err := ls.StartOllamaServer(); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			return
		}
		fmt.Println("✅ Server command sent. Wait a few seconds for it to bind.")
	},
}

var localPullCmd = &cobra.Command{
	Use:   "pull [MODEL]",
	Short: "Download a model from Ollama library and register it",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewModelRepository(appStore, appCrypto)
		ls := services.NewLocalLLMService(repo)
		modelName := args[0]

		fmt.Printf("📥 Pulling model '%s' from Ollama...\n", modelName)
		err := ls.PullOllamaModel(cmd.Context(), modelName, func(percent float64, status string) {
			fmt.Printf("\r   [%-20s] %.1f%% - %-30s", strings.Repeat("=", int(percent/5)), percent, status)
		})
		fmt.Println()

		if err != nil {
			fmt.Printf("\n❌ Pull failed: %v\n", err)
			return
		}

		fmt.Printf("\n✅ Model '%s' downloaded and registered into Vraxter successfully!\n", modelName)
	},
}

var localSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Detect existing local models from Ollama and register them in Vraxter",
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewModelRepository(appStore, appCrypto)
		ls := services.NewLocalLLMService(repo)

		fmt.Println("🔄 Syncing local models from Ollama...")
		count, err := ls.SyncOllamaModels(cmd.Context())
		if err != nil {
			fmt.Printf("❌ Sync failed: %v\n", err)
			return
		}

		if count == 0 {
			fmt.Println("✨ All local models are already synchronized!")
		} else {
			fmt.Printf("✅ Success! Registered %d new local models.\n", count)
		}
	},
}

func init() {
	localModelsCmd.AddCommand(localStatusCmd)
	localModelsCmd.AddCommand(localStartCmd)
	localModelsCmd.AddCommand(localPullCmd)
	localModelsCmd.AddCommand(localSyncCmd)
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
	Short: "Select and LOCK a model as the active default for the CLI",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewModelRepository(appStore, appCrypto)
		id := args[0]
		// 1. Enable it in DB first (if disabled)
		if err := repo.SetActive(id, true); err != nil {
			fmt.Println("❌ Error enabling model in DB:", err)
			return
		}
		// 2. Lock it as the session default
		p := filepath.Join(appConfig.AppDir, "active_model")
		if err := os.WriteFile(p, []byte(id), 0644); err != nil {
			fmt.Println("❌ Error writing active model lock:", err)
			return
		}
		fmt.Printf("✅ Model '%s' activated and locked as default for CLI.\n", id)
	},
}

var restoreModelCmd = &cobra.Command{
	Use:   "restore",
	Short: "Remove model lock and restore automatic use-case routing",
	Run: func(cmd *cobra.Command, args []string) {
		p := filepath.Join(appConfig.AppDir, "active_model")
		_ = os.Remove(p)
		fmt.Println("🔄 CLI Model routing restored to automatic.")
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
		if err := service.UpdateModel(cmd.Context(), args[0], nil, nil, nil, &p, nil, nil); err != nil {
			fmt.Println("❌ Error:", err)
			return
		}
		fmt.Printf("✅ Model '%s' priority set to %d.\n", args[0], p)
	},
}
