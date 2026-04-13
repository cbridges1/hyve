package kubeconfig

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/kubeconfig"
	"github.com/cbridges1/hyve/internal/provider"
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

var kubeconfigAddExternalCmd = &cobra.Command{
	Use:   "add-external [cluster-name]",
	Short: "Import an external cluster's kubeconfig into Hyve",
	Long: `Import a kubeconfig for a cluster that was not created by Hyve.

The kubeconfig is stored encrypted in the local Hyve database so that
'hyve use' and workflow execution work the same as for Hyve-managed clusters.
No Git repository needs to be configured.

Read from a file:
  hyve kubeconfig add-external my-cluster --file ~/.kube/my-cluster.yaml

Read from stdin:
  cat ~/.kube/my-cluster.yaml | hyve kubeconfig add-external my-cluster`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName := args[0]
		filePath, _ := cmd.Flags().GetString("file")
		return addExternalKubeconfig(clusterName, filePath)
	},
}

var kubeconfigListExternalCmd = &cobra.Command{
	Use:   "list-external",
	Short: "List all locally-imported external cluster kubeconfigs",
	Run: func(cmd *cobra.Command, args []string) {
		listExternalKubeconfigs()
	},
}

var kubeconfigRemoveExternalCmd = &cobra.Command{
	Use:   "remove-external [cluster-name]",
	Short: "Remove a locally-imported external cluster kubeconfig",
	Long: `Remove the kubeconfig for an external cluster from Hyve's local store.

This does not remove it from ~/.kube/config. Use 'hyve kubeconfig remove' for that.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]
		removeExternalKubeconfig(clusterName)
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

	kubeconfigAddExternalCmd.Flags().StringP("file", "f", "", "Path to kubeconfig file (reads from stdin if omitted)")

	kubeconfigCmd.AddCommand(kubeconfigSyncCmd)
	kubeconfigCmd.AddCommand(kubeconfigGetCmd)
	kubeconfigCmd.AddCommand(kubeconfigUseCmd)
	kubeconfigCmd.AddCommand(kubeconfigMergeCmd)
	kubeconfigCmd.AddCommand(kubeconfigRemoveCmd)
	kubeconfigCmd.AddCommand(kubeconfigMigrateCmd)
	kubeconfigCmd.AddCommand(kubeconfigAddExternalCmd)
	kubeconfigCmd.AddCommand(kubeconfigListExternalCmd)
	kubeconfigCmd.AddCommand(kubeconfigRemoveExternalCmd)
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
	kubeconfigMgr, kc, err := resolveKubeconfig(clusterName)
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer kubeconfigMgr.Close()

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
		kubeDir := filepath.Join(homeDir, ".kube")
		if err := os.MkdirAll(kubeDir, 0755); err != nil {
			log.Fatalf("Failed to create .kube directory: %v", err)
		}

		outPath := filepath.Join(kubeDir, "config-"+clusterName)
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

	// Attempt a fresh sync for Hyve-managed clusters. External (local-only) clusters
	// skip this step — their kubeconfig was imported via 'add-external' and is used as-is.
	if syncMgr, _, err := shared.CreateKubeconfigManager(); err == nil {
		defer syncMgr.Close()

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

					// For AWS clusters: grant the local IAM identity access to the cluster's
					// Kubernetes API via EKS access entries so that the kubeconfig generated
					// with local credentials works without needing to be the cluster creator.
					if granter, ok := prov.(provider.AccessEntryGranter); ok {
						if localARN := localAWSCallerARN(); localARN != "" {
							log.Printf("🔑 Granting local identity %s access to cluster %s...", localARN, clusterName)
							if err := granter.EnsureAccessEntry(ctx, clusterName, localARN); err != nil {
								log.Printf("⚠️  Could not grant EKS access entry (cluster may use aws-auth ConfigMap instead): %v", err)
							} else {
								log.Printf("✅ Local identity granted cluster admin access")
							}
						}
					}

					syncer := kubeconfig.NewSyncer(syncMgr, prov)
					if err := syncer.SyncSingleKubeconfig(ctx, clusterName); err != nil {
						log.Printf("⚠️  Kubeconfig sync failed, using cached credentials: %v", err)
					}
					break
				}
			}
		}
	}

	// Resolve from repo store or local external store.
	kubeconfigMgr, kc, err := resolveKubeconfig(clusterName)
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer kubeconfigMgr.Close()

	cfg, err := kc.GetConfig()
	if err != nil {
		log.Fatalf("Failed to decrypt kubeconfig: %v", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get user home directory: %v", err)
	}

	kubeDir := filepath.Join(homeDir, ".kube")
	if err := os.MkdirAll(kubeDir, 0755); err != nil {
		log.Fatalf("Failed to create .kube directory: %v", err)
	}

	kubeConfigPath := filepath.Join(kubeDir, "config")

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
		backupPath := kubeConfigPath + ".backup"
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
	kubeconfigMgr, kc, err := resolveKubeconfig(clusterName)
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer kubeconfigMgr.Close()

	cfg, err := kc.GetConfig()
	if err != nil {
		log.Fatalf("Failed to decrypt kubeconfig: %v", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get user home directory: %v", err)
	}

	kubeDir := filepath.Join(homeDir, ".kube")
	if err := os.MkdirAll(kubeDir, 0755); err != nil {
		log.Fatalf("Failed to create .kube directory: %v", err)
	}

	kubeConfigPath := filepath.Join(kubeDir, "config")

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
		backupPath := kubeConfigPath + ".backup"
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

// resolveKubeconfig looks up a stored kubeconfig for clusterName, checking the
// current repository's store first and then falling back to the local external
// store. The returned manager must be closed by the caller.
func resolveKubeconfig(clusterName string) (*kubeconfig.Manager, *kubeconfig.Kubeconfig, error) {
	// Try the repository-scoped store first.
	if mgr, _, err := shared.CreateKubeconfigManager(); err == nil {
		if kc, err := mgr.GetKubeconfig(clusterName); err == nil && kc != nil {
			return mgr, kc, nil
		}
		mgr.Close()
	}

	// Fall back to the local external store.
	localMgr, err := shared.CreateLocalKubeconfigManager()
	if err != nil {
		return nil, nil, fmt.Errorf("kubeconfig not found for cluster '%s'. "+
			"Run 'hyve kubeconfig sync' for Hyve-managed clusters, or "+
			"'hyve kubeconfig add-external %s --file <path>' for external clusters", clusterName, clusterName)
	}
	kc, err := localMgr.GetKubeconfig(clusterName)
	if err != nil || kc == nil {
		localMgr.Close()
		return nil, nil, fmt.Errorf("kubeconfig not found for cluster '%s'. "+
			"Run 'hyve kubeconfig sync' for Hyve-managed clusters, or "+
			"'hyve kubeconfig add-external %s --file <path>' for external clusters", clusterName, clusterName)
	}
	return localMgr, kc, nil
}

// ── External kubeconfig management ───────────────────────────────────────────

func addExternalKubeconfig(clusterName, filePath string) error {
	var content []byte
	var err error

	if filePath != "" {
		content, err = os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read kubeconfig file: %w", err)
		}
	} else {
		// Check whether stdin has data (piped input).
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) != 0 {
			return fmt.Errorf("no kubeconfig source provided — use --file <path> or pipe kubeconfig via stdin")
		}
		content, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read kubeconfig from stdin: %w", err)
		}
	}

	if len(content) == 0 {
		return fmt.Errorf("kubeconfig is empty")
	}

	localMgr, err := shared.CreateLocalKubeconfigManager()
	if err != nil {
		return fmt.Errorf("failed to create local kubeconfig store: %w", err)
	}
	defer localMgr.Close()

	if _, err := localMgr.StoreKubeconfig(clusterName, string(content)); err != nil {
		return fmt.Errorf("failed to store kubeconfig: %w", err)
	}

	log.Printf("✅ External kubeconfig stored for cluster '%s'", clusterName)
	log.Printf("💡 Use it with: hyve use %s", clusterName)
	return nil
}

func listExternalKubeconfigs() {
	localMgr, err := shared.CreateLocalKubeconfigManager()
	if err != nil {
		log.Fatalf("Failed to open local kubeconfig store: %v", err)
	}
	defer localMgr.Close()

	kcs, err := localMgr.ListKubeconfigs()
	if err != nil {
		log.Fatalf("Failed to list external kubeconfigs: %v", err)
	}

	if len(kcs) == 0 {
		log.Println("No external kubeconfigs stored.")
		log.Println("💡 Import one with: hyve kubeconfig add-external <name> --file <path>")
		return
	}

	log.Printf("🖥️  External kubeconfigs (%d):\n", len(kcs))
	for _, kc := range kcs {
		log.Printf("  %s  (added: %s)", kc.ClusterName, kc.CreatedAt.Format("2006-01-02 15:04"))
	}
}

func removeExternalKubeconfig(clusterName string) {
	localMgr, err := shared.CreateLocalKubeconfigManager()
	if err != nil {
		log.Fatalf("Failed to open local kubeconfig store: %v", err)
	}
	defer localMgr.Close()

	if err := localMgr.DeleteKubeconfig(clusterName); err != nil {
		log.Fatalf("Failed to remove external kubeconfig: %v", err)
	}

	log.Printf("✅ Removed external kubeconfig for cluster '%s'", clusterName)
	log.Printf("💡 Context may still be present in ~/.kube/config — remove it with: hyve kubeconfig remove %s", clusterName)
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

// localAWSCallerARN returns the ARN of the currently configured AWS identity by running
// `aws sts get-caller-identity`. Returns an empty string if the AWS CLI is unavailable or
// no credentials are configured.
func localAWSCallerARN() string {
	cmd := exec.Command("aws", "sts", "get-caller-identity", "--query", "Arn", "--output", "text")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
