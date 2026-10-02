package controller

import (
	"context"
	"fmt"
	"log"

	"github.com/cbridges1/hyve/internal/agentpki"
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	internalcontroller "github.com/cbridges1/hyve/internal/controller"
	"github.com/cbridges1/hyve/internal/k8sjob"
	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/orgdb"
	"github.com/cbridges1/hyve/internal/reconcile"
	"github.com/cbridges1/hyve/internal/workflow"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
)

// resolveTargetNamespaces answers "which namespace(s) should this
// controller process reconcile" — Phase 1's default (reconcilingClusterID
// == "") is always exactly []string{currentNamespace}, orgStore unused
// (nil is fine, and expected, in this case — see cmd/controller/run.go's
// own call site, which only opens a Store at all when reconcilingClusterID
// is set). Set, this queries orgStore (Milestone 6's one deliberate,
// narrow exception to "the controller never touches Store" — see
// HYVE-ORGANIZATION-MODEL-PROPOSAL.md, nexus-config/docs) for every
// organization mapped to that id and returns their namespaces instead — an
// empty, non-nil slice (not an error) when none are mapped yet, matching
// this command's own "reconcile nothing until one is" stance rather than
// failing to start.
func resolveTargetNamespaces(ctx context.Context, orgStore *orgdb.Store, currentNamespace, reconcilingClusterID string) ([]string, error) {
	if reconcilingClusterID == "" {
		return []string{currentNamespace}, nil
	}
	orgs, err := orgStore.ListOrganizationsByReconcilingCluster(ctx, &reconcilingClusterID)
	if err != nil {
		return nil, err
	}
	namespaces := make([]string, len(orgs))
	for i, org := range orgs {
		namespaces[i] = org.Namespace
	}
	return namespaces, nil
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
	if err := mgr.GetAPIReader().Get(context.Background(), apitypes.NamespacedName{Namespace: targetNamespace, Name: deps.configName}, &startupCfg); err != nil {
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
