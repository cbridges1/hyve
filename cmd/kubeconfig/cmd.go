package kubeconfig

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"hyve/cmd/shared"
	"hyve/internal/kubeconfig"
	"hyve/internal/provider"
)

// Cmd is the root kubeconfig command exposed to the parent.
var Cmd = kubeconfigCmd

var kubeconfigCmd = &cobra.Command{
	Use:   "kubeconfig",
	Short: "Manage kubeconfigs for clusters",
	Long:  "Commands to sync, retrieve, and manage kubeconfigs for clusters in the current repository",
}

var kubeconfigSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync kubeconfigs from all active clusters",
	Long:  "Retrieve and store kubeconfigs from all active clusters in the current repository",
	Run: func(cmd *cobra.Command, args []string) {
		syncKubeconfigs()
	},
}

var kubeconfigGetCmd = &cobra.Command{
	Use:   "get [cluster-name]",
	Short: "Get kubeconfig for a specific cluster",
	Long:  "Retrieve and display the kubeconfig for a specific cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]
		getKubeconfig(cmd, clusterName)
	},
}

var kubeconfigUseCmd = &cobra.Command{
	Use:   "use [cluster-name]",
	Short: "Merge cluster into ~/.kube/config and set as active context",
	Long:  "Merge the cluster's kubeconfig into ~/.kube/config and set it as the active kubectl context",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]
		UseKubeconfig(clusterName)
	},
}

var kubeconfigMergeCmd = &cobra.Command{
	Use:   "merge [cluster-name]",
	Short: "Merge cluster context into local ~/.kube/config",
	Long:  "Merge the cluster's kubeconfig context into your local ~/.kube/config file for easy kubectl access",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]
		mergeKubeconfig(clusterName)
	},
}

var kubeconfigRemoveCmd = &cobra.Command{
	Use:   "remove [cluster-name]",
	Short: "Remove cluster context from local ~/.kube/config",
	Long:  "Remove the cluster's context, cluster, and user entries from your local ~/.kube/config file",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]
		shared.RemoveKubeconfig(clusterName)
	},
}

var kubeconfigMigrateCmd = &cobra.Command{
	Use:   "migrate [old-hostname]",
	Short: "Migrate kubeconfig encryption to new portable format",
	Long: `Migrate kubeconfig encryption from hostname-based keys to portable keys.

This command re-encrypts all kubeconfigs using a key that doesn't include the hostname,
making the database portable across machines. You need to provide the hostname that was
used when the kubeconfigs were originally encrypted.

Example:
  hyve kubeconfig migrate "old-macbook.local"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		oldHostname := args[0]
		return migrateKubeconfigEncryption(oldHostname)
	},
}

func init() {
	kubeconfigGetCmd.Flags().BoolP("save", "s", false, "Save kubeconfig to ~/.kube/config-<cluster-name>")
	kubeconfigGetCmd.Flags().BoolP("merge", "m", false, "Merge kubeconfig into ~/.kube/config")
	kubeconfigGetCmd.Flags().StringP("output", "o", "", "Output file path for kubeconfig")

	kubeconfigCmd.AddCommand(kubeconfigSyncCmd)
	kubeconfigCmd.AddCommand(kubeconfigGetCmd)
	kubeconfigCmd.AddCommand(kubeconfigUseCmd)
	kubeconfigCmd.AddCommand(kubeconfigMergeCmd)
	kubeconfigCmd.AddCommand(kubeconfigRemoveCmd)
	kubeconfigCmd.AddCommand(kubeconfigMigrateCmd)
}

func syncKubeconfigs() {
	ctx := context.Background()

	kubeconfigMgr, repoName, err := shared.CreateKubeconfigManager()
	if err != nil {
		log.Fatalf("Failed to create kubeconfig manager: %v", err)
	}
	defer kubeconfigMgr.Close()

	stateMgr, _ := shared.CreateStateManager(ctx)
	clusterDefs, err := stateMgr.LoadClusterDefinitions()
	if err != nil {
		log.Fatalf("Failed to load cluster definitions: %v", err)
	}

	log.Printf("📁 Syncing kubeconfigs for repository '%s'", repoName)

	providerFactory := provider.NewFactory()
	successCount := 0

	activeClusterNames := make([]string, 0, len(clusterDefs))
	for _, cd := range clusterDefs {
		activeClusterNames = append(activeClusterNames, cd.Metadata.Name)
	}

	for _, clusterDef := range clusterDefs {
		prov, err := shared.CreateProviderForCluster(providerFactory, clusterDef)
		if err != nil {
			log.Printf("Failed to create provider for cluster %s: %v", clusterDef.Metadata.Name, err)
			continue
		}

		syncer := kubeconfig.NewSyncer(kubeconfigMgr, prov)
		if err := syncer.SyncSingleKubeconfig(ctx, clusterDef.Metadata.Name); err != nil {
			log.Printf("Failed to sync kubeconfig for cluster %s: %v", clusterDef.Metadata.Name, err)
			continue
		}
		successCount++
	}

	if err := kubeconfigMgr.CleanupOrphanedKubeconfigs(activeClusterNames); err != nil {
		log.Printf("Failed to cleanup orphaned kubeconfigs: %v", err)
	}

	log.Printf("✅ Kubeconfig sync completed: %d/%d clusters synced successfully", successCount, len(clusterDefs))
}

func getKubeconfig(cmd *cobra.Command, clusterName string) {
	kubeconfigMgr, _, err := shared.CreateKubeconfigManager()
	if err != nil {
		log.Fatalf("Failed to create kubeconfig manager: %v", err)
	}
	defer kubeconfigMgr.Close()

	kc, err := kubeconfigMgr.GetKubeconfig(clusterName)
	if err != nil {
		log.Fatalf("Failed to get kubeconfig: %v", err)
	}

	if kc == nil {
		log.Fatalf("Kubeconfig not found for cluster %s. Run 'hyve kubeconfig sync' first.", clusterName)
	}

	cfg, err := kc.GetConfig()
	if err != nil {
		log.Fatalf("Failed to decrypt kubeconfig: %v", err)
	}

	saveFlag, _ := cmd.Flags().GetBool("save")
	mergeFlag, _ := cmd.Flags().GetBool("merge")
	outputPath, _ := cmd.Flags().GetString("output")

	if outputPath != "" {
		err := os.WriteFile(outputPath, []byte(cfg), 0600)
		if err != nil {
			log.Fatalf("Failed to write kubeconfig to %s: %v", outputPath, err)
		}
		log.Printf("✅ Kubeconfig saved to %s", outputPath)
	} else if saveFlag {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Failed to get user home directory: %v", err)
		}
		kubeDir := fmt.Sprintf("%s/.kube", homeDir)
		if err := os.MkdirAll(kubeDir, 0755); err != nil {
			log.Fatalf("Failed to create .kube directory: %v", err)
		}

		outPath := fmt.Sprintf("%s/config-%s", kubeDir, clusterName)
		err = os.WriteFile(outPath, []byte(cfg), 0600)
		if err != nil {
			log.Fatalf("Failed to write kubeconfig to %s: %v", outPath, err)
		}
		log.Printf("✅ Kubeconfig saved to %s", outPath)
		log.Printf("💡 To use: export KUBECONFIG=%s", outPath)
	} else if mergeFlag {
		log.Println("⚠️  Merge functionality not yet implemented")
		log.Println("💡 Use --save flag to save to a separate file, or redirect output:")
		log.Printf("   hyve kubeconfig get %s > ~/.kube/config-%s", clusterName, clusterName)
		fmt.Print(cfg)
	} else {
		fmt.Print(cfg)
	}
}

// UseKubeconfig merges the cluster's kubeconfig into ~/.kube/config and sets it as active context.
// Exported so the root `use` command can call it.
func UseKubeconfig(clusterName string) {
	ctx := context.Background()

	kubeconfigMgr, _, err := shared.CreateKubeconfigManager()
	if err != nil {
		log.Fatalf("Failed to create kubeconfig manager: %v", err)
	}
	defer kubeconfigMgr.Close()

	// Attempt a fresh sync so the stored kubeconfig is up-to-date (e.g. renewed
	// tokens for GCP). If this fails for any reason we fall through and use
	// whatever is already in the database.
	stateMgr, _ := shared.CreateStateManager(ctx)
	if stateMgr != nil {
		clusterDefs, err := stateMgr.LoadClusterDefinitions()
		if err == nil {
			providerFactory := provider.NewFactory()
			for _, cd := range clusterDefs {
				if cd.Metadata.Name != clusterName {
					continue
				}
				prov, err := shared.CreateProviderForCluster(providerFactory, cd)
				if err != nil {
					log.Printf("⚠️  Could not create provider for sync: %v", err)
					break
				}
				syncer := kubeconfig.NewSyncer(kubeconfigMgr, prov)
				if err := syncer.SyncSingleKubeconfig(ctx, clusterName); err != nil {
					log.Printf("⚠️  Kubeconfig sync failed, using cached credentials: %v", err)
				}
				break
			}
		}
	}

	kc, err := kubeconfigMgr.GetKubeconfig(clusterName)
	if err != nil {
		log.Fatalf("Failed to get kubeconfig: %v", err)
	}

	if kc == nil {
		log.Fatalf("Kubeconfig not found for cluster %s. Run 'hyve kubeconfig sync' first.", clusterName)
	}

	cfg, err := kc.GetConfig()
	if err != nil {
		log.Fatalf("Failed to decrypt kubeconfig: %v", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get user home directory: %v", err)
	}

	kubeDir := fmt.Sprintf("%s/.kube", homeDir)
	if err := os.MkdirAll(kubeDir, 0755); err != nil {
		log.Fatalf("Failed to create .kube directory: %v", err)
	}

	kubeConfigPath := fmt.Sprintf("%s/config", kubeDir)

	log.Printf("🔀 Merging cluster '%s' into %s", clusterName, kubeConfigPath)

	existingConfig := ""
	if existingData, err := os.ReadFile(kubeConfigPath); err == nil {
		existingConfig = string(existingData)
	}

	if existingConfig == "" {
		if err := os.WriteFile(kubeConfigPath, []byte(cfg), 0600); err != nil {
			log.Fatalf("Failed to write kubeconfig: %v", err)
		}
	} else {
		backupPath := fmt.Sprintf("%s.backup", kubeConfigPath)
		if err := os.WriteFile(backupPath, []byte(existingConfig), 0600); err != nil {
			log.Printf("⚠️  Warning: Failed to create backup at %s", backupPath)
		} else {
			log.Printf("📦 Backup created at %s", backupPath)
		}

		mergedContent, err := kubeconfig.MergeKubeconfigs(existingConfig, cfg)
		if err != nil {
			log.Fatalf("Failed to merge kubeconfigs: %v", err)
		}

		if err := os.WriteFile(kubeConfigPath, []byte(mergedContent), 0600); err != nil {
			log.Fatalf("Failed to write merged kubeconfig: %v", err)
		}
	}

	log.Printf("✅ Merged cluster '%s' into %s", clusterName, kubeConfigPath)

	useCtxCmd := exec.Command("kubectl", "config", "use-context", clusterName)
	useCtxCmd.Stdout = os.Stdout
	useCtxCmd.Stderr = os.Stderr
	if err := useCtxCmd.Run(); err != nil {
		log.Printf("⚠️  Failed to set context: %v", err)
		log.Printf("   Run manually: kubectl config use-context %s", clusterName)
	} else {
		log.Printf("✅ Active context set to '%s'", clusterName)
		log.Println()
		log.Println("💡 Test your connection:")
		log.Println("   kubectl get nodes")
	}
}

func mergeKubeconfig(clusterName string) {
	kubeconfigMgr, _, err := shared.CreateKubeconfigManager()
	if err != nil {
		log.Fatalf("Failed to create kubeconfig manager: %v", err)
	}
	defer kubeconfigMgr.Close()

	kc, err := kubeconfigMgr.GetKubeconfig(clusterName)
	if err != nil {
		log.Fatalf("Failed to get kubeconfig: %v", err)
	}

	if kc == nil {
		log.Fatalf("Kubeconfig not found for cluster %s. Run 'hyve kubeconfig sync' first.", clusterName)
	}

	cfg, err := kc.GetConfig()
	if err != nil {
		log.Fatalf("Failed to decrypt kubeconfig: %v", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get user home directory: %v", err)
	}

	kubeDir := fmt.Sprintf("%s/.kube", homeDir)
	if err := os.MkdirAll(kubeDir, 0755); err != nil {
		log.Fatalf("Failed to create .kube directory: %v", err)
	}

	kubeConfigPath := fmt.Sprintf("%s/config", kubeDir)

	log.Printf("🔀 Merging cluster '%s' into %s", clusterName, kubeConfigPath)

	existingConfig := ""
	if existingData, err := os.ReadFile(kubeConfigPath); err == nil {
		existingConfig = string(existingData)
	}

	if existingConfig == "" {
		if err := os.WriteFile(kubeConfigPath, []byte(cfg), 0600); err != nil {
			log.Fatalf("Failed to write kubeconfig: %v", err)
		}
	} else {
		backupPath := fmt.Sprintf("%s.backup", kubeConfigPath)
		if err := os.WriteFile(backupPath, []byte(existingConfig), 0600); err != nil {
			log.Printf("⚠️  Warning: Failed to create backup at %s", backupPath)
		} else {
			log.Printf("📦 Backup created at %s", backupPath)
		}

		mergedContent, err := kubeconfig.MergeKubeconfigs(existingConfig, cfg)
		if err != nil {
			log.Fatalf("Failed to merge kubeconfigs: %v", err)
		}

		if err := os.WriteFile(kubeConfigPath, []byte(mergedContent), 0600); err != nil {
			log.Fatalf("Failed to write merged kubeconfig: %v", err)
		}
	}

	log.Printf("✅ Successfully merged cluster '%s' into %s", clusterName, kubeConfigPath)
	log.Println()
	log.Println("💡 Next steps:")
	log.Printf("   kubectl config use-context %s", clusterName)
	log.Println("   kubectl get nodes")
}

func migrateKubeconfigEncryption(oldHostname string) error {
	kubeconfigMgr, repoName, err := shared.CreateKubeconfigManager()
	if err != nil {
		return err
	}

	log.Printf("🔄 Starting migration for repository: %s", repoName)
	log.Printf("🔑 Old hostname: %s", oldHostname)
	log.Println()

	if err := kubeconfigMgr.MigrateEncryption(oldHostname); err != nil {
		log.Printf("❌ Migration failed: %v", err)
		return err
	}

	log.Println("✅ Migration completed successfully!")
	log.Println()
	log.Println("📝 All kubeconfigs have been re-encrypted with the new portable key format.")
	log.Println("💡 Your kubeconfigs will now work across different machines without hostname dependencies.")
	log.Println()

	return nil
}
