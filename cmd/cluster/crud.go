package cluster

import (
	gocontext "context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/types"
)

func showCluster(clusterName string) {
	ctx := gocontext.Background()
	_, clustersDir := shared.CreateStateManager(ctx)
	filePath := filepath.Join(clustersDir, clusterName+".yaml")

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatalf("Cluster '%s' not found. Use 'hyve cluster list' to see available clusters.", clusterName)
		}
		log.Fatalf("Failed to read cluster file: %v", err)
	}

	var clusterDef types.ClusterDefinition
	if err := yaml.Unmarshal(data, &clusterDef); err != nil {
		log.Fatalf("Failed to parse cluster definition: %v", err)
	}

	fmt.Printf("---\n%s", string(data))
	fmt.Println()
	fmt.Printf("Summary:\n")
	fmt.Printf("  Name:   %s\n", clusterDef.Metadata.Name)
	fmt.Printf("  Region: %s\n", clusterDef.Metadata.Region)
	fmt.Printf("  Driver: %s@%s\n", clusterDef.Spec.Driver.Source, clusterDef.Spec.Driver.Version)
	if len(clusterDef.Spec.Params) > 0 {
		fmt.Println("  Params:")
		for k, v := range clusterDef.Spec.Params {
			fmt.Printf("    %s: %s\n", k, v)
		}
	}
	if clusterDef.Spec.Pause {
		fmt.Printf("  Pause:    true\n")
	}
	if clusterDef.Spec.ExpiresAt != "" {
		fmt.Printf("  ExpiresAt: %s\n", clusterDef.Spec.ExpiresAt)
	}
	if len(clusterDef.Spec.Workflows.BeforeCreate) > 0 {
		fmt.Printf("  BeforeCreate: %v\n", clusterDef.Spec.Workflows.BeforeCreate)
	}
	if len(clusterDef.Spec.Workflows.OnCreate) > 0 {
		fmt.Printf("  OnCreate: %v\n", clusterDef.Spec.Workflows.OnCreate)
	}
	if len(clusterDef.Spec.Workflows.OnDelete) > 0 {
		fmt.Printf("  OnDelete: %v\n", clusterDef.Spec.Workflows.OnDelete)
	}
	if len(clusterDef.Spec.Workflows.AfterDelete) > 0 {
		fmt.Printf("  AfterDelete: %v\n", clusterDef.Spec.Workflows.AfterDelete)
	}
	if len(clusterDef.Spec.PendingWorkflows) > 0 {
		fmt.Printf("  PendingWorkflows: %d queued\n", len(clusterDef.Spec.PendingWorkflows))
	}
}

func listClusters() {
	ctx := gocontext.Background()
	_, clustersDir := shared.CreateStateManager(ctx)

	if _, err := os.Stat(clustersDir); os.IsNotExist(err) {
		log.Println("❌ No clusters found")
		log.Println("\n💡 Run 'hyve template execute <template> <cluster>' to create a cluster")
		return
	}

	entries, err := os.ReadDir(clustersDir)
	if err != nil {
		log.Fatalf("Failed to read clusters directory: %v", err)
	}

	var clusters []types.ClusterDefinition
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		filePath := filepath.Join(clustersDir, name)
		data, err := os.ReadFile(filePath)
		if err != nil {
			log.Printf("Warning: Failed to read %s: %v", name, err)
			continue
		}
		var clusterDef types.ClusterDefinition
		if err := yaml.Unmarshal(data, &clusterDef); err != nil {
			log.Printf("Warning: Failed to parse %s: %v", name, err)
			continue
		}
		if clusterDef.Kind == "Cluster" && !clusterDef.Spec.Delete {
			clusters = append(clusters, clusterDef)
		}
	}

	if len(clusters) == 0 {
		log.Println("❌ No clusters found")
		log.Println("\n💡 Run 'hyve template execute <template> <cluster>' to create a cluster")
		return
	}

	log.Printf("📦 Clusters (%d):\n", len(clusters))

	for _, cluster := range clusters {
		nameLabel := cluster.Metadata.Name
		if cluster.Spec.Pause {
			nameLabel += " [paused]"
		}
		log.Printf("  %s", nameLabel)
		log.Printf("    Driver: %s@%s", cluster.Spec.Driver.Source, cluster.Spec.Driver.Version)
		log.Printf("    Region: %s", cluster.Metadata.Region)
		if cluster.Spec.ExpiresAt != "" {
			log.Printf("    Expires At: %s", cluster.Spec.ExpiresAt)
		}
		log.Println()
	}

	log.Println("💡 Commands:")
	log.Println("  hyve cluster show <name>      # Show cluster definition")
	log.Println("  hyve cluster delete <name>    # Mark cluster for deletion")
	log.Println("  hyve cluster auth <name>      # Configure kubeconfig")
	log.Println("  hyve reconcile                # Apply changes")
}

// markClusterForDeletion sets the cluster's spec.delete flag and commits the
// change. The reconciler picks this up on its next run.
func markClusterForDeletion(clusterName string) {
	ctx := gocontext.Background()
	stateMgr, clustersDir := shared.CreateStateManager(ctx)
	filePath := filepath.Join(clustersDir, clusterName+".yaml")

	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Failed to read cluster file: %v", err)
	}
	var clusterDef types.ClusterDefinition
	if err := yaml.Unmarshal(data, &clusterDef); err != nil {
		log.Fatalf("Failed to parse cluster definition: %v", err)
	}
	clusterDef.Spec.Delete = true
	updated, err := yaml.Marshal(&clusterDef)
	if err != nil {
		log.Fatalf("Failed to marshal cluster definition: %v", err)
	}
	if err := os.WriteFile(filePath, updated, 0644); err != nil {
		log.Fatalf("Failed to write cluster definition: %v", err)
	}

	shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Mark cluster %s for deletion", clusterName))
	log.Printf("📝 Cluster '%s' marked for deletion", clusterName)
	log.Printf("   Reconciler will run onDelete workflows, delete via the module, and remove the YAML.")
	shared.RunReconciliation("", false)
}
