// Package api implements `hyve cluster-config api run` — nested under
// cmd/clusterconfig, see that package's own doc comment for why — the HTTP
// API + auth layer described in HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's
// Phase 6. Not a
// resurrection of the removed hyve serve: real, independent authz (backed
// by internal/orgdb's Postgres/SQLite Binding rows, not a Kubernetes CRD,
// since HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md's Milestone 4)
// drives it, and it's a thin front door onto the ClusterDefinition CRD the
// controller already reconciles, not a second implementation of hyve's
// logic. Cluster mode never requires this API — plain kubectl against the
// CRDs always works.
package api

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/agentpki"
	hyveapi "github.com/cbridges1/hyve/internal/api"
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/orgdb"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// organizationDeletionSweepInterval is how often SweepPendingOrganizationDeletions
// runs in the background — short enough that a caller deleting an
// already-empty (or quick-to-terminate) organization sees its row actually
// disappear soon after, without needing to wait on handleDeleteOrganization's
// own opportunistic same-request check to have caught it.
const organizationDeletionSweepInterval = 30 * time.Second

// reconcilingClusterHealthSweepInterval is how often
// SweepReconcilingClusterHealth runs — longer than the deletion sweep
// since a reachability check is a real network round trip per registered
// cluster (potentially many), not a handful of cheap Namespace Gets, and
// "is this cluster up" doesn't need second-to-second freshness the way a
// caller waiting on their own delete does.
const reconcilingClusterHealthSweepInterval = 2 * time.Minute

var (
	apiNamespace          string
	apiModulesDir         string
	apiBindAddress        string
	apiPublicBaseURL      string
	apiProxyTarget        string
	apiInClusterCAPath    string
	apiHostServiceAccount string
	apiAgentBindAddress   string
	apiPublicCAPath       string
	apiConfigName         string
	apiDBDriver           string
	apiDBDSN              string
	apiHomeCluster        string
)

// Cmd is the api command.
var Cmd = &cobra.Command{
	Use:   "api",
	Short: "Run hyve's HTTP API + auth layer, or manage its local users",
	Long:  "Commands for hyve's HTTP API + auth layer — a convenience layer in front of the ClusterDefinition/HyveAccessBinding CRDs, not a required gateway.",
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the HTTP API + auth layer",
	Long: `Starts hyve's API server: local (username/password) login, role-gated
ClusterDefinition CRUD, and GET /api/kubeconfig kubeconfig minting for any
managed cluster. See HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 6.

Requires a hyve-api-credentials Secret (session-signing-key) in
--namespace before it will start — see internal/api.LoadSigningKey's doc
comment for how to create one.`,
	Run: func(cmd *cobra.Command, args []string) {
		runAPI()
	},
}

func init() {
	runCmd.Flags().StringVar(&apiNamespace, "namespace", "hyve-system", "Namespace ClusterDefinitions/HyveAccessBindings/credentials Secrets live in")
	runCmd.Flags().StringVar(&apiModulesDir, "modules-dir", "/var/lib/hyve/modules", "Directory containing the baked-in hyve.lock and resolved modules — see cmd/controller's --modules-dir")
	runCmd.Flags().StringVar(&apiBindAddress, "bind-address", ":8090", "Address the API binds to")
	runCmd.Flags().StringVar(&apiPublicBaseURL, "public-base-url", "", "This API's own public address (e.g. https://hyve-api.example.com) — required for the host-cluster and agent-proxy kubeconfig paths' server: fields")
	runCmd.Flags().StringVar(&apiProxyTarget, "proxy-target", "https://kubernetes.default.svc", "Upstream /proxy/* forwards to")
	runCmd.Flags().StringVar(&apiInClusterCAPath, "in-cluster-ca-path", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt", "This pod's own in-cluster CA — used both for the host-cluster kubeconfig's certificate-authority-data and to trust the /proxy upstream")
	runCmd.Flags().StringVar(&apiHostServiceAccount, "host-service-account", "hyve-host-admin", "Name of the dedicated ServiceAccount (in --namespace) a superadmin's host-cluster kubeconfig (access.method: primary, no real spec.driver) mints a token against — see deploy/helm/hyve/templates/api-access-roles.yaml")
	runCmd.Flags().StringVar(&apiAgentBindAddress, "agent-bind-address", ":8092", "Address hyve-agent's own SSH tunnel listener binds to — see internal/api.Server.ServeAgentTunnel")
	runCmd.Flags().StringVar(&apiPublicCAPath, "public-ca-path", "", "PEM-encoded CA certificate that signed whatever terminates TLS in front of --public-base-url (an Ingress, a LoadBalancer, ...) — embedded into every agent-proxy kubeconfig's certificate-authority-data so callers trust it without needing it in their own system trust store. Leave unset for a publicly-trusted certificate (e.g. a real ACME/Let's Encrypt cert) — see internal/api.AgentProvider.PublicCA")
	runCmd.Flags().StringVar(&apiConfigName, "config-name", "hyve-config", "Name of the singleton HyveConfig object within --namespace (GET/PATCH /api/config) — must match cmd/controller's own --config-name")
	runCmd.Flags().StringVar(&apiDBDriver, "db", "sqlite", "Backend for hyve-api's own organization/environment/RBAC datastore — 'sqlite' (default, single API replica only) or 'postgres' (required for horizontal API scaling or any use of per-organization reconciling clusters — see internal/orgdb and HYVE-ORGANIZATION-MODEL-PROPOSAL.md's 'Deployment strategy' section)")
	runCmd.Flags().StringVar(&apiDBDSN, "db-dsn", "/data/orgdb.sqlite", "Data source name for --db: a file path for sqlite, a standard connection string (e.g. postgres://user:pass@host:5432/dbname) for postgres")
	runCmd.Flags().StringVar(&apiHomeCluster, "home-cluster", "required", "Whether this process needs a Kubernetes cluster of its own (Milestone 10 Part C) — 'required' (default, matches every pre-Milestone-10 install's behavior: in-cluster config or --kubeconfig must resolve, Fatal if not) or 'none' (skip Kubernetes client construction entirely; every organization's resources must be reachable through a registered reconciling cluster instead — see HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md's Milestone 10 Part C, nexus-config/docs). Features that are inherently home-cluster-only (GET/PATCH /api/config, the host-cluster kubeconfig path, /proxy) become unavailable under 'none', same soft-fail stance those already have for other missing prerequisites.")

	Cmd.AddCommand(runCmd)
	Cmd.AddCommand(createUserCmd)
}

func runAPI() {
	if err := hyvev1alpha1.AddToScheme(scheme.Scheme); err != nil {
		log.Fatalf("❌ Failed to register hyve.io/v1alpha1 scheme: %v", err)
	}

	if apiHomeCluster != "required" && apiHomeCluster != "none" {
		log.Fatalf("❌ --home-cluster must be \"required\" or \"none\", got %q", apiHomeCluster)
	}

	// Opened before any Kubernetes client construction below (Milestone 10
	// Part C — HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md, nexus-config/docs):
	// organization/environment/RBAC data isn't an opt-in capability the way
	// hyve-agent is, and — as of Part C — neither is hyve-api's own signing
	// key or any local account's password hash, both of which now live here
	// too (see EnsureSigningKey). Fatal on failure, unlike AgentCA/
	// agentpki's soft-fail stance further below.
	orgStore, dbErr := orgdb.Open(apiDBDriver, apiDBDSN)
	if dbErr != nil {
		log.Fatalf("❌ Failed to open --db=%s organization datastore at %q: %v", apiDBDriver, apiDBDSN, dbErr)
	}

	signingKey, skErr := hyveapi.EnsureSigningKey(context.Background(), orgStore, apiNamespace)
	if skErr != nil {
		log.Fatalf("❌ Failed to load/generate session-signing key: %v", skErr)
	}

	// This process's own Kubernetes client/clientset — genuinely optional
	// as of Milestone 10 Part C (see Server.Client's own doc comment and
	// --home-cluster's flag help above). "required" (the default) matches
	// every pre-Milestone-10 install's behavior exactly: an unresolvable
	// config is still Fatal, not a silent downgrade — deliberately not
	// "auto-detect and soft-fail," since that would turn a genuine
	// misconfiguration on an install that expects a home cluster into a
	// silently reduced-capability start instead of the clear failure an
	// operator needs to see.
	var c client.Client
	var clientset kubernetes.Interface
	if apiHomeCluster == "none" {
		log.Printf("ℹ️  --home-cluster=none: this process has no Kubernetes client of its own — every organization's resources must be reachable through a registered reconciling cluster")
	} else {
		cfg := ctrl.GetConfigOrDie()

		var err error
		c, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
		if err != nil {
			log.Fatalf("❌ Failed to build Kubernetes client: %v", err)
		}

		// Needed for agent bootstrap token validation (agentpki), the agent
		// tunnel CA, and raw Events() reads/writes — see Server.Clientset's
		// own doc comment.
		clientset, err = kubernetes.NewForConfig(cfg)
		if err != nil {
			log.Fatalf("❌ Failed to build Kubernetes clientset: %v", err)
		}
	}

	moduleAuthProvider := &hyveapi.ModuleAuthProvider{ModulesDir: apiModulesDir}
	// TunnelProvider needs a home-cluster client — left nil under
	// --home-cluster=none, same as every other home-cluster-only provider
	// below; handleKubeconfig's own dispatch already treats a nil provider
	// as "unavailable" (503-equivalent), not a panic.
	var tunnelProvider hyveapi.AccessProvider
	if c != nil {
		tunnelProvider = &hyveapi.TunnelProvider{Client: c, Namespace: apiNamespace}
	}

	// Optional — see --public-ca-path's own doc comment. Empty path means
	// "not configured," not an error: most real deployments use a
	// publicly-trusted certificate and have no CA of their own to embed.
	var publicCA []byte
	if apiPublicCAPath != "" {
		var caErr error
		publicCA, caErr = os.ReadFile(apiPublicCAPath)
		if caErr != nil {
			log.Fatalf("❌ Failed to read --public-ca-path %s: %v", apiPublicCAPath, caErr)
		}
	}

	// The SQLite/Postgres deployment gate becomes real here (Milestone 6 —
	// see HYVE-ORGANIZATION-MODEL-PROPOSAL.md's own deployment-strategy
	// section, nexus-config/docs): a single-file SQLite database has no
	// story for the concurrent, cross-process access Milestone 6's
	// reconciling-cluster migration (PATCH /organizations/{name}) assumes
	// once any organization actually lives on a cluster other than this
	// process's own home cluster. Checked once at startup, not per
	// request — an org can only reach that state through this same API, so
	// a fresh `--db=sqlite` process either already has one or doesn't; it
	// can't newly acquire one without going through this same startup path
	// again on its next restart.
	if apiDBDriver == "sqlite" {
		anyMigrated, checkErr := orgStore.AnyOrganizationHasReconcilingCluster(context.Background())
		if checkErr != nil {
			log.Fatalf("❌ Failed to check for organizations on a non-home reconciling cluster: %v", checkErr)
		}
		if anyMigrated {
			log.Fatalf("❌ --db=sqlite refused: at least one organization has been moved to a reconciling cluster other than this install's own home cluster (Milestone 6) — switch to --db=postgres before restarting, see HYVE-ORGANIZATION-MODEL-PROPOSAL.md's deployment-strategy section")
		}
	}

	// Milestone 10 Part A (HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md,
	// nexus-config/docs) — see ensureControlPlaneOrganization's own doc
	// comment for what/why.
	if seeded, err := ensureControlPlaneOrganization(context.Background(), orgStore, apiNamespace); err != nil {
		log.Fatalf("❌ Failed to seed control-plane organization %q: %v", apiNamespace, err)
	} else if seeded {
		log.Printf("ℹ️  Seeded control-plane organization %q (Milestone 10 Part A)", apiNamespace)
	}

	server := &hyveapi.Server{
		Client:             c,
		Namespace:          apiNamespace,
		SigningKey:         signingKey,
		ModuleAuthProvider: moduleAuthProvider,
		TunnelProvider:     tunnelProvider,
		AgentProvider:      &hyveapi.AgentProvider{PublicBaseURL: apiPublicBaseURL, PublicCA: publicCA},
		ModulesDir:         apiModulesDir,
		Clientset:          clientset,
		ConfigName:         apiConfigName,
		OrgStore:           orgStore,
	}

	// Soft-fail, not Fatal: hyve-agent (docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md)
	// is an opt-in capability — an install that never uses it shouldn't be
	// unable to start API just because CA bootstrap hit a transient
	// problem. Server.AgentCA's own doc comment covers the left-nil case
	// (POST /agent/bootstrap 500s with a clear message instead). clientset
	// == nil (--home-cluster=none, Milestone 10 Part C) gets the identical
	// treatment — nothing here needs a home cluster of its own to be
	// available, so this is folded into the same soft-fail branch rather
	// than a separate check.
	if clientset == nil {
		log.Printf("⚠️  No home cluster (--home-cluster=none) — POST /agent/bootstrap will be unavailable")
	} else if agentCA, agentCAErr := agentpki.LoadOrCreateCA(context.Background(), clientset, apiNamespace); agentCAErr != nil {
		log.Printf("⚠️  Could not load/create hyve-agent's internal CA (%v) — POST /agent/bootstrap will be unavailable", agentCAErr)
	} else {
		server.AgentCA = agentCA
		server.AgentRegistry = hyveapi.NewAgentRegistry()
	}

	if clientset == nil {
		log.Printf("⚠️  No home cluster (--home-cluster=none) — the host-cluster kubeconfig path and /proxy will be unavailable")
	} else if caData, caErr := os.ReadFile(apiInClusterCAPath); caErr != nil {
		log.Printf("⚠️  Could not read in-cluster CA at %s (%v) — the host-cluster kubeconfig path and /proxy will be unavailable until this runs inside a real pod", apiInClusterCAPath, caErr)
	} else {
		server.HostProvider = &hyveapi.HostProvider{
			Clientset:             clientset,
			CA:                    caData,
			PublicBaseURL:         apiPublicBaseURL,
			HostServiceAccountRef: hyvev1alpha1.ServiceAccountRef{Namespace: apiNamespace, Name: apiHostServiceAccount},
		}
		proxy, pErr := hyveapi.BuildProxy(apiProxyTarget, caData)
		if pErr != nil {
			log.Fatalf("❌ Failed to build /proxy handler: %v", pErr)
		}
		server.Proxy = proxy
	}

	// Milestone 5's organization-deletion sweep — the periodic half of
	// Server.SweepPendingOrganizationDeletions' "check on next relevant
	// request, or a periodic sweep" design (see that method's own doc
	// comment). Its own goroutine, like the agent tunnel listener below:
	// logs are already handled inside the sweep itself, nothing here can
	// fail startup.
	go func() {
		ticker := time.NewTicker(organizationDeletionSweepInterval)
		defer ticker.Stop()
		for range ticker.C {
			server.SweepPendingOrganizationDeletions(context.Background())
		}
	}()

	// Milestone 6's reconciling-cluster health check — the periodic
	// discovery-call probe behind reconcilingClusterDTO's own
	// reachable/lastCheckedAt/lastError fields (see
	// Server.SweepReconcilingClusterHealth's own doc comment). Same
	// standing-goroutine shape as the deletion sweep just above.
	go func() {
		ticker := time.NewTicker(reconcilingClusterHealthSweepInterval)
		defer ticker.Stop()
		for range ticker.C {
			server.SweepReconcilingClusterHealth(context.Background())
		}
	}()

	// The agent tunnel listener is a raw TCP+SSH listener, not an
	// http.Handler — see Server.ServeAgentTunnel's own doc comment. Its
	// own goroutine, logs rather than kills the whole API on error;
	// skipped entirely if AgentCA/AgentRegistry never got configured (the
	// soft-fail branch above already logged why).
	if server.AgentCA != nil && server.AgentRegistry != nil {
		go func() {
			log.Printf("🚀 hyve agent tunnel listener starting — bind=%s", apiAgentBindAddress)
			if err := server.ServeAgentTunnel(context.Background(), apiAgentBindAddress); err != nil {
				log.Printf("❌ Agent tunnel listener exited with error: %v", err)
			}
		}()
	}

	log.Printf("🚀 hyve api starting — namespace=%s bind=%s", apiNamespace, apiBindAddress)
	if err := http.ListenAndServe(apiBindAddress, server.Routes()); err != nil {
		log.Fatalf("❌ Server exited with error: %v", err)
	}
}
