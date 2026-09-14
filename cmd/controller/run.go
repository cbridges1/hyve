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

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	internalcontroller "github.com/cbridges1/hyve/internal/controller"
	"github.com/cbridges1/hyve/internal/orgdb"

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
	reconcilingClusterID    string
	controllerDBDriver      string
	controllerDBDSN         string
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
	runCmd.Flags().StringVar(&reconcilingClusterID, "reconciling-cluster-id", "", "Id of the internal/orgdb.ReconcilingCluster this process reconciles. Leave unset for the default single-namespace mode (--namespace alone). When set, --db/--db-dsn must point at the same organization datastore hyve-api uses — this process queries it once at startup (read-only) for every organization mapped to this id, and reconciles exactly those namespaces, one full reconciler instance per namespace, all on this one process/manager")
	runCmd.Flags().StringVar(&controllerDBDriver, "db", "sqlite", "Organization datastore driver — only read when --reconciling-cluster-id is set (\"sqlite\" or \"postgres\", matching hyve-api's own --db)")
	runCmd.Flags().StringVar(&controllerDBDSN, "db-dsn", "/data/orgdb.sqlite", "Organization datastore DSN — only read when --reconciling-cluster-id is set. Must resolve to the SAME datastore hyve-api itself uses; a SQLite DSN only works here if this process can reach that same file, which in practice means --db=postgres is required for any real deployment using this flag (see cmd/api/run.go's own SQLite/Postgres deployment gate)")

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

	// Milestone 6: resolve which namespace(s) this process reconciles (see
	// HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "Per-organization reconciling
	// cluster" section, nexus-config/docs). --reconciling-cluster-id unset
	// (the default) is exactly Phase 1's original single-namespace
	// behavior — one namespace, from --namespace, one reconciler instance
	// each of ClusterDefinition/WorkflowRun, unchanged. Set, this process
	// instead makes one read-only query against the organization datastore
	// — the proposal's own deliberate, narrow exception to "the controller
	// never touches Store" — for every organization mapped to that id, and
	// reconciles all of their namespaces instead, one full reconciler
	// instance per namespace (see setupNamespaceReconcilers).
	multiInstance := reconcilingClusterID != ""
	var orgStore *orgdb.Store
	if multiInstance {
		var dbErr error
		orgStore, dbErr = orgdb.Open(controllerDBDriver, controllerDBDSN)
		if dbErr != nil {
			log.Fatalf("❌ Failed to open --db=%s organization datastore at %q: %v", controllerDBDriver, controllerDBDSN, dbErr)
		}
		defer orgStore.Close()
	}
	targetNamespaces, resolveErr := resolveTargetNamespaces(context.Background(), orgStore, namespace, reconcilingClusterID)
	if resolveErr != nil {
		log.Fatalf("❌ Failed to list organizations for reconciling cluster %q: %v", reconcilingClusterID, resolveErr)
	}
	if multiInstance && len(targetNamespaces) == 0 {
		log.Printf("⚠️  No organizations are currently mapped to reconciling cluster %q — this process will reconcile nothing until one is", reconcilingClusterID)
	}

	cacheNamespaces := make(map[string]cache.Config, len(targetNamespaces))
	for _, ns := range targetNamespaces {
		cacheNamespaces[ns] = cache.Config{}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme.Scheme,
		// ClusterDefinition/HyveConfig are both namespaced resources, and
		// this controller only ever cares about targetNamespaces (Phase 1:
		// just --namespace; Milestone 6: every organization namespace
		// mapped to --reconciling-cluster-id, resolved just above) — scoping
		// the cache's watch/list to exactly that set matches
		// deploy/helm/hyve-controller's own RBAC, which grants a namespaced
		// Role, not a ClusterRole (plus a narrow read-only ClusterRole for
		// the handful of cluster-wide needs — see that chart's own comment).
		// Left unscoped (the controller-runtime default), the manager's
		// cache tries to list/watch ClusterDefinition cluster-wide and
		// fails outright when run with that Role's real, least-privilege
		// permissions — confirmed live: this exact mismatch crash-looped
		// the controller pod ("cannot list resource \"clusterdefinitions\"
		// ... at the cluster scope") the first time this ran in-cluster
		// with real RBAC rather than a local process's full-access
		// kubeconfig.
		Cache: cache.Options{
			DefaultNamespaces: cacheNamespaces,
		},
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "hyve-controller-leader",
	})
	if err != nil {
		log.Fatalf("❌ Failed to start manager: %v", err)
	}

	// A plain client-go clientset — separate from the controller-runtime
	// client above, since KubernetesJobStepRunner works with Jobs/Pods/pod
	// logs, exactly the shape client-go's typed Interface (and its fake for
	// tests) is built for, matching the plan's own "unit test against
	// client-go's fake clientset" instruction. Process-level, shared across
	// every namespace's own reconciler instances below — building a second
	// one per namespace would buy nothing, it's the same rest.Config either
	// way.
	clientset, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		log.Fatalf("❌ Failed to build Kubernetes clientset: %v", err)
	}

	// Optional — see --agent-ca-path's own doc comment. Empty path means
	// "not configured," not an error: most real deployments use a
	// publicly-trusted certificate and have no CA of their own to embed.
	// Read once, shared: this is PEM bytes describing how to trust whatever
	// terminates TLS in front of --agent-control-plane-url, not
	// namespace-scoped data.
	var agentCACertPEM string
	if agentCAPath != "" {
		agentCA, caErr := os.ReadFile(agentCAPath)
		if caErr != nil {
			log.Fatalf("❌ Failed to read --agent-ca-path %s: %v", agentCAPath, caErr)
		}
		agentCACertPEM = string(agentCA)
	}
	if agentControlPlaneURL == "" || agentTunnelAddress == "" {
		log.Printf("ℹ️  --agent-control-plane-url/--agent-tunnel-address not set — spec.access.agent.enabled will be a no-op on every cluster")
	}

	deps := reconcilerSharedDeps{
		clientset:               clientset,
		configName:              configName,
		modulesDir:              modulesDir,
		maxConcurrentReconciles: maxConcurrentReconciles,
		agentControlPlaneURL:    agentControlPlaneURL,
		agentTunnelAddress:      agentTunnelAddress,
		agentCACertPEM:          agentCACertPEM,
		// hostServiceAccount/hostCAPath/AgentTokenIssuer's own
		// ControlPlaneNamespace/AgentControlPlaneNamespace all stay tied to
		// this single, shared --namespace — see setupNamespaceReconcilers'
		// own doc comment for why these represent this control-plane
		// process's own identity (which ServiceAccount it mints host/agent
		// tokens against), not per-organization data, and so are
		// deliberately NOT looped per target namespace the way
		// StateProvider/StepRunner/ModuleRunner are.
		controlNamespace:   namespace,
		hostServiceAccount: hostServiceAccount,
		hostCAPath:         hostCAPath,
	}

	// One full reconciler instance per target namespace — see
	// setupNamespaceReconcilers' own doc comment for exactly what that
	// means and why Phase 1's single-namespace case (multiInstance false)
	// preserves today's exact controller names/behavior unchanged.
	for _, ns := range targetNamespaces {
		namePrefix := ""
		if multiInstance {
			namePrefix = "org-" + ns
		}
		if err := setupNamespaceReconcilers(mgr, ns, namePrefix, deps); err != nil {
			log.Fatalf("❌ Failed to set up reconcilers for namespace %q: %v", ns, err)
		}
	}

	// Milestone 5's organization-deletion design (see
	// HYVE-ORGANIZATION-MODEL-PROPOSAL.md, nexus-config/docs) — clears
	// hyvev1alpha1.OrganizationNamespaceFinalizer once every hyve-owned
	// object in a Terminating organization Namespace is confirmed gone.
	namespaceReconciler := &internalcontroller.NamespaceReconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
	}
	if err := namespaceReconciler.SetupWithManager(mgr); err != nil {
		log.Fatalf("❌ Failed to set up Namespace controller: %v", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Fatalf("❌ Failed to set up health check: %v", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Fatalf("❌ Failed to set up readiness check: %v", err)
	}

	log.Printf("🚀 hyve controller starting — namespaces=%v modules-dir=%s config=%s reconciling-cluster-id=%q", targetNamespaces, modulesDir, configName, reconcilingClusterID)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatalf("❌ Manager exited with error: %v", err)
	}
}
