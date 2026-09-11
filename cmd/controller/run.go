// Package controller implements `hyve cluster-config controller run` —
// nested under cmd/clusterconfig, see that package's own doc comment for
// why — the long-running reconcile loop against ClusterDefinition/
// HyveConfig CRDs described in
// HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 4. A reconcile loop against
// the Kubernetes API, not an HTTP server — no REST/WebSocket surface here,
// so this doesn't reopen the hyve-serve removal decision.
package controller

import (
	"context"
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/agentpki"
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	internalcontroller "github.com/cbridges1/hyve/internal/controller"
	"github.com/cbridges1/hyve/internal/k8sjob"
	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/reconcile"
	"github.com/cbridges1/hyve/internal/workflow"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	modulesDir              string
	namespace               string
	configName              string
	metricsAddr             string
	probeAddr               string
	leaderElect             bool
	maxConcurrentReconciles int
	agentControlPlaneURL    string
	agentTunnelAddress      string
	agentCAPath             string
	hostServiceAccount      string
	hostCAPath              string
)

// Cmd is the controller command.
var Cmd = &cobra.Command{
	Use:   "controller",
	Short: "Run hyve as a cluster-native controller",
	Long:  "Commands for running hyve's controller/operator mode — a long-running reconcile loop against ClusterDefinition CRDs instead of a git checkout or local directory.",
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the reconcile loop against ClusterDefinition/HyveConfig CRDs",
	Long: `Starts hyve's controller-runtime-based reconcile loop. Every
ClusterDefinition CR in --namespace is driven through the same
internal/reconcile engine the CLI's own 'hyve reconcile' uses — same
module/workflow execution, same drift detection, just backed by Kubernetes
CRDs instead of a git checkout.

--modules-dir must point at a directory already containing a resolved
hyve.lock and every module/workflow it locks — see the Dockerfile this
project ships for how the controller image bakes that in at build time.
This command does not fetch modules over the network at startup.`,
	Run: func(cmd *cobra.Command, args []string) {
		runController()
	},
}

func init() {
	runCmd.Flags().StringVar(&modulesDir, "modules-dir", "/var/lib/hyve/modules", "Directory containing the baked-in hyve.lock and resolved modules/workflows")
	runCmd.Flags().StringVar(&namespace, "namespace", "hyve-system", "Namespace to watch for ClusterDefinition/HyveConfig objects")
	runCmd.Flags().StringVar(&configName, "config-name", "hyve-config", "Name of the singleton HyveConfig object within --namespace")
	runCmd.Flags().StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Address the metrics endpoint binds to")
	runCmd.Flags().StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Address the health/readiness probe endpoint binds to")
	runCmd.Flags().BoolVar(&leaderElect, "leader-elect", false, "Enable leader election for controller manager HA")
	runCmd.Flags().IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", 4, "Maximum ClusterDefinitions reconciled at once — without this, a single stuck cluster (e.g. a workflow step wedged on ImagePullBackOff) blocks every other cluster's reconcile in the namespace")
	runCmd.Flags().StringVar(&agentControlPlaneURL, "agent-control-plane-url", "", "hyve-api's own externally-reachable base URL, for a newly-installed hyve-agent's POST /agent/bootstrap (e.g. https://hyve-api.example.com) — leave unset to disable hyve-agent installation entirely (spec.access.agent.enabled becomes a no-op, logged as a warning)")
	runCmd.Flags().StringVar(&agentTunnelAddress, "agent-tunnel-address", "", "hyve-api's own externally-reachable SSH tunnel listener address, host:port (e.g. hyve-api.example.com:8092) — same disable-if-unset behavior as --agent-control-plane-url")
	runCmd.Flags().StringVar(&agentCAPath, "agent-ca-path", "", "PEM-encoded CA certificate that signed whatever terminates TLS in front of --agent-control-plane-url — embedded as a literal ConfigMap on every remote cluster hyve-agent installs onto (a cross-cluster volume mount isn't possible, unlike --public-ca-path's same-cluster case) so hyve-agent's own POST /agent/bootstrap call trusts it. Leave unset for a publicly-trusted certificate (e.g. a real ACME/Let's Encrypt cert) — see internal/reconcile/agent.go")
	runCmd.Flags().StringVar(&hostServiceAccount, "host-service-account", "hyve-host-admin", "Name of the dedicated ServiceAccount (in --namespace) this controller mints a token against to reconcile spec.resources for a primary-marked ClusterDefinition with no real spec.driver — see internal/reconcile/host.go and deploy/helm/hyve/templates/api-access-roles.yaml. Must match hyve-api's own --host-service-account")
	runCmd.Flags().StringVar(&hostCAPath, "in-cluster-ca-path", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt", "This pod's own in-cluster CA — used for the host-cluster kubeconfig's certificate-authority-data")

	Cmd.AddCommand(runCmd)
}

func runController() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	if _, err := os.Stat(modulesDir); err != nil {
		log.Fatalf("❌ --modules-dir %q not accessible: %v", modulesDir, err)
	}

	if err := hyvev1alpha1.AddToScheme(scheme.Scheme); err != nil {
		log.Fatalf("❌ Failed to register hyve.io/v1alpha1 scheme: %v", err)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme.Scheme,
		// ClusterDefinition/HyveConfig are both namespaced resources and
		// this controller only ever cares about one namespace (--namespace)
		// — scoping the cache's watch/list to just that namespace matches
		// deploy/helm/hyve-controller's own RBAC, which grants a namespaced
		// Role, not a ClusterRole. Left unscoped (the controller-runtime
		// default), the manager's cache tries to list/watch
		// ClusterDefinition cluster-wide and fails outright when run with
		// that Role's real, least-privilege permissions — confirmed live:
		// this exact mismatch crash-looped the controller pod
		// ("cannot list resource \"clusterdefinitions\" ... at the cluster
		// scope") the first time this ran in-cluster with real RBAC rather
		// than a local process's full-access kubeconfig.
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{namespace: {}},
		},
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "hyve-controller-leader",
	})
	if err != nil {
		log.Fatalf("❌ Failed to start manager: %v", err)
	}

	stateProvider := &internalcontroller.CRDStateProvider{
		Client:         mgr.GetClient(),
		Namespace:      namespace,
		ConfigName:     configName,
		ModulesDirPath: modulesDir,
	}

	// A plain client-go clientset — separate from the controller-runtime
	// client above, since KubernetesJobStepRunner works with Jobs/Pods/pod
	// logs, exactly the shape client-go's typed Interface (and its fake for
	// tests) is built for, matching the plan's own "unit test against
	// client-go's fake clientset" instruction.
	clientset, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		log.Fatalf("❌ Failed to build Kubernetes clientset: %v", err)
	}

	// mgr.GetClient() returns a cached client whose cache only starts
	// syncing once mgr.Start() runs — reading through it here, before
	// Start(), would always fail with "the cache is not started" (confirmed
	// live: this exact bug shipped in an earlier version of this file).
	// mgr.GetAPIReader() reads directly from the API server, bypassing the
	// cache entirely, which is exactly what a one-time startup read needs.
	var startupCfg hyvev1alpha1.HyveConfig
	var defaultWorkflowImage, defaultModuleImage, defaultAgentImage string
	var imagePullSecrets []string
	var imageInstalls []k8sjob.ImageInstall
	if err := mgr.GetAPIReader().Get(context.Background(), apitypes.NamespacedName{Namespace: namespace, Name: configName}, &startupCfg); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Printf("⚠️  Could not read HyveConfig.spec.defaultWorkflowImage/defaultModuleImage/defaultAgentImage/imagePullSecrets/imageInstalls at startup (%v) — workflow jobs/module operations with no image of their own will fail until this is fixed and the controller restarts", err)
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
	hyveReconciler.StepRunner = &workflow.KubernetesJobStepRunner{Client: clientset, Namespace: namespace, ImagePullSecrets: imagePullSecrets, ImageInstalls: imageInstalls}
	hyveReconciler.DefaultWorkflowImage = defaultWorkflowImage
	hyveReconciler.ModuleRunner = &module.JobRunner{Client: clientset, Namespace: namespace, ImagePullSecrets: imagePullSecrets, ImageInstalls: imageInstalls}
	hyveReconciler.DefaultModuleImage = defaultModuleImage

	// hyve-agent installation (milestone 4 — see internal/reconcile/agent.go):
	// soft-disabled, not fatal, when --agent-control-plane-url/
	// --agent-tunnel-address are left unset, same "opt-in feature, missing
	// config just disables it" stance as cmd/api/run.go's own AgentCA
	// wiring. TargetNamespace and ControlPlaneNamespace happen to be the
	// same value today (this controller's own --namespace) — see
	// Reconciler.AgentControlPlaneNamespace's own doc comment for why
	// that's not assumed permanent.
	hyveReconciler.DefaultAgentImage = defaultAgentImage
	hyveReconciler.AgentTokenIssuer = &agentpki.TokenIssuer{Clientset: clientset, ControlPlaneNamespace: namespace}
	hyveReconciler.AgentControlPlaneNamespace = namespace
	hyveReconciler.AgentControlPlaneURL = agentControlPlaneURL
	hyveReconciler.AgentTunnelAddress = agentTunnelAddress
	if agentControlPlaneURL == "" || agentTunnelAddress == "" {
		log.Printf("ℹ️  --agent-control-plane-url/--agent-tunnel-address not set — spec.access.agent.enabled will be a no-op on every cluster")
	}

	// Optional — see --agent-ca-path's own doc comment. Empty path means
	// "not configured," not an error: most real deployments use a
	// publicly-trusted certificate and have no CA of their own to embed.
	if agentCAPath != "" {
		agentCA, caErr := os.ReadFile(agentCAPath)
		if caErr != nil {
			log.Fatalf("❌ Failed to read --agent-ca-path %s: %v", agentCAPath, caErr)
		}
		hyveReconciler.AgentCACertPEM = string(agentCA)
	}

	// Host-cluster spec.resources reconciliation (a primary-marked
	// ClusterDefinition with no real spec.driver — see
	// docs/HYVE-AGENT-MIGRATION-GUIDE.md's "Host cluster access" section):
	// mints a token against hostServiceAccount directly against
	// https://kubernetes.default.svc, no /proxy hop needed since this
	// process already runs inside the target cluster.
	hyveReconciler.HostKubeconfigIssuer = &hostKubeconfigIssuer{
		Clientset:              clientset,
		Namespace:              namespace,
		HostServiceAccountName: hostServiceAccount,
		CAPath:                 hostCAPath,
	}

	reconciler := &internalcontroller.ClusterDefinitionReconciler{
		Client:                  mgr.GetClient(),
		APIReader:               mgr.GetAPIReader(),
		Reconciler:              hyveReconciler,
		StateProvider:           stateProvider,
		Namespace:               namespace,
		MaxConcurrentReconciles: maxConcurrentReconciles,
	}

	if err := reconciler.SetupWithManager(mgr); err != nil {
		log.Fatalf("❌ Failed to set up ClusterDefinition controller: %v", err)
	}

	workflowRunReconciler := &internalcontroller.WorkflowRunReconciler{
		Client:        mgr.GetClient(),
		APIReader:     mgr.GetAPIReader(),
		Reconciler:    hyveReconciler,
		StateProvider: stateProvider,
		Namespace:     namespace,
	}
	if err := workflowRunReconciler.SetupWithManager(mgr); err != nil {
		log.Fatalf("❌ Failed to set up WorkflowRun controller: %v", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Fatalf("❌ Failed to set up health check: %v", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Fatalf("❌ Failed to set up readiness check: %v", err)
	}

	log.Printf("🚀 hyve controller starting — namespace=%s modules-dir=%s config=%s", namespace, modulesDir, configName)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatalf("❌ Manager exited with error: %v", err)
	}
}
