package controller

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/cbridges1/hyve/internal/agentpki"
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	internalcontroller "github.com/cbridges1/hyve/internal/controller"
	"github.com/cbridges1/hyve/internal/k8sjob"
	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/orgdb"
	"github.com/cbridges1/hyve/internal/reconcile"
	"github.com/cbridges1/hyve/internal/types"
	"github.com/cbridges1/hyve/internal/workflow"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
)

// resolveTargetNamespaces answers "which namespace(s) should this
// controller process reconcile":
//
//   - reconcilingClusterID set (Milestone 6): every organization mapped to
//     that id, queried from orgStore — an empty, non-nil slice (not an
//     error) when none are mapped yet, matching this command's own
//     "reconcile nothing until one is" stance. watchHome is ignored.
//   - watchHome (--watch-home-organizations, the home cluster's controller
//     in a multi-tenant install): currentNamespace plus every organization
//     still on the home cluster (reconciling_cluster_id NULL) — without it,
//     nothing ever reconciles those organizations' namespaces, since no
//     reconciling-cluster process claims them either.
//   - neither (Phase 1's default): exactly []string{currentNamespace},
//     orgStore unused (nil is fine, and expected).
//
// orgStore is read-only here — Milestone 6's one deliberate, narrow
// exception to "the controller never touches Store" (see
// HYVE-ORGANIZATION-MODEL-PROPOSAL.md, nexus-config/docs).
func resolveTargetNamespaces(ctx context.Context, orgStore *orgdb.Store, currentNamespace, reconcilingClusterID string, watchHome bool) ([]string, error) {
	if reconcilingClusterID == "" && !watchHome {
		return []string{currentNamespace}, nil
	}
	var id *string
	if reconcilingClusterID != "" {
		id = &reconcilingClusterID
	}
	orgs, err := orgStore.ListOrganizationsByReconcilingCluster(ctx, id)
	if err != nil {
		return nil, err
	}
	namespaces := make([]string, 0, len(orgs)+1)
	if id == nil {
		namespaces = append(namespaces, currentNamespace)
	}
	for _, org := range orgs {
		if id == nil && org.Namespace == currentNamespace {
			continue // the control plane's own organization, already first
		}
		namespaces = append(namespaces, org.Namespace)
	}
	return namespaces, nil
}

// errTargetNamespacesChanged ends the manager when the set
// resolveTargetNamespaces returns no longer matches the one this process
// started with (an organization was created, deleted, or migrated). The
// manager's cache is fixed to its namespaces at construction, so the
// controller exits and Kubernetes restarts it onto the new set.
var errTargetNamespacesChanged = errors.New("the set of namespaces to reconcile changed")

// watchTargetNamespaces re-runs resolve every interval and returns
// errTargetNamespacesChanged once its result differs (as a set) from
// current; a failed lookup is logged and retried, never fatal. Returns nil
// when ctx ends. Added to the manager as a Runnable, so its error stops
// mgr.Start.
func watchTargetNamespaces(ctx context.Context, interval time.Duration, current []string, resolve func(context.Context) ([]string, error)) error {
	want := make(map[string]bool, len(current))
	for _, ns := range current {
		want[ns] = true
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		got, err := resolve(ctx)
		if err != nil {
			log.Printf("⚠️  Could not re-check which namespaces to reconcile (%v) — retrying in %s", err, interval)
			continue
		}
		if len(got) != len(want) {
			return fmt.Errorf("%w: %v → %v", errTargetNamespacesChanged, current, got)
		}
		for _, ns := range got {
			if !want[ns] {
				return fmt.Errorf("%w: %v → %v", errTargetNamespacesChanged, current, got)
			}
		}
	}
}

// reconcilerSharedDeps bundles setup shared across every target
// namespace's own reconciler instances — see setupNamespaceReconcilers'
// own doc comment for exactly what stays shared versus what's rebuilt per
// namespace, and why.
type reconcilerSharedDeps struct {
	clientset               kubernetes.Interface
	configName              string
	modulesDir              string
	maxConcurrentReconciles int
	agentControlPlaneURL    string
	agentTunnelAddress      string
	agentCACertPEM          string

	// controlNamespace/hostServiceAccount/hostCAPath back
	// AgentTokenIssuer.ControlPlaneNamespace/Reconciler.AgentControlPlaneNamespace
	// and HostKubeconfigIssuer — this control-plane process's own identity
	// (which ServiceAccount it mints host/agent tokens against), always a
	// single fixed namespace (this command's own --namespace) regardless
	// of how many organization namespaces setupNamespaceReconcilers is
	// called for. Deliberately not looped per target namespace: unlike
	// StateProvider/StepRunner/ModuleRunner (which read/write each
	// organization's own ClusterDefinition/Workflow/Resource CRs and Jobs,
	// and so must be scoped to that organization's own namespace), minting
	// a host/agent-bootstrap token is about proving who *this process* is,
	// not which organization a given reconcile happens to be for — the
	// same ServiceAccount either way. This is a judgment call under real
	// ambiguity (Milestone 6's own proposal doesn't specify host/agent
	// access semantics for one reconciling-cluster process serving several
	// organizations at once) rather than something the plan settled —
	// worth revisiting if a real deployment needs per-organization host/
	// agent identities instead.
	controlNamespace   string
	hostServiceAccount string
	hostCAPath         string

	// homeOrganizations: every target namespace other than
	// controlNamespace is an organization on the home cluster
	// (--watch-home-organizations), whose clusters may use the control
	// plane's host as a management cluster when the install allows it.
	homeOrganizations bool
}

// setupNamespaceReconcilers builds and registers one full, independent set
// of per-namespace reconcile state — a CRDStateProvider, a
// *reconcile.Reconciler (with its own StepRunner/ModuleRunner scoped to
// targetNamespace, and HyveConfig-derived defaults read from that same
// namespace's own singleton), a ClusterDefinitionReconciler, and a
// WorkflowRunReconciler — for exactly one namespace on mgr.
//
// Called once per entry in cmd/controller/run.go's own targetNamespaces:
// with controllerNamePrefix empty (Phase 1's default, single-namespace
// mode) this registers the plain, unnamed SetupWithManager exactly as this
// command always has, so every controller name/metric/log line an existing
// single-tenant install already depends on stays unchanged. With
// controllerNamePrefix set (Milestone 6's --reconciling-cluster-id mode,
// one process reconciling several organizations' namespaces on the same
// manager/cache) this instead uses SetupWithManagerNamed's namespace-
// filtered variant — see that method's own doc comment for why a
// namespace predicate, not a separate cache, is what actually keeps one
// organization's reconciler from also picking up another's objects: every
// instance still shares the manager's one underlying ClusterDefinition/
// WorkflowRun watch.
func setupNamespaceReconcilers(mgr ctrl.Manager, targetNamespace, controllerNamePrefix string, deps reconcilerSharedDeps) error {
	stateProvider := &internalcontroller.CRDStateProvider{
		Client:         mgr.GetClient(),
		Namespace:      targetNamespace,
		ConfigName:     deps.configName,
		ModulesDirPath: deps.modulesDir,
	}

	// mgr.GetClient() returns a cached client whose cache only starts
	// syncing once mgr.Start() runs — reading through it here, before
	// Start(), would always fail with "the cache is not started" (confirmed
	// live: this exact bug shipped in an earlier version of this command).
	// mgr.GetAPIReader() reads directly from the API server, bypassing the
	// cache entirely, which is exactly what a one-time startup read needs.
	var startupCfg hyvev1alpha1.HyveConfig
	var defaultWorkflowImage, defaultModuleImage, defaultAgentImage string
	var imagePullSecrets []string
	var imageInstalls []k8sjob.ImageInstall
	err := mgr.GetAPIReader().Get(context.Background(), apitypes.NamespacedName{Namespace: targetNamespace, Name: deps.configName}, &startupCfg)
	if apierrors.IsNotFound(err) && targetNamespace != deps.controlNamespace {
		// An organization on the home cluster has no HyveConfig of its own
		// (the console's per-organization settings only exist on a
		// reconciling cluster), so it inherits the install-wide one's
		// default images, pull secrets, and image installs.
		err = mgr.GetAPIReader().Get(context.Background(), apitypes.NamespacedName{Namespace: deps.controlNamespace, Name: deps.configName}, &startupCfg)
	}
	if err != nil {
		if !apierrors.IsNotFound(err) {
			log.Printf("⚠️  [%s] Could not read HyveConfig.spec.defaultWorkflowImage/defaultModuleImage/defaultAgentImage/imagePullSecrets/imageInstalls at startup (%v) — workflow jobs/module operations with no image of their own will fail until this is fixed and the controller restarts", targetNamespace, err)
		}
	} else {
		defaultWorkflowImage = startupCfg.Spec.DefaultWorkflowImage
		defaultModuleImage = startupCfg.Spec.DefaultModuleImage
		defaultAgentImage = startupCfg.Spec.DefaultAgentImage
		imagePullSecrets = startupCfg.Spec.ImagePullSecrets
		imageInstalls = make([]k8sjob.ImageInstall, len(startupCfg.Spec.ImageInstalls))
		for i, ii := range startupCfg.Spec.ImageInstalls {
			imageInstalls[i] = k8sjob.ImageInstall{Image: ii.Image, Install: ii.Install}
		}
	}

	hyveReconciler := reconcile.NewReconciler(stateProvider)
	hyveReconciler.StepRunner = &workflow.KubernetesJobStepRunner{Client: deps.clientset, Namespace: targetNamespace, ImagePullSecrets: imagePullSecrets, ImageInstalls: imageInstalls}
	hyveReconciler.DefaultWorkflowImage = defaultWorkflowImage
	hyveReconciler.ModuleRunner = &module.JobRunner{Client: deps.clientset, Namespace: targetNamespace, ImagePullSecrets: imagePullSecrets, ImageInstalls: imageInstalls}
	hyveReconciler.DefaultModuleImage = defaultModuleImage

	// hyve-agent installation (milestone 4 — see internal/reconcile/agent.go):
	// soft-disabled, not fatal, when --agent-control-plane-url/
	// --agent-tunnel-address are left unset, same "opt-in feature, missing
	// config just disables it" stance as cmd/api/run.go's own AgentCA
	// wiring.
	hyveReconciler.DefaultAgentImage = defaultAgentImage
	hyveReconciler.AgentTokenIssuer = &agentpki.TokenIssuer{Clientset: deps.clientset, ControlPlaneNamespace: deps.controlNamespace}
	hyveReconciler.AgentControlPlaneNamespace = deps.controlNamespace
	hyveReconciler.ClusterNamespace = targetNamespace
	hyveReconciler.AgentControlPlaneURL = deps.agentControlPlaneURL
	hyveReconciler.AgentTunnelAddress = deps.agentTunnelAddress
	hyveReconciler.AgentCACertPEM = deps.agentCACertPEM

	// Host-cluster spec.resources reconciliation (a primary-marked
	// ClusterDefinition with no real spec.driver — see
	// docs/HYVE-AGENT-MIGRATION-GUIDE.md's "Host cluster access" section):
	// mints a token against deps.hostServiceAccount directly against
	// https://kubernetes.default.svc, no /proxy hop needed since this
	// process already runs inside the target cluster.
	hyveReconciler.HostKubeconfigIssuer = &hostKubeconfigIssuer{
		Clientset:              deps.clientset,
		Namespace:              deps.controlNamespace,
		HostServiceAccountName: deps.hostServiceAccount,
		CAPath:                 deps.hostCAPath,
	}

	if deps.homeOrganizations && targetNamespace != deps.controlNamespace {
		hyveReconciler.HostClusters = hostClustersFor(mgr, deps)
	}

	reconciler := &internalcontroller.ClusterDefinitionReconciler{
		Client:                  mgr.GetClient(),
		APIReader:               mgr.GetAPIReader(),
		Reconciler:              hyveReconciler,
		StateProvider:           stateProvider,
		Namespace:               targetNamespace,
		MaxConcurrentReconciles: deps.maxConcurrentReconciles,
	}
	if controllerNamePrefix == "" {
		if err := reconciler.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("set up ClusterDefinition controller: %w", err)
		}
	} else if err := reconciler.SetupWithManagerNamed(mgr, controllerNamePrefix+"-clusterdefinition"); err != nil {
		return fmt.Errorf("set up ClusterDefinition controller: %w", err)
	}

	workflowRunReconciler := &internalcontroller.WorkflowRunReconciler{
		Client:        mgr.GetClient(),
		APIReader:     mgr.GetAPIReader(),
		Reconciler:    hyveReconciler,
		StateProvider: stateProvider,
		Namespace:     targetNamespace,
	}
	if controllerNamePrefix == "" {
		if err := workflowRunReconciler.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("set up WorkflowRun controller: %w", err)
		}
	} else if err := workflowRunReconciler.SetupWithManagerNamed(mgr, controllerNamePrefix+"-workflowrun"); err != nil {
		return fmt.Errorf("set up WorkflowRun controller: %w", err)
	}

	return nil
}

// hostClustersFor backs reconcile.Reconciler.HostClusters for an
// organization on the home cluster: the control plane's access.method:
// primary ClusterDefinitions, and whether the control plane's HyveConfig
// shares them (spec.organizationsMayUseHostCluster). Both are read on every
// call, so toggling the setting needs no restart.
func hostClustersFor(mgr ctrl.Manager, deps reconcilerSharedDeps) func(context.Context) ([]types.ClusterDefinition, bool, error) {
	control := &internalcontroller.CRDStateProvider{
		Client:         mgr.GetClient(),
		Namespace:      deps.controlNamespace,
		ConfigName:     deps.configName,
		ModulesDirPath: deps.modulesDir,
	}
	return func(ctx context.Context) ([]types.ClusterDefinition, bool, error) {
		var cfg hyvev1alpha1.HyveConfig
		err := mgr.GetClient().Get(ctx, apitypes.NamespacedName{Namespace: deps.controlNamespace, Name: deps.configName}, &cfg)
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, false, fmt.Errorf("read HyveConfig %s/%s: %w", deps.controlNamespace, deps.configName, err)
		}
		defs, err := control.LoadClusterDefinitions()
		if err != nil {
			return nil, false, err
		}
		var hosts []types.ClusterDefinition
		for _, d := range defs {
			if d.Spec.AccessMethod == types.AccessMethodPrimary {
				hosts = append(hosts, d)
			}
		}
		return hosts, cfg.Spec.OrganizationsMayUseHostCluster, nil
	}
}
