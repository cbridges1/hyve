package workflow

import (
	"log"
	"time"

	"github.com/cbridges1/hyve/cmd/shared"
)

// workflowRunPollTimeout bounds how long `hyve workflow run` (cluster mode)
// waits for its dispatched WorkflowRun to reach a terminal phase —
// generous, since a workflow's own steps (e.g. a Helm install with --wait)
// can legitimately take several minutes. Matches this codebase's existing
// precedent of a named constant rather than a magic number (see
// internal/api/accessmethod_mint.go's mintTimeout).
const workflowRunPollTimeout = 10 * time.Minute

// workflowRunPollInterval is how often the CLI re-checks a WorkflowRun's
// status while waiting.
const workflowRunPollInterval = 2 * time.Second

// runWorkflowClusterMode is `hyve workflow run`'s cluster-mode path — it
// creates a WorkflowRun CR via the API (internal/api/workflowruns.go) and
// polls its status until WorkflowRunReconciler (running in hyve-controller)
// drives it to Succeeded/Failed. Unlike local mode's runWorkflowByRef, this
// requires --cluster: a WorkflowRun has nothing to authenticate against
// without one, whereas local mode tolerates no cluster context for
// workflows with no cluster-dependent steps.
func runWorkflowClusterMode(client *shared.APIClient, nameOrSource, pathFlag, cluster string, showLogs bool, setVars map[string]string) {
	if cluster == "" {
		log.Fatal("--cluster is required in cluster mode — a workflow run needs a target ClusterDefinition to authenticate against")
	}

	var workflowName, source string
	if looksLikeRemoteSource(nameOrSource) {
		source = nameOrSource
	} else {
		workflowName = nameOrSource
	}

	created, err := client.CreateWorkflowRun(workflowName, source, pathFlag, cluster, setVars)
	if err != nil {
		log.Fatalf("Failed to start workflow run: %v", err)
	}

	log.Printf("🚀 Running workflow '%s' against cluster '%s' (%s)...", nameOrSource, cluster, created.Name)

	deadline := time.Now().Add(workflowRunPollTimeout)
	for {
		status, err := client.GetWorkflowRun(created.Name)
		if err != nil {
			log.Fatalf("Failed to check workflow run status: %v", err)
		}

		switch status.Phase {
		case "Succeeded":
			log.Printf("✅ Workflow '%s' completed", nameOrSource)
			if showLogs && status.Output != "" {
				log.Println()
				log.Println(status.Output)
			}
			return
		case "Failed":
			if status.Output != "" && showLogs {
				log.Println()
				log.Println(status.Output)
			}
			log.Fatalf("❌ Workflow '%s' failed: %s", nameOrSource, status.Message)
		}

		if time.Now().After(deadline) {
			log.Fatalf("Timed out after %s waiting for workflow run %q to complete — check `kubectl get workflowruns.hyve.io %s` for its current state", workflowRunPollTimeout, created.Name, created.Name)
		}
		time.Sleep(workflowRunPollInterval)
	}
}
