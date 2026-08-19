package cluster

import (
	gocontext "context"
	"fmt"
	"log"
	"os"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/types"
)

// showCluster, listClusters, and markClusterForDeletion each start with the
// same dispatch: a valid local Session (see shared.UseClusterMode) means
// cluster mode — talk to the API — otherwise fall through to today's
// local-file behavior, unchanged. Session presence deliberately wins with
// no separate flag; `hyve logout` cleanly reverts to local mode.
func showCluster(clusterName string) {
	if sess, ok := shared.UseClusterMode(); ok {
		showClusterAPI(shared.NewAPIClient(sess), clusterName)
		return
	}

	ctx := gocontext.Background()
	stateMgr, _ := shared.CreateStateManager(ctx)

	clusterDef, data, err := stateMgr.LoadClusterDefinition(clusterName)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatalf("Cluster '%s' not found. Use 'hyve cluster list' to see available clusters.", clusterName)
		}
		log.Fatalf("Failed to read cluster file: %v", err)
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
	if sess, ok := shared.UseClusterMode(); ok {
		listClustersAPI(shared.NewAPIClient(sess))
		return
	}

	ctx := gocontext.Background()
	stateMgr, _ := shared.CreateStateManager(ctx)

	defs, err := stateMgr.LoadClusterDefinitions()
	if err != nil {
		log.Fatalf("Failed to load cluster definitions: %v", err)
	}

	var clusters []types.ClusterDefinition
	for _, clusterDef := range defs {
		if clusterDef.Kind == "ClusterDefinition" && !clusterDef.Spec.Delete {
			clusters = append(clusters, clusterDef)
		}
	}

	if len(clusters) == 0 {
		log.Println("❌ No clusters found")
		log.Println("\n💡 Run 'hyve cluster create <cluster> --template <template>' to create a cluster")
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
	if sess, ok := shared.UseClusterMode(); ok {
		deleteClusterAPI(shared.NewAPIClient(sess), clusterName)
		return
	}

	ctx := gocontext.Background()
	stateMgr, _ := shared.CreateStateManager(ctx)

	clusterDef, _, err := stateMgr.LoadClusterDefinition(clusterName)
	if err != nil {
		log.Fatalf("Failed to read cluster file: %v", err)
	}
	clusterDef.Spec.Delete = true
	if err := stateMgr.SaveClusterDefinition(clusterDef); err != nil {
		log.Fatalf("Failed to write cluster definition: %v", err)
	}

	shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Mark cluster %s for deletion", clusterName))
	log.Printf("📝 Cluster '%s' marked for deletion", clusterName)
	log.Printf("   Reconciler will run onDelete workflows, delete via the module, and remove the YAML.")
	shared.RunReconciliation("", false)
}
