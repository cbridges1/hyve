// Package api implements `hyve cluster-config api run` — nested under
// cmd/clusterconfig, see that package's own doc comment for why — the HTTP
// API + auth layer described in HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's
// Phase 6. Not a
// resurrection of the removed hyve serve: real, independent authz
// (HyveAccessBinding-based) drives it, and it's a thin front door onto the
// ClusterDefinition/HyveAccessBinding CRDs the controller already
// reconciles, not a second implementation of hyve's logic. Cluster mode
// never requires this API — plain kubectl against the CRDs always works.
package api

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	hyveapi "github.com/cbridges1/hyve/internal/api"
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	apiNamespace          string
	apiModulesDir         string
	apiBindAddress        string
	apiPublicBaseURL      string
	apiPrimaryClusterName string
	apiProxyTarget        string
	apiInClusterCAPath    string
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
	runCmd.Flags().StringVar(&apiPublicBaseURL, "public-base-url", "", "This API's own public address (e.g. https://hyve-api.example.com) — required for the primary-cluster kubeconfig path's server: field")
	runCmd.Flags().StringVar(&apiPrimaryClusterName, "primary-cluster-name", "", "ClusterDefinition name that identifies \"this API's own cluster\" for GET /api/kubeconfig — leave unset to disable the primary-cluster path entirely")
	runCmd.Flags().StringVar(&apiProxyTarget, "proxy-target", "https://kubernetes.default.svc", "Upstream /proxy/* forwards to")
	runCmd.Flags().StringVar(&apiInClusterCAPath, "in-cluster-ca-path", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt", "This pod's own in-cluster CA — used both for the primary-cluster kubeconfig's certificate-authority-data and to trust the /proxy upstream")

	Cmd.AddCommand(runCmd)
	Cmd.AddCommand(createUserCmd)
}

func runAPI() {
	if err := hyvev1alpha1.AddToScheme(scheme.Scheme); err != nil {
		log.Fatalf("❌ Failed to register hyve.io/v1alpha1 scheme: %v", err)
	}

	cfg := ctrl.GetConfigOrDie()

	c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		log.Fatalf("❌ Failed to build Kubernetes client: %v", err)
	}

	signingKey, err := hyveapi.LoadSigningKey(context.Background(), c, apiNamespace)
	if err != nil {
		log.Fatalf("❌ %v", err)
	}

	moduleAuthProvider := &hyveapi.ModuleAuthProvider{ModulesDir: apiModulesDir}
	tunnelProvider := &hyveapi.TunnelProvider{Client: c, Namespace: apiNamespace}

	server := &hyveapi.Server{
		Client:             c,
		Namespace:          apiNamespace,
		SigningKey:         signingKey,
		PrimaryClusterName: apiPrimaryClusterName,
		ModuleAuthProvider: moduleAuthProvider,
		TunnelProvider:     tunnelProvider,
	}

	caData, caErr := os.ReadFile(apiInClusterCAPath)
	if caErr != nil {
		log.Printf("⚠️  Could not read in-cluster CA at %s (%v) — the primary-cluster kubeconfig path and /proxy will be unavailable until this runs inside a real pod", apiInClusterCAPath, caErr)
	} else if apiPrimaryClusterName == "" {
		log.Printf("ℹ️  --primary-cluster-name not set — primary-cluster kubeconfig path disabled")
	} else {
		clientset, csErr := kubernetes.NewForConfig(cfg)
		if csErr != nil {
			log.Fatalf("❌ Failed to build Kubernetes clientset: %v", csErr)
		}
		server.PrimaryProvider = &hyveapi.PrimaryClusterProvider{
			Clientset:     clientset,
			CA:            caData,
			PublicBaseURL: apiPublicBaseURL,
		}
		proxy, pErr := hyveapi.BuildProxy(apiProxyTarget, caData)
		if pErr != nil {
			log.Fatalf("❌ Failed to build /proxy handler: %v", pErr)
		}
		server.Proxy = proxy
	}

	log.Printf("🚀 hyve api starting — namespace=%s bind=%s", apiNamespace, apiBindAddress)
	if err := http.ListenAndServe(apiBindAddress, server.Routes()); err != nil {
		log.Fatalf("❌ Server exited with error: %v", err)
	}
}
