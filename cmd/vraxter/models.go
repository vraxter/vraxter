package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	v1 "github.com/patagonicrune/vraxter/api/v1"
	"github.com/patagonicrune/vraxter/internal/client"
	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/services"
	"github.com/patagonicrune/vraxter/pkg/types"
	"github.com/spf13/cobra"
)

var (
	modelProviderReg string
	modelIDRemote    string
	modelPriorityVal int
	modelAliasVal    string
	modelFilterProv  string
	modelUpdatePrio  int
	modelUpdateActive string
	modelUseCasePrios string
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Manage Vraxter models linked to providers",
}

var listModelsCmd = &cobra.Command{
	Use:   "list",
	Short: "List all added models",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		// 1. Try gRPC first
		gClient, err := client.NewManagementClient(":50051")
		if err == nil {
			defer gClient.Close()
			models, err := gClient.ListModels(ctx, modelFilterProv)
			if err == nil {
				renderModelsRPC(models)
				return
			}
		}

		// 2. Fallback to Local DB
		if os.Getenv("VRAXTER_INTERNAL_SESSION") != "true" {
			fmt.Printf("💡 [LOCAL FALLBACK] Daemon offline. Accessing DB directly.\n")
		}
		manager := getModelManager()
		models, err := manager.ListModels()
		if err != nil {
			fmt.Println("❌ Error listing models:", err)
			return
		}

		// filter local results if flag set
		if modelFilterProv != "" {
			var filtered []types.ModelConfig
			for _, m := range models {
				if m.ProviderID == modelFilterProv || m.Provider == modelFilterProv {
					filtered = append(filtered, m)
				}
			}
			models = filtered
		}

		renderModelsLocal(models)
	},
}

func renderModelsRPC(models []*v1.ModelInfo) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "\nID\tALIAS\tPROVIDER\tREMOTE MODEL\tPRIORITY\tACTIVE")
	fmt.Fprintln(w, "──\t─────\t────────\t────────────\t────────\t──────")

	for _, m := range models {
		activeStr := "🟢"
		if !m.IsActive {
			activeStr = "⚪"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
			m.Id, m.Alias, m.ProviderName, m.Model, m.Priority, activeStr)
	}
	w.Flush()
}

func renderModelsLocal(models []types.ModelConfig) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "\nID\tALIAS\tPROVIDER\tREMOTE MODEL\tPRIORITY\tACTIVE")
	fmt.Fprintln(w, "──\t─────\t────────\t────────────\t────────\t──────")

	for _, m := range models {
		activeStr := "🟢"
		if !m.IsActive {
			activeStr = "⚪"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
			m.ID, m.Alias, m.ProviderID, m.Model, m.Priority, activeStr)
	}
	w.Flush()
}

var addModelCmd = &cobra.Command{
	Use:   "add",
	Short: "Add (Activate) a model from a configured provider",
	Run: func(cmd *cobra.Command, args []string) {
		manager := getModelManager()

		if modelProviderReg == "" || modelIDRemote == "" {
			fmt.Fprintln(os.Stderr, "❌ Missing mandatory flags: --provider and --model are required.")
			os.Exit(1)
		}

		id, err := manager.AddModel(context.Background(), modelProviderReg, modelIDRemote, modelPriorityVal, modelAliasVal)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to add model: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("✅ Model '%s' added successfully with ID: %s (Priority: %d)\n", modelIDRemote, id, modelPriorityVal)
	},
}

var activateModelCmd = &cobra.Command{
	Use:   "activate [ID]",
	Short: "Mark a model as primary for CLI interactions",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		p := filepath.Join(appConfig.AppDir, "active_model")
		if err := os.WriteFile(p, []byte(id), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error locking model: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Model '%s' is now the primary CLI model.\n", id)
	},
}

var updateModelCmd = &cobra.Command{
	Use:   "update [ID]",
	Short: "Update an existing model configuration",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		manager := getModelManager()
		id := args[0]

		var prio *int
		if cmd.Flags().Changed("priority") {
			prio = &modelUpdatePrio
		}

		var active *bool
		if cmd.Flags().Changed("active") {
			val := true
			if modelUpdateActive == "false" || modelUpdateActive == "0" {
				val = false
			}
			active = &val
		}

		var alias *string
		if cmd.Flags().Changed("alias") {
			alias = &modelAliasVal
		}

		var modelName *string
		if cmd.Flags().Changed("model") {
			modelName = &modelIDRemote
		}
		
		var useCasePriorities map[string]int
		if cmd.Flags().Changed("use-case-priorities") {
			useCasePriorities = make(map[string]int)
			if err := json.Unmarshal([]byte(modelUseCasePrios), &useCasePriorities); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Invalid JSON format for --use-case-priorities: %v\n", err)
				os.Exit(1)
			}
		}

		if err := manager.UpdateModel(id, prio, active, alias, modelName, useCasePriorities); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error updating model: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("✅ Model '%s' updated successfully.\n", id)
	},
}

var deleteModelCmd = &cobra.Command{
	Use:   "remove [ID]",
	Short: "Unregister a model from Vraxter",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		manager := getModelManager()
		if err := manager.DeleteModel(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Model '%s' removed.\n", args[0])
	},
}

func init() {
	addModelCmd.Flags().StringVarP(&modelProviderReg, "provider", "p", "", "Provider ID or Name (MANDATORY)")
	addModelCmd.Flags().StringVarP(&modelIDRemote, "model", "m", "", "Remote Model Name, e.g. gpt-4o (MANDATORY)")
	addModelCmd.Flags().IntVar(&modelPriorityVal, "priority", 5, "Selection priority (1-10, lower is higher)")
	addModelCmd.Flags().StringVar(&modelAliasVal, "alias", "", "Friendly alias (optional)")

	addModelCmd.MarkFlagRequired("provider")
	addModelCmd.MarkFlagRequired("model")

	listModelsCmd.Flags().StringVar(&modelFilterProv, "provider", "", "Filter models by provider ID or Name")

	updateModelCmd.Flags().IntVar(&modelUpdatePrio, "priority", 5, "New selection priority")
	updateModelCmd.Flags().StringVar(&modelUpdateActive, "active", "true", "Set model active status (true/false)")
	updateModelCmd.Flags().StringVar(&modelAliasVal, "alias", "", "New friendly alias")
	updateModelCmd.Flags().StringVarP(&modelIDRemote, "model", "m", "", "New remote model name (technical ID)")
	updateModelCmd.Flags().StringVar(&modelUseCasePrios, "use-case-priorities", "", "JSON map of use case priorities (e.g. '{\"coding\":1}')")

	modelsCmd.AddCommand(listModelsCmd)
	modelsCmd.AddCommand(addModelCmd)
	modelsCmd.AddCommand(updateModelCmd)
	modelsCmd.AddCommand(deleteModelCmd)
	modelsCmd.AddCommand(activateModelCmd)
	rootCmd.AddCommand(modelsCmd)
}

func getModelManager() *services.ModelManager {
	bootstrap()
	mRepo := db.NewModelRepository(appStore, appCrypto)
	pRepo := db.NewProviderRepository(appStore, appCrypto)
	return services.NewModelManager(mRepo, pRepo)
}
