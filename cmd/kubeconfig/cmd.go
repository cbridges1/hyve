package kubeconfig

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/kubeconfig"
)

// Cmd is the root kubeconfig command exposed to the parent.
var Cmd = kubeconfigCmd

var kubeconfigCmd = &cobra.Command{
	Use:   "kubeconfig",
	Short: "Manage kubeconfigs for clusters",
	Long: `Commands to retrieve and merge stored kubeconfigs.

Kubeconfigs are now populated by each cluster module's auth operation.
Use 'hyve cluster auth <name>' to fetch and configure access for a cluster.`,
}

var kubeconfigGetCmd = &cobra.Command{
	Use:   "get [cluster-name]",
	Short: "Get kubeconfig for a specific cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]
		getKubeconfig(cmd, clusterName)
	},
}

var kubeconfigUseCmd = &cobra.Command{
	Use:   "use [cluster-name]",
	Short: "Merge cluster into ~/.kube/config and set as active context",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		UseKubeconfig(args[0])
	},
}

var kubeconfigMergeCmd = &cobra.Command{
	Use:   "merge [cluster-name]",
	Short: "Merge cluster context into local ~/.kube/config",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		mergeKubeconfig(args[0])
	},
}

var kubeconfigRemoveCmd = &cobra.Command{
	Use:   "remove [cluster-name]",
	Short: "Remove cluster context from local ~/.kube/config",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		removeFromKubeConfig(args[0])
	},
}

var kubeconfigMigrateCmd = &cobra.Command{
	Use:   "migrate [old-hostname]",
	Short: "Migrate kubeconfig encryption to new portable format",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return migrateKubeconfigEncryption(args[0])
	},
}

func init() {
	kubeconfigGetCmd.Flags().BoolP("save", "s", false, "Save kubeconfig to ~/.kube/config-<cluster-name>")
	kubeconfigGetCmd.Flags().BoolP("merge", "m", false, "Merge kubeconfig into ~/.kube/config")
	kubeconfigGetCmd.Flags().StringP("output", "o", "", "Output file path for kubeconfig")

	kubeconfigCmd.AddCommand(kubeconfigGetCmd)
	kubeconfigCmd.AddCommand(kubeconfigUseCmd)
	kubeconfigCmd.AddCommand(kubeconfigMergeCmd)
	kubeconfigCmd.AddCommand(kubeconfigRemoveCmd)
	kubeconfigCmd.AddCommand(kubeconfigMigrateCmd)
}

func createKubeconfigManager() (*kubeconfig.Manager, string, error) {
	repoPath := shared.GetRepoPath()
	if repoPath == "" {
		return nil, "", fmt.Errorf("no Git repository configured. Use 'hyve git add' to configure a repository")
	}
	repoName := filepath.Base(repoPath)
	mgr, err := kubeconfig.NewManager(repoName)
	if err != nil {
		return nil, "", err
	}
	return mgr, repoName, nil
}

func getKubeconfig(cmd *cobra.Command, clusterName string) {
	mgr, kc, err := resolveKubeconfig(clusterName)
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer mgr.Close()

	cfg, err := kc.GetConfig()
	if err != nil {
		log.Fatalf("Failed to decrypt kubeconfig: %v", err)
	}

	saveFlag, _ := cmd.Flags().GetBool("save")
	mergeFlag, _ := cmd.Flags().GetBool("merge")
	outputPath, _ := cmd.Flags().GetString("output")

	if outputPath != "" {
		if err := os.WriteFile(outputPath, []byte(cfg), 0600); err != nil {
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
		if err := os.WriteFile(outPath, []byte(cfg), 0600); err != nil {
			log.Fatalf("Failed to write kubeconfig to %s: %v", outPath, err)
		}
		log.Printf("✅ Kubeconfig saved to %s", outPath)
		log.Printf("💡 To use: export KUBECONFIG=%s", outPath)
	} else if mergeFlag {
		mergeKubeconfig(clusterName)
	} else {
		fmt.Print(cfg)
	}
}

// UseKubeconfig merges the cluster's kubeconfig into ~/.kube/config and sets it as active context.
func UseKubeconfig(clusterName string) {
	mergeKubeconfig(clusterName)
	useCtxCmd := exec.Command("kubectl", "config", "use-context", clusterName)
	useCtxCmd.Stdout = os.Stdout
	useCtxCmd.Stderr = os.Stderr
	if err := useCtxCmd.Run(); err != nil {
		log.Printf("⚠️  Failed to set context: %v", err)
		log.Printf("   Run manually: kubectl config use-context %s", clusterName)
	} else {
		log.Printf("✅ Active context set to '%s'", clusterName)
	}
}

func mergeKubeconfig(clusterName string) {
	mgr, kc, err := resolveKubeconfig(clusterName)
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer mgr.Close()

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
}

func removeFromKubeConfig(clusterName string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get user home directory: %v", err)
	}
	kubeConfigPath := filepath.Join(homeDir, ".kube", "config")
	if _, err := os.Stat(kubeConfigPath); os.IsNotExist(err) {
		log.Printf("❌ No kubeconfig found at %s", kubeConfigPath)
		return
	}
	existingData, err := os.ReadFile(kubeConfigPath)
	if err != nil {
		log.Fatalf("Failed to read kubeconfig: %v", err)
	}
	if err := kubeconfig.RemoveKubeconfigContext(string(existingData), clusterName, kubeConfigPath); err != nil {
		log.Fatalf("Failed to remove context: %v", err)
	}
	log.Printf("✅ Removed cluster '%s' from %s", clusterName, kubeConfigPath)
}

func resolveKubeconfig(clusterName string) (*kubeconfig.Manager, *kubeconfig.Kubeconfig, error) {
	mgr, _, err := createKubeconfigManager()
	if err != nil {
		return nil, nil, fmt.Errorf("kubeconfig not found for cluster '%s': %w", clusterName, err)
	}
	kc, err := mgr.GetKubeconfig(clusterName)
	if err != nil || kc == nil {
		mgr.Close()
		return nil, nil, fmt.Errorf("kubeconfig not found for cluster '%s': run 'hyve cluster auth %s' first", clusterName, clusterName)
	}
	return mgr, kc, nil
}

func migrateKubeconfigEncryption(oldHostname string) error {
	kubeconfigMgr, repoName, err := createKubeconfigManager()
	if err != nil {
		return err
	}

	log.Printf("🔄 Starting migration for repository: %s", repoName)
	log.Printf("🔑 Old hostname: %s", oldHostname)

	if err := kubeconfigMgr.MigrateEncryption(oldHostname); err != nil {
		log.Printf("❌ Migration failed: %v", err)
		return err
	}

	log.Println("✅ Migration completed successfully!")
	return nil
}
