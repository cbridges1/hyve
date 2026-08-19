package reconcile

import (
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/types"
	"github.com/cbridges1/hyve/internal/workflow"
)

// StateProvider is the source-of-truth abstraction Reconciler depends on for
// reading and persisting cluster definitions. It says nothing about how (or
// whether) the underlying storage is kept in sync with anything remote —
// that's deliberately outside its scope. state.Manager (local-directory-
// backed, used by the CLI) and internal/controller.CRDStateProvider
// (CRD-backed, used by the controller) both implement this.
type StateProvider interface {
	// LocalPath returns the root directory used for driver module
	// resolution, hyve.lock, and workflow definitions.
	LocalPath() string

	LoadRepoConfig() (*state.RepoConfig, error)

	LoadClusterDefinitions() ([]types.ClusterDefinition, error)

	// SaveClusterDefinition persists a cluster's driverOutputs/
	// appliedResources (and any spec changes) back to the source of truth.
	SaveClusterDefinition(def *types.ClusterDefinition) error

	RemoveClusterFile(name string) error

	HasStateSidecar(name string) bool

	// WorkflowSource resolves a local-name WorkflowRef into a runnable
	// Workflow definition — a plain FileSource under LocalPath() for file
	// mode; a CR-preferring ChainSource for CRD mode (see
	// internal/controller.CRDStateProvider.WorkflowSource's doc comment).
	WorkflowSource() workflow.Source
}

// Compile-time conformance check. Lives here, not in internal/state, since
// internal/reconcile already imports internal/state — putting the assertion
// there instead would create an import cycle.
var _ StateProvider = (*state.Manager)(nil)
