package reconcile

import (
	"context"
	"fmt"
	"os"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/types"
)

// HostKubeconfigIssuer mints a kubeconfig for hyve's own host cluster — the
// cluster hyve-controller/hyve-api themselves run on — so
// reconcileHostCluster can apply spec.resources against it with no driver
// module involved. Declared here, not in the concrete client-go-backed
// package that implements it (internal/hostauth, wrapped directly by
// cmd/controller/run.go), same "interface lives with the consumer, concrete
// impl lives with whoever owns the real dependency" shape as
// AgentTokenIssuer/StepRunner/ModuleRunner already establish for this same
// Reconciler. nil (the CLI's own default) means a primary-marked cluster
// with no real spec.driver simply can't reconcile spec.resources in
// local/file mode — logged as a warning, not a hard failure, since there's
// no control-plane cluster concept there at all to mint a credential for.
type HostKubeconfigIssuer interface {
	MintHostKubeconfig(ctx context.Context) ([]byte, error)
}

// isHostClusterWithoutDriver reports whether def is hyve's own host
// cluster (access.method: primary — see hyvev1alpha1.AccessMethodPrimary's
// own doc comment) with no real spec.driver assigned — the common,
// zero-config case this project settled on: the host cluster needs no
// module at all for its own lifecycle (auth/create/delete), since it
// already exists by definition and hyve-controller/hyve-api already run
// inside it. A primary-marked cluster WITH a real spec.driver (an admin's
// deliberate opt-out of that automatic path) is reconciled through the
// ordinary driver-based path (reconcileCluster) instead, exactly like any
// other cluster — this function returns false for that case on purpose.
func isHostClusterWithoutDriver(def types.ClusterDefinition) bool {
	return def.Spec.AccessMethod == types.AccessMethodPrimary && def.Spec.Driver.Source == ""
}

// reconcileHostCluster is ReconcileOne's dispatch target for a driver-less
// host cluster — see isHostClusterWithoutDriver. No status/create/delete/
// scale concepts apply here (there is no cloud lifecycle to drive: the
// cluster already exists by definition, it's the one hyve-controller/
// hyve-api themselves run on), so this skips reconcileCluster's whole
// driver-execution machinery entirely. What DOES still apply, per this
// project's own explicit decision: spec.resources reconciliation (this
// function's entire job) and ad hoc `hyve workflow run` (unaffected by any
// of this — it only needs a working kubeconfig, which `hyve cluster auth
// <name>` already produces via internal/api's own HostProvider, entirely
// independent of the reconcile loop). Lifecycle hook workflows
// (beforeCreate/onCreate/afterCreate/onDelete/afterDelete/preReconcile) are
// NOT run here — none of them have a natural trigger without a
// create/delete/param-drift concept to hang off of.
//
// cluster.Spec.Delete is honored only as "stop reconciling, let the
// finalizer clear" — see internal/controller.ClusterDefinitionReconciler.
// reconcileDelete, which removes ClusterDefinitionFinalizer as soon as
// this returns nil regardless of what (if anything) actually ran.
// Deliberately does NOT attempt to clean up previously-applied
// spec.resources on delete: this object represents "hyve's own record
// that this is the host cluster," not the cluster itself — unregistering
// it must never be mistaken for a request to tear anything down on a
// cluster hyve-controller/hyve-api are themselves still running on.
func (r *Reconciler) reconcileHostCluster(ctx context.Context, cluster types.ClusterDefinition, lf *module.LockFile, dryRun bool, secretsEnv map[string]string, hooks *ReconcileHooks) error {
	name := cluster.Metadata.Name

	if cluster.Spec.Delete {
		r.logf("[%s] Host cluster unregistered — no cleanup performed (this ClusterDefinition tracks hyve's own host cluster, not something hyve created)", name)
		return nil
	}

	if r.HostKubeconfigIssuer == nil {
		r.logf("[%s] Warning: no HostKubeconfigIssuer configured — skipping spec.resources reconciliation (local/file mode has no control-plane cluster concept)", name)
		return nil
	}

	kc, err := r.HostKubeconfigIssuer.MintHostKubeconfig(ctx)
	if err != nil {
		return fmt.Errorf("mint host cluster kubeconfig: %w", err)
	}
	kcFile, err := os.CreateTemp("", "hyve-host-kubeconfig-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp kubeconfig file for host cluster: %w", err)
	}
	defer os.Remove(kcFile.Name())
	if _, err := kcFile.Write(kc); err != nil {
		kcFile.Close()
		return fmt.Errorf("write temp kubeconfig file for host cluster: %w", err)
	}
	kcFile.Close()

	env := append(buildModuleEnv(cluster, secretsEnv), "KUBECONFIG="+kcFile.Name())

	// hyve-agent installation applies here too now (see
	// docs/HYVE-CLOUD-EXPOSURE-PROPOSAL.md) — the host cluster is meant to
	// reach itself through hyve-agent/AgentProvider like any other managed
	// cluster, not through HostProvider's own CA-pinned /proxy path, which
	// cannot work at all on a managed control plane (EKS/GKE/AKS never
	// expose the apiserver's own CA key). Before this, reconcileAgent was
	// only ever called from reconcileCluster's own dispatch branch — a
	// driver-less host cluster took this function's separate dispatch path
	// instead (see isHostClusterWithoutDriver) and so spec.access.agent
	// silently had no effect on it at all, regardless of what it was set
	// to. The kubeconfig just minted above (HostKubeconfigIssuer, already
	// cluster-admin-equivalent in-cluster) is exactly what reconcileAgent
	// needs to kubectl apply hyve-agent's own manifests — no driver module
	// involved, same as spec.resources below. Same warn-and-continue,
	// skip-on-dry-run stance as reconcileCluster's own call site: agent
	// install state is independent of everything else this function does,
	// and reconcileAgent has no read-only mode of its own.
	if dryRun {
		r.logf("[%s] DRY RUN: skipping hyve-agent reconciliation", name)
	} else if agentErr := r.reconcileAgent(ctx, &cluster, env); agentErr != nil {
		r.logf("[%s] Warning: hyve-agent reconciliation failed: %v", name, agentErr)
	}

	repoCfg, cfgErr := r.stateMgr.LoadRepoConfig()
	if cfgErr != nil {
		r.logf("[%s] Warning: failed to load hyve.yaml (defaulting strictResourceDelete=false): %v", name, cfgErr)
		repoCfg = &state.RepoConfig{}
	}
	return r.reconcileResources(ctx, &cluster, env, lf, repoCfg.Reconcile.StrictResourceDelete, dryRun)
}
