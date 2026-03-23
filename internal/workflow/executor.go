package workflow

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	cluster_pkg "github.com/cbridges1/hyve/internal/cluster"
	"github.com/cbridges1/hyve/internal/config"
	"github.com/cbridges1/hyve/internal/kubeconfig"
	"github.com/cbridges1/hyve/internal/provider"
	"github.com/cbridges1/hyve/internal/providerconfig"
	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/types"
)

// Executor handles workflow execution
type Executor struct {
	manager           *Manager
	kubeconfigManager *kubeconfig.Manager
	execution         *WorkflowExecution
	currentCluster    string
	variables         map[string]string
	workingDir        string
	repoName          string
}

// NewExecutor creates a new workflow executor
func NewExecutor(manager *Manager, cluster string) (*Executor, error) {
	var kubeconfigMgr *kubeconfig.Manager
	var err error

	var repoName string
	execRepoMgr, repoErr := repository.NewManager()
	if repoErr == nil {
		defer execRepoMgr.Close()
		if currentRepo, err := execRepoMgr.GetCurrentRepository(); err == nil {
			repoName = currentRepo.Name
		}
	}

	if cluster != "" {
		kubeconfigMgr, err = kubeconfig.NewManager(repoName)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubeconfig manager: %w", err)
		}
	}

	return &Executor{
		manager:           manager,
		kubeconfigManager: kubeconfigMgr,
		currentCluster:    cluster,
		variables:         make(map[string]string),
		workingDir:        manager.localPath,
		repoName:          repoName,
	}, nil
}

// RunWorkflow executes a workflow
func (e *Executor) RunWorkflow(ctx context.Context, workflowName string, cluster string) (*WorkflowExecution, error) {
	workflow, err := e.manager.GetWorkflow(workflowName)
	if err != nil {
		return nil, fmt.Errorf("failed to get workflow: %w", err)
	}

	// Use specified cluster or default
	targetCluster := cluster
	if targetCluster == "" {
		targetCluster = e.currentCluster
	}

	// Create execution instance
	execution := &WorkflowExecution{
		ID:           generateExecutionID(),
		WorkflowName: workflowName,
		Cluster:      targetCluster,
		Status:       StatusRunning,
		StartTime:    time.Now(),
		Trigger:      "manual",
		JobResults:   make(map[string]*JobResult),
		Logs:         []WorkflowLogEntry{},
		Variables:    make(map[string]string),
	}

	e.execution = execution
	e.addLog("INFO", "", "", fmt.Sprintf("Starting workflow '%s'", workflowName))

	// Validate workflow requirements
	if workflow.Spec.Requirements != nil {
		e.addLog("INFO", "", "", "Validating workflow requirements...")
		validator, err := NewRequirementValidator()
		if err != nil {
			e.execution.Status = StatusFailed
			e.addLog("ERROR", "", "", fmt.Sprintf("Failed to create requirement validator: %v", err))
			return execution, fmt.Errorf("failed to create requirement validator: %w", err)
		}
		defer validator.Close()

		// Validate all requirements
		if err := validator.ValidateRequirements(workflow.Spec.Requirements); err != nil {
			e.execution.Status = StatusFailed
			e.addLog("ERROR", "", "", fmt.Sprintf("Requirements validation failed: %v", err))
			return execution, fmt.Errorf("requirements validation failed: %w", err)
		}

		// Load secrets into environment
		if err := validator.LoadSecretsIntoEnvironment(workflow.Spec.Requirements); err != nil {
			e.execution.Status = StatusFailed
			e.addLog("ERROR", "", "", fmt.Sprintf("Failed to load secrets: %v", err))
			return execution, fmt.Errorf("failed to load secrets: %w", err)
		}

		e.addLog("INFO", "", "", "✅ All requirements validated successfully")
	}

	// Set up kubeconfig if cluster specified
	var kubeconfigPath string
	if targetCluster != "" {
		kubeconfigPath, err = e.setupKubeconfig(ctx, targetCluster)
		if err != nil {
			e.execution.Status = StatusFailed
			e.addLog("ERROR", "", "", fmt.Sprintf("Failed to setup kubeconfig: %v", err))
			return execution, fmt.Errorf("failed to setup kubeconfig: %w", err)
		}
		defer e.cleanupKubeconfig(kubeconfigPath)
	}

	// Set up environment variables
	if err := e.setupEnvironmentVariables(ctx, workflow, targetCluster, kubeconfigPath); err != nil {
		e.execution.Status = StatusFailed
		e.addLog("ERROR", "", "", fmt.Sprintf("Failed to setup environment variables: %v", err))
		return execution, fmt.Errorf("failed to setup environment variables: %w", err)
	}

	// Execute jobs
	if err := e.executeJobs(ctx, workflow); err != nil {
		e.execution.Status = StatusFailed
		e.addLog("ERROR", "", "", fmt.Sprintf("Workflow failed: %v", err))
		e.finalizeExecution()
		return execution, fmt.Errorf("workflow execution failed: %w", err)
	}

	e.execution.Status = StatusCompleted
	e.addLog("INFO", "", "", "Workflow completed successfully")
	e.finalizeExecution()

	return execution, nil
}

// setupKubeconfig sets up kubeconfig for the target cluster.
// It always attempts a fresh sync from the provider first (ensuring tokens are
// current), then falls back to whatever is stored in the database.
func (e *Executor) setupKubeconfig(ctx context.Context, cluster string) (string, error) {
	if e.kubeconfigManager == nil {
		return "", fmt.Errorf("kubeconfig manager not initialized")
	}

	// Attempt a fresh sync so credentials are never stale (e.g. GCP bearer tokens).
	clusterDef, defErr := e.loadClusterDefinition(cluster)
	if defErr == nil {
		prov, provErr := e.createProviderFromClusterDef(clusterDef)
		if provErr == nil {
			syncer := kubeconfig.NewSyncer(e.kubeconfigManager, prov)
			if err := syncer.SyncSingleKubeconfig(ctx, cluster); err != nil {
				log.Printf("Warning: kubeconfig sync failed for cluster '%s', falling back to cached credentials: %v", cluster, err)
			}
		} else {
			log.Printf("Warning: could not create provider for kubeconfig sync: %v", provErr)
		}
	} else {
		log.Printf("Warning: could not load cluster definition for kubeconfig sync: %v", defErr)
	}

	kc, err := e.kubeconfigManager.GetKubeconfig(cluster)
	if err != nil {
		return "", fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	var kubeconfigData string

	if kc == nil {
		// Sync didn't store anything — try fetching directly from the provider.
		log.Printf("No stored kubeconfig found for cluster '%s', fetching from provider...", cluster)

		if defErr != nil {
			return "", fmt.Errorf("no kubeconfig found for cluster '%s' and failed to load cluster definition: %w", cluster, defErr)
		}

		prov, err := e.createProviderFromClusterDef(clusterDef)
		if err != nil {
			return "", fmt.Errorf("no kubeconfig found for cluster '%s' and failed to create provider: %w", cluster, err)
		}

		clusterMgr := cluster_pkg.NewManager(prov)
		clusterInfo, err := clusterMgr.GetClusterInfo(ctx, cluster)
		if err != nil {
			return "", fmt.Errorf("no kubeconfig found for cluster '%s' and failed to fetch from provider: %w", cluster, err)
		}

		if clusterInfo == nil || clusterInfo.Kubeconfig == "" {
			return "", fmt.Errorf("no kubeconfig found for cluster '%s'", cluster)
		}

		kubeconfigData = clusterInfo.Kubeconfig

		if _, err := e.kubeconfigManager.StoreKubeconfig(cluster, kubeconfigData); err != nil {
			log.Printf("Warning: failed to store kubeconfig for cluster '%s': %v", cluster, err)
		}
	} else {
		kubeconfigData, err = kc.GetConfig()
		if err != nil {
			return "", fmt.Errorf("failed to decrypt kubeconfig: %w", err)
		}
	}

	// Create temporary kubeconfig file
	tempDir := filepath.Join(os.Getenv("HOME"), ".hyve", "temp")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	tempFile := filepath.Join(tempDir, fmt.Sprintf("kubeconfig-workflow-%s", cluster))
	if err := os.WriteFile(tempFile, []byte(kubeconfigData), 0600); err != nil {
		return "", fmt.Errorf("failed to write temporary kubeconfig: %w", err)
	}

	return tempFile, nil
}

// cleanupKubeconfig cleans up temporary kubeconfig file
func (e *Executor) cleanupKubeconfig(kubeconfigPath string) {
	if kubeconfigPath != "" {
		os.Remove(kubeconfigPath)
	}
}

// setupEnvironmentVariables sets up environment variables for the workflow
func (e *Executor) setupEnvironmentVariables(ctx context.Context, workflow *Workflow, targetCluster string, kubeconfigPath string) error {
	e.variables["WORKFLOW_NAME"] = workflow.Metadata.Name
	e.variables["WORKFLOW_CLUSTER"] = e.currentCluster
	e.variables["WORKFLOW_EXECUTION_ID"] = e.execution.ID
	e.variables["HYVE_REPOSITORY"] = e.repoName
	e.variables["HYVE_REPOSITORY_PATH"] = e.manager.localPath

	if kubeconfigPath != "" {
		e.variables["KUBECONFIG"] = kubeconfigPath
		os.Setenv("KUBECONFIG", kubeconfigPath)
	}

	// Export cluster-specific environment variables if cluster is specified
	if targetCluster != "" {
		if err := e.exportClusterEnvironmentVariables(ctx, targetCluster); err != nil {
			log.Printf("Warning: Failed to export cluster environment variables: %v", err)
			// Don't fail the workflow if we can't get cluster info
		}
	}

	return nil
}

// exportClusterEnvironmentVariables exports cluster-specific environment variables
func (e *Executor) exportClusterEnvironmentVariables(ctx context.Context, clusterName string) error {
	// Load cluster definitions from YAML files
	clusterDef, err := e.loadClusterDefinition(clusterName)
	if err != nil {
		return fmt.Errorf("failed to load cluster definition: %w", err)
	}

	// Create provider for this cluster
	prov, err := e.createProviderFromClusterDef(clusterDef)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	// Create cluster manager
	clusterMgr := cluster_pkg.NewManager(prov)

	// Get cluster information
	clusterInfo, err := clusterMgr.GetClusterInfo(ctx, clusterName)
	if err != nil {
		return fmt.Errorf("failed to get cluster info: %w", err)
	}

	if clusterInfo == nil {
		return fmt.Errorf("cluster info not found")
	}

	// Set HYVE_CLUSTER_* environment variables
	e.variables["HYVE_CLUSTER_NAME"] = clusterInfo.Name
	e.variables["HYVE_CLUSTER_IP_ADDRESS"] = clusterInfo.IPAddress
	e.variables["HYVE_CLUSTER_ACCESS_PORT"] = clusterInfo.AccessPort
	e.variables["HYVE_CLUSTER_ID"] = clusterInfo.ID
	e.variables["HYVE_CLUSTER_STATUS"] = clusterInfo.Status
	e.variables["HYVE_CLUSTER_KUBECONFIG"] = clusterInfo.Kubeconfig

	// Also export to process environment so they're available in scripts
	os.Setenv("HYVE_CLUSTER_NAME", clusterInfo.Name)
	os.Setenv("HYVE_CLUSTER_IP_ADDRESS", clusterInfo.IPAddress)
	os.Setenv("HYVE_CLUSTER_ACCESS_PORT", clusterInfo.AccessPort)
	os.Setenv("HYVE_CLUSTER_ID", clusterInfo.ID)
	os.Setenv("HYVE_CLUSTER_STATUS", clusterInfo.Status)
	os.Setenv("HYVE_CLUSTER_KUBECONFIG", clusterInfo.Kubeconfig)

	log.Printf("✅ Exported cluster information to environment:")
	log.Printf("  HYVE_CLUSTER_NAME=%s", clusterInfo.Name)
	log.Printf("  HYVE_CLUSTER_IP_ADDRESS=%s", clusterInfo.IPAddress)
	log.Printf("  HYVE_CLUSTER_ACCESS_PORT=%s", clusterInfo.AccessPort)
	log.Printf("  HYVE_CLUSTER_ID=%s", clusterInfo.ID)
	log.Printf("  HYVE_CLUSTER_STATUS=%s", clusterInfo.Status)

	return nil
}

// createProviderFromClusterDef creates a provider for the given cluster definition
func (e *Executor) createProviderFromClusterDef(clusterDef *types.ClusterDefinition) (provider.Provider, error) {
	providerName := clusterDef.Spec.Provider
	if providerName == "" {
		providerName = "civo"
	}

	opts := provider.ProviderOptions{
		Region: clusterDef.Metadata.Region,
	}

	switch strings.ToLower(providerName) {
	case "civo":
		opts.AccountName = clusterDef.Spec.CivoOrganization
		opts.APIKey = config.NewManager().GetCivoToken(clusterDef.Spec.CivoOrganization)
	case "aws":
		opts.AccountName = clusterDef.Spec.AWSAccount
	case "gcp":
		opts.AccountName = clusterDef.Spec.GCPProject
		if clusterDef.Spec.GCPProject != "" {
			if repoMgr, err := repository.NewManager(); err == nil {
				defer repoMgr.Close()
				if currentRepo, err := repoMgr.GetCurrentRepository(); err == nil {
					pcMgr := providerconfig.NewManager(currentRepo.LocalPath)
					if projectID, err := pcMgr.GetGCPProjectID(clusterDef.Spec.GCPProject); err == nil {
						opts.ProjectID = projectID
					}
				}
			}
		}
	case "azure":
		opts.AccountName = clusterDef.Spec.AzureSubscription
		opts.AzureResourceGroup = clusterDef.Spec.AzureResourceGroup
		if clusterDef.Spec.AzureSubscription != "" {
			if repoMgr, err := repository.NewManager(); err == nil {
				defer repoMgr.Close()
				if currentRepo, err := repoMgr.GetCurrentRepository(); err == nil {
					pcMgr := providerconfig.NewManager(currentRepo.LocalPath)
					if subID, err := pcMgr.GetAzureSubscriptionID(clusterDef.Spec.AzureSubscription); err == nil {
						opts.AzureSubscriptionID = subID
					}
				}
			}
		}
	}

	factory := provider.NewFactory()
	return factory.CreateProviderWithOptions(providerName, opts)
}

// loadClusterDefinition loads a cluster definition from YAML file
func (e *Executor) loadClusterDefinition(clusterName string) (*types.ClusterDefinition, error) {
	clustersDir := filepath.Join(e.manager.localPath, "clusters")
	var clusterDef *types.ClusterDefinition

	// Check if clusters directory exists
	if _, err := os.Stat(clustersDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("clusters directory not found at %s", clustersDir)
	}

	err := filepath.WalkDir(clustersDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || (!strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml")) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", path, err)
		}

		var cluster types.ClusterDefinition
		if err := yaml.Unmarshal(data, &cluster); err != nil {
			return fmt.Errorf("failed to unmarshal cluster definition from %s: %w", path, err)
		}

		if cluster.Metadata.Name == clusterName {
			clusterDef = &cluster
			return filepath.SkipAll
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	if clusterDef == nil {
		return nil, fmt.Errorf("cluster %s not found in clusters directory", clusterName)
	}

	return clusterDef, nil
}

// expandVariables expands variables in a string
func (e *Executor) expandVariables(input string) string {
	result := input
	for key, value := range e.variables {
		result = strings.ReplaceAll(result, fmt.Sprintf("${%s}", key), value)
		result = strings.ReplaceAll(result, fmt.Sprintf("$%s", key), value)
	}
	return result
}

// addLog adds a log entry to the execution
func (e *Executor) addLog(level, job, step, message string) {
	entry := WorkflowLogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Job:       job,
		Step:      step,
		Message:   message,
	}

	e.execution.Logs = append(e.execution.Logs, entry)

	// Also log to stdout for immediate feedback
	prefix := fmt.Sprintf("[%s]", level)
	if job != "" {
		prefix += fmt.Sprintf("[%s]", job)
	}
	if step != "" {
		prefix += fmt.Sprintf("[%s]", step)
	}
	log.Printf("%s %s", prefix, message)
}

// finalizeExecution finalizes the workflow execution
func (e *Executor) finalizeExecution() {
	endTime := time.Now()
	e.execution.EndTime = &endTime
	e.execution.Duration = endTime.Sub(e.execution.StartTime)
}

// generateExecutionID generates a unique execution ID
func generateExecutionID() string {
	return fmt.Sprintf("exec_%d", time.Now().Unix())
}

// Close closes the executor and cleans up resources
func (e *Executor) Close() error {
	if e.kubeconfigManager != nil {
		return e.kubeconfigManager.Close()
	}
	return nil
}
