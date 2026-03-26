package reconcile

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/cbridges1/hyve/internal/cluster"
	"github.com/cbridges1/hyve/internal/kubeconfig"
	"github.com/cbridges1/hyve/internal/provider"
	"github.com/cbridges1/hyve/internal/providerconfig"
	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/types"
	"github.com/cbridges1/hyve/internal/workflow"
)

// Reconciler handles the reconciliation of clusters using provider abstraction
type Reconciler struct {
	providerFactory *provider.Factory
	stateMgr        *state.Manager
}

// NewReconciler creates a new reconciler
func NewReconciler(stateMgr *state.Manager) *Reconciler {
	return &Reconciler{
		providerFactory: provider.NewFactory(),
		stateMgr:        stateMgr,
	}
}

// ReconcileAll reconciles all clusters across all regions
func (r *Reconciler) ReconcileAll(ctx context.Context, clusterDefs []types.ClusterDefinition) error {
	// Load reconcile config to determine strictDelete setting.
	strictDelete := false
	log.Printf("Loading repo config from: %s", r.stateMgr.LocalPath())
	if repoCfg, err := r.stateMgr.LoadRepoConfig(); err == nil {
		strictDelete = repoCfg.Reconcile.StrictDelete
		log.Printf("Repo config loaded: mode=%s strictDelete=%v", repoCfg.Reconcile.Mode, strictDelete)
	} else {
		log.Printf("Warning: failed to load repo config: %v — defaulting strictDelete=false", err)
	}
	if strictDelete {
		log.Println("Strict-delete mode enabled: cloud clusters not present in YAML will be deleted")
	}

	if len(clusterDefs) == 0 && !strictDelete {
		log.Println("No cluster definitions found in state/clusters directory — skipping reconcile")
		return nil
	}

	// Convergence loop: process one cluster at a time, syncing repo state between each
	// reconciliation so concurrent pipeline runs are reflected before the next cluster is handled.
	finalDefs := r.convergenceLoop(ctx, clusterDefs)

	// Orphan cleanup using the final desired state after all reconciliations complete.
	r.cleanupOrphansByRegion(ctx, finalDefs)

	if strictDelete {
		if len(finalDefs) == 0 {
			log.Println("No cluster definitions found — strict-delete sweeping all cloud clusters...")
		} else {
			log.Println("strict-delete: sweeping all configured provider accounts for orphaned clusters...")
		}
		r.strictDeleteSweep(ctx)
	}

	if len(finalDefs) > 0 {
		log.Println("Exporting cluster information...")
		r.exportAllClusterInfo(ctx, finalDefs)

		log.Println("Syncing kubeconfigs...")
		if err := r.syncKubeconfigs(ctx, finalDefs); err != nil {
			log.Printf("Failed to sync kubeconfigs: %v", err)
		}
	}

	return nil
}

// convergenceLoop processes clusters one at a time, syncing the remote repo and reloading
// cluster definitions between each reconciliation. This ensures that state changes committed
// by concurrent pipeline runs are reflected before the next cluster is handled, avoiding
// redundant work and converging to the true desired state faster.
func (r *Reconciler) convergenceLoop(ctx context.Context, initialDefs []types.ClusterDefinition) []types.ClusterDefinition {
	processed := make(map[string]bool)
	currentDefs := initialDefs

	for {
		// Find the next cluster that has not yet been processed in this run.
		var next *types.ClusterDefinition
		for i := range currentDefs {
			if !processed[currentDefs[i].Metadata.Name] {
				next = &currentDefs[i]
				break
			}
		}
		if next == nil {
			break
		}

		clusterName := next.Metadata.Name
		processed[clusterName] = true

		log.Printf("Reconciling cluster: %s", clusterName)

		prov, err := r.createProviderForCluster(*next)
		if err != nil {
			log.Printf("Failed to create provider for cluster %s: %v", clusterName, err)
		} else {
			clusterMgr := cluster.NewManager(prov)
			reconcileErr := r.reconcileCluster(ctx, clusterMgr, *next)
			if reconcileErr != nil {
				log.Printf("Failed to reconcile cluster %s: %v", clusterName, reconcileErr)
			} else if next.Spec.Delete {
				// onDestroy workflows ran and the cloud cluster was deleted — now
				// remove the YAML marker and push so the repo reflects the final state.
				if removeErr := r.stateMgr.RemoveClusterFile(clusterName); removeErr != nil {
					log.Printf("Warning: failed to remove cluster file for %s: %v", clusterName, removeErr)
				} else {
					commitMsg := fmt.Sprintf("chore: remove cluster definition for %s after deletion", clusterName)
					if commitErr := r.stateMgr.CommitAndPush(ctx, commitMsg); commitErr != nil {
						log.Printf("Warning: failed to commit cluster file removal for %s: %v", clusterName, commitErr)
					} else {
						log.Printf("Removed cluster definition file for %s and pushed to remote", clusterName)
					}
				}
			}
		}

		// Pull latest remote changes so that clusters already reconciled by a
		// concurrent pipeline run are visible in the next iteration.
		if err := r.stateMgr.SyncWithRemote(ctx); err != nil {
			log.Printf("Warning: failed to sync with remote after reconciling %s: %v", clusterName, err)
		}

		reloaded, err := r.stateMgr.LoadClusterDefinitions()
		if err != nil {
			log.Printf("Warning: failed to reload cluster definitions after reconciling %s: %v", clusterName, err)
		} else {
			currentDefs = reloaded
		}
	}

	return currentDefs
}

// cleanupOrphansByRegion removes managed-prefix clusters that are no longer in the desired
// state, grouped by region so each provider scope is cleaned up independently.
func (r *Reconciler) cleanupOrphansByRegion(ctx context.Context, clusterDefs []types.ClusterDefinition) {
	regionClusters := make(map[string][]types.ClusterDefinition)
	for _, cd := range clusterDefs {
		regionClusters[cd.Metadata.Region] = append(regionClusters[cd.Metadata.Region], cd)
	}

	for region, clusters := range regionClusters {
		prov, err := r.createProviderForCluster(clusters[0])
		if err != nil {
			log.Printf("Failed to create provider for cleanup in region %s: %v", region, err)
			continue
		}
		clusterMgr := cluster.NewManager(prov)
		if err := r.cleanupOrphanedResources(ctx, clusterMgr, clusters); err != nil {
			log.Printf("Failed to cleanup orphaned resources in region %s: %v", region, err)
		}
	}
}

// createProviderForCluster creates a provider with credentials resolved from the provider
// config YAML files (provider-configs/*.yaml). Values may be literal strings or
// ${ENV_VAR} references that are expanded at resolution time.
func (r *Reconciler) createProviderForCluster(clusterDef types.ClusterDefinition) (provider.Provider, error) {
	providerName := clusterDef.Spec.Provider
	if providerName == "" {
		providerName = "civo" // default
	}

	opts := provider.ProviderOptions{
		Region:      clusterDef.Metadata.Region,
		AccountName: clusterDef.Spec.CivoOrganization, // overridden below per-provider
	}

	pcMgr := providerconfig.NewManager(r.stateMgr.GetStateRoot())

	switch strings.ToLower(providerName) {
	case "civo":
		opts.AccountName = clusterDef.Spec.CivoOrganization
		if opts.AccountName != "" {
			if token, err := pcMgr.GetCivoToken(opts.AccountName); err == nil && token != "" {
				opts.APIKey = token
			}
		}

	case "aws":
		opts.AccountName = clusterDef.Spec.AWSAccount
		if opts.AccountName != "" {
			keyID, secret, session, _ := pcMgr.GetAWSCredentials(opts.AccountName)
			opts.AccessKeyID = keyID
			opts.SecretAccessKey = secret
			opts.SessionToken = session
		}

	case "gcp":
		opts.AccountName = clusterDef.Spec.GCPProject
		if clusterDef.Spec.GCPProjectID != "" {
			opts.ProjectID = clusterDef.Spec.GCPProjectID
			log.Printf("Using GCP project ID '%s' for cluster %s", clusterDef.Spec.GCPProjectID, clusterDef.Metadata.Name)
		} else if opts.AccountName != "" {
			projectID, err := pcMgr.GetGCPProjectID(opts.AccountName)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve GCP project '%s': %w", opts.AccountName, err)
			}
			opts.ProjectID = projectID
			log.Printf("Using GCP project '%s' (ID: %s) for cluster %s", opts.AccountName, projectID, clusterDef.Metadata.Name)
		}
		if opts.AccountName != "" {
			credJSON, _ := pcMgr.GetGCPCredentialsJSON(opts.AccountName)
			opts.GCPCredentialsJSON = credJSON
		}

	case "azure":
		opts.AccountName = clusterDef.Spec.AzureSubscription
		if clusterDef.Spec.AzureSubscriptionID != "" {
			opts.AzureSubscriptionID = clusterDef.Spec.AzureSubscriptionID
			log.Printf("Using Azure subscription ID '%s' for cluster %s", clusterDef.Spec.AzureSubscriptionID, clusterDef.Metadata.Name)
		} else if opts.AccountName != "" {
			subID, err := pcMgr.GetAzureSubscriptionID(opts.AccountName)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve Azure subscription '%s': %w", opts.AccountName, err)
			}
			opts.AzureSubscriptionID = subID
			log.Printf("Using Azure subscription '%s' (ID: %s) for cluster %s", opts.AccountName, subID, clusterDef.Metadata.Name)
		}
		if opts.AccountName != "" {
			tenantID, clientID, clientSecret, _ := pcMgr.GetAzureCredentials(opts.AccountName)
			opts.AzureTenantID = tenantID
			opts.AzureClientID = clientID
			opts.AzureClientSecret = clientSecret
		}
		opts.AzureResourceGroup = clusterDef.Spec.AzureResourceGroup
	}

	return r.providerFactory.CreateProviderWithOptions(providerName, opts)
}

// reconcileCluster handles the reconciliation of a single cluster
func (r *Reconciler) reconcileCluster(ctx context.Context, clusterMgr *cluster.Manager, clusterDef types.ClusterDefinition) error {
	action := clusterMgr.DetermineAction(ctx, clusterDef)

	switch action {
	case types.ActionCreate:
		return r.createCluster(ctx, clusterMgr, clusterDef)
	case types.ActionUpdate:
		return r.updateCluster(ctx, clusterMgr, clusterDef)
	case types.ActionDelete:
		return r.deleteCluster(ctx, clusterMgr, clusterDef)
	case types.ActionNone:
		log.Printf("Cluster %s is up to date, no action needed", clusterDef.Metadata.Name)
		return nil
	default:
		log.Printf("Unknown action for cluster %s: %v", clusterDef.Metadata.Name, action)
		return nil
	}
}

// createCluster creates a new cluster
func (r *Reconciler) createCluster(ctx context.Context, clusterMgr *cluster.Manager, clusterDef types.ClusterDefinition) error {
	existingCluster, err := clusterMgr.FindByName(ctx, clusterDef.Metadata.Name)
	if err != nil {
		return err
	}

	if existingCluster != nil {
		log.Printf("Cluster %s already exists with ID %s, updating status and continuing",
			clusterDef.Metadata.Name, existingCluster.ID)
		log.Printf("Cluster %s already exists and is in %s state", clusterDef.Metadata.Name, existingCluster.Status)
		return nil
	}

	log.Printf("Creating cluster with definition: %+v", clusterDef)
	createdCluster, err := clusterMgr.Create(ctx, clusterDef)
	if err != nil {
		return err
	}

	err = clusterMgr.WaitForReady(ctx, createdCluster.ID)
	if err != nil {
		return err
	}
	log.Println("Cluster is ready!")

	log.Printf("Cluster %s created successfully!", clusterDef.Metadata.Name)

	// Run onCreated workflows if defined
	if len(clusterDef.Spec.Workflows.OnCreated) > 0 {
		log.Printf("🔄 Running onCreated workflows for cluster %s...", clusterDef.Metadata.Name)
		r.runWorkflows(ctx, clusterDef.Spec.Workflows.OnCreated, clusterDef.Metadata.Name)
	}

	return nil
}

// updateCluster updates an existing cluster
func (r *Reconciler) updateCluster(ctx context.Context, clusterMgr *cluster.Manager, clusterDef types.ClusterDefinition) error {
	return clusterMgr.Update(ctx, clusterDef)
}

// deleteCluster deletes a cluster and its associated resources
func (r *Reconciler) deleteCluster(ctx context.Context, clusterMgr *cluster.Manager, clusterDef types.ClusterDefinition) error {
	existingCluster, err := clusterMgr.FindByName(ctx, clusterDef.Metadata.Name)
	if err != nil {
		return err
	}

	if existingCluster == nil {
		log.Printf("Cluster %s not found, nothing to delete", clusterDef.Metadata.Name)
		return nil
	}

	// Run onDestroy workflows before deletion if defined
	if len(clusterDef.Spec.Workflows.OnDestroy) > 0 {
		log.Printf("🔄 Running onDestroy workflows for cluster %s...", clusterDef.Metadata.Name)
		r.runWorkflows(ctx, clusterDef.Spec.Workflows.OnDestroy, clusterDef.Metadata.Name)
	}

	log.Printf("Deleting cluster %s with ID %s", clusterDef.Metadata.Name, existingCluster.ID)

	err = clusterMgr.Delete(ctx, existingCluster.ID)
	if err != nil {
		return err
	}

	log.Printf("Cluster %s deleted successfully!", clusterDef.Metadata.Name)
	return nil
}

// cleanupOrphanedResources removes managed-prefix clusters that are no longer defined
func (r *Reconciler) cleanupOrphanedResources(ctx context.Context, clusterMgr *cluster.Manager, clusters []types.ClusterDefinition) error {
	orphanedClusters, err := clusterMgr.FindOrphaned(ctx, clusters)
	if err != nil {
		return err
	}

	return clusterMgr.CleanupOrphaned(ctx, orphanedClusters)
}

// strictDeleteSweep deletes every cloud cluster that has no matching YAML definition.
// It reads all configured accounts/projects/subscriptions/organizations from the
// provider config files and queries each one independently for active clusters,
// comparing the results against the current repository state.
func (r *Reconciler) strictDeleteSweep(ctx context.Context) {
	// Load the current desired state fresh from the repository.
	desiredClusters, err := r.stateMgr.LoadClusterDefinitions()
	if err != nil {
		log.Printf("strict-delete: failed to load cluster definitions: %v", err)
		return
	}

	pcMgr := providerconfig.NewManager(r.stateMgr.GetStateRoot())

	// --- Civo organizations ---
	civoOrgs, err := pcMgr.ListCivoOrganizations()
	if err != nil {
		log.Printf("strict-delete: failed to list Civo organizations: %v", err)
	}
	// When no named organizations are configured, sweep with empty credentials
	// so clusters in unregistered accounts are still found.
	if len(civoOrgs) == 0 {
		civoOrgs = []providerconfig.CivoOrganization{{Name: "", Regions: nil}}
	}
	for _, org := range civoOrgs {
		regions := org.Regions
		if len(regions) == 0 {
			regions = []string{"PHX1", "NYC1", "FRA1", "LON1"}
		}
		for _, region := range dedupRegions(regions) {
			prov, err := r.createProviderForCluster(types.ClusterDefinition{
				Metadata: types.ClusterMetadata{Region: region},
				Spec:     types.ClusterSpec{Provider: "civo", CivoOrganization: org.Name},
			})
			if err != nil {
				log.Printf("strict-delete: Civo org=%q region=%s: failed to create provider: %v", org.Name, region, err)
				continue
			}
			log.Printf("strict-delete: sweeping Civo org=%q region=%s", org.Name, region)
			if err := cluster.NewManager(prov).StrictDeleteOrphans(ctx, desiredClusters); err != nil {
				log.Printf("strict-delete: Civo org=%q region=%s: %v", org.Name, region, err)
			}
		}
	}

	// --- AWS accounts ---
	awsAccounts, err := pcMgr.ListAWSAccounts()
	if err != nil {
		log.Printf("strict-delete: failed to list AWS accounts: %v", err)
	}
	// When no named accounts are configured, fall back to the default credential
	// chain (AWS_ACCESS_KEY_ID / AWS_PROFILE / instance role) so clusters in
	// unregistered accounts are still found.
	if len(awsAccounts) == 0 {
		awsAccounts = []providerconfig.AWSAccount{{Name: "", Regions: nil}}
	}
	for _, account := range awsAccounts {
		regions := account.Regions
		if len(regions) == 0 {
			regions = []string{"us-east-1", "us-west-2", "eu-west-1", "eu-central-1", "ap-southeast-1"}
		}
		for _, region := range dedupRegions(regions) {
			prov, err := r.createProviderForCluster(types.ClusterDefinition{
				Metadata: types.ClusterMetadata{Region: region},
				Spec:     types.ClusterSpec{Provider: "aws", AWSAccount: account.Name},
			})
			if err != nil {
				log.Printf("strict-delete: AWS account=%q region=%s: failed to create provider: %v", account.Name, region, err)
				continue
			}
			log.Printf("strict-delete: sweeping AWS account=%q region=%s", account.Name, region)
			if err := cluster.NewManager(prov).StrictDeleteOrphans(ctx, desiredClusters); err != nil {
				log.Printf("strict-delete: AWS account=%q region=%s: %v", account.Name, region, err)
			}
		}
	}

	// --- GCP projects ---
	// Use "-" as the location to list all clusters across all zones and regions
	// in a single API call per project (GCP wildcard location).
	gcpProjects, err := pcMgr.ListGCPProjects()
	if err != nil {
		log.Printf("strict-delete: failed to list GCP projects: %v", err)
	}
	for _, project := range gcpProjects {
		prov, err := r.createProviderForCluster(types.ClusterDefinition{
			Metadata: types.ClusterMetadata{Region: "-"},
			Spec:     types.ClusterSpec{Provider: "gcp", GCPProject: project.Name},
		})
		if err != nil {
			log.Printf("strict-delete: GCP project=%q: failed to create provider: %v", project.Name, err)
			continue
		}
		log.Printf("strict-delete: sweeping GCP project=%q (all regions)", project.Name)
		if err := cluster.NewManager(prov).StrictDeleteOrphans(ctx, desiredClusters); err != nil {
			log.Printf("strict-delete: GCP project=%q: %v", project.Name, err)
		}
	}

	// --- Azure subscriptions ---
	// Each resource group is a separate provider scope; its Location is used as the region.
	azureSubs, err := pcMgr.ListAzureSubscriptions()
	if err != nil {
		log.Printf("strict-delete: failed to list Azure subscriptions: %v", err)
	}
	for _, sub := range azureSubs {
		if len(sub.ResourceGroups) == 0 {
			log.Printf("strict-delete: Azure subscription=%q has no resource groups configured, skipping", sub.Name)
			continue
		}
		for _, rg := range sub.ResourceGroups {
			location := rg.Location
			if location == "" {
				location = "eastus"
			}
			prov, err := r.createProviderForCluster(types.ClusterDefinition{
				Metadata: types.ClusterMetadata{Region: location},
				Spec: types.ClusterSpec{
					Provider:           "azure",
					AzureSubscription:  sub.Name,
					AzureResourceGroup: rg.Name,
				},
			})
			if err != nil {
				log.Printf("strict-delete: Azure sub=%q rg=%q: failed to create provider: %v", sub.Name, rg.Name, err)
				continue
			}
			log.Printf("strict-delete: sweeping Azure subscription=%q resource-group=%q", sub.Name, rg.Name)
			if err := cluster.NewManager(prov).StrictDeleteOrphans(ctx, desiredClusters); err != nil {
				log.Printf("strict-delete: Azure sub=%q rg=%q: %v", sub.Name, rg.Name, err)
			}
		}
	}
}

// dedupRegions returns the input slice with duplicates removed, preserving order.
func dedupRegions(regions []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(regions))
	for _, r := range regions {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// exportAllClusterInfo exports information for all active clusters
func (r *Reconciler) exportAllClusterInfo(ctx context.Context, clusterDefs []types.ClusterDefinition) {
	for _, clusterDef := range clusterDefs {
		err := r.exportClusterInfo(ctx, clusterDef)
		if err != nil {
			log.Printf("Failed to export info for cluster %s: %v", clusterDef.Metadata.Name, err)
		}
	}
}

// exportClusterInfo exports cluster information
func (r *Reconciler) exportClusterInfo(ctx context.Context, clusterDef types.ClusterDefinition) error {
	prov, err := r.createProviderForCluster(clusterDef)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	clusterMgr := cluster.NewManager(prov)
	return exportClusterInfoToEnv(ctx, clusterMgr, clusterDef.Metadata.Name)
}

// syncKubeconfigs syncs kubeconfigs for all active clusters
func (r *Reconciler) syncKubeconfigs(ctx context.Context, clusterDefs []types.ClusterDefinition) error {
	// Get current repository name
	repoMgr, err := repository.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create repository manager: %w", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		return fmt.Errorf("failed to get current repository: %w", err)
	}

	// Create kubeconfig manager
	kubeconfigMgr, err := kubeconfig.NewManager(currentRepo.Name)
	if err != nil {
		return fmt.Errorf("failed to create kubeconfig manager: %w", err)
	}
	defer kubeconfigMgr.Close()

	// Collect all active cluster names for orphan cleanup after syncing.
	activeClusterNames := make([]string, 0, len(clusterDefs))
	for _, cd := range clusterDefs {
		activeClusterNames = append(activeClusterNames, cd.Metadata.Name)
	}

	// Group clusters by region so each cluster gets a provider with the right credentials.
	regionClusters := make(map[string][]types.ClusterDefinition)
	for _, clusterDef := range clusterDefs {
		region := clusterDef.Metadata.Region
		regionClusters[region] = append(regionClusters[region], clusterDef)
	}

	// Sync kubeconfigs for each cluster individually (different credentials per cluster).
	// Use SyncSingleKubeconfig so cleanup is NOT called per-iteration — calling
	// CleanupOrphanedKubeconfigs with a single name inside a loop would delete every
	// other cluster's kubeconfig before its iteration runs.
	for region, clusters := range regionClusters {
		for _, clusterDef := range clusters {
			prov, err := r.createProviderForCluster(clusterDef)
			if err != nil {
				log.Printf("Failed to create provider for cluster %s in region %s: %v",
					clusterDef.Metadata.Name, region, err)
				continue
			}

			syncer := kubeconfig.NewSyncer(kubeconfigMgr, prov)
			if err := syncer.SyncSingleKubeconfig(ctx, clusterDef.Metadata.Name); err != nil {
				log.Printf("Failed to sync kubeconfig for cluster %s in region %s: %v",
					clusterDef.Metadata.Name, region, err)
			}
		}
	}

	// Clean up kubeconfigs for clusters no longer in the desired state.
	if err := kubeconfigMgr.CleanupOrphanedKubeconfigs(activeClusterNames); err != nil {
		log.Printf("Failed to cleanup orphaned kubeconfigs: %v", err)
	}

	return nil
}

// runWorkflows executes a list of workflows for a cluster
func (r *Reconciler) runWorkflows(ctx context.Context, workflowNames []string, clusterName string) {
	if len(workflowNames) == 0 {
		return
	}

	// Create workflow manager using the local path already known to the state
	// manager — avoids a database lookup that fails when --path is used.
	workflowMgr, err := workflow.NewManager(r.stateMgr.LocalPath())
	if err != nil {
		log.Printf("⚠️  Failed to create workflow manager: %v", err)
		return
	}

	// Create workflow executor
	executor, err := workflow.NewExecutor(workflowMgr, clusterName)
	if err != nil {
		log.Printf("⚠️  Failed to create workflow executor: %v", err)
		return
	}
	defer executor.Close()

	// Run each workflow
	for _, workflowName := range workflowNames {
		log.Printf("▶️  Running workflow '%s' for cluster '%s'...", workflowName, clusterName)

		execution, err := executor.RunWorkflow(ctx, workflowName, clusterName)
		if err != nil {
			log.Printf("⚠️  Workflow '%s' failed: %v", workflowName, err)
			continue
		}

		if execution.Status == workflow.StatusCompleted {
			log.Printf("✅ Workflow '%s' completed successfully", workflowName)
		} else {
			log.Printf("⚠️  Workflow '%s' finished with status: %s", workflowName, execution.Status)
		}
	}
}
