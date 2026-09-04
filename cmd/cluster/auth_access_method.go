package cluster

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/cbridges1/hyve/cmd/shared"
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/kubeconfig"
	mod "github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/rancher"
)

// accessMethodResolver returns (provider, serverURL) for a named
// AccessMethod — local mode resolves it via internal/accessmethod.Manager
// reading a local file, cluster mode via GET /api/access-methods/<ref>
// (see runClusterAuth/authClusterAPI). Both are read-only lookups; neither
// ever touches /api/kubeconfig or the existing AccessProvider dispatch at
// all — see HYVE-ACCESS-METHOD-DESIGN.md.
type accessMethodResolver func(ref string) (provider, serverURL string, err error)

// runAccessMethodAuth is the entry point both runClusterAuth (local mode)
// and authClusterAPI (cluster mode) call before falling through to their
// existing module-auth paths. Reports false when the cluster doesn't use
// this path at all (accessMethodRef unset) — the caller should continue
// with its own logic. A resolution or mint failure exits the process
// directly (log.Fatalf), matching every other fatal error path in this
// command.
func runAccessMethodAuth(name, accessMethodRef, accessMethodClusterID string, resolve accessMethodResolver) bool {
	if accessMethodRef == "" {
		return false
	}

	provider, serverURL, err := resolve(accessMethodRef)
	if err != nil {
		log.Fatalf("Failed to resolve access method %q: %v", accessMethodRef, err)
	}
	if accessMethodClusterID == "" {
		log.Fatalf("Cluster %q references access method %q but has no accessMethodClusterID set — "+
			"an admin needs to set spec.access.accessMethodClusterID to this cluster's identifier within %q", name, accessMethodRef, accessMethodRef)
	}

	switch provider {
	case hyvev1alpha1.AccessMethodProviderRancher:
		mintViaRancher(name, serverURL, accessMethodClusterID)
	case hyvev1alpha1.AccessMethodProviderTeleport:
		log.Fatalf("Access method %q uses provider %q — not yet implemented (see HYVE-ACCESS-METHOD-DESIGN.md: "+
			"a native Teleport client is deferred, needs a spike before it's trustworthy)", accessMethodRef, provider)
	default:
		log.Fatalf("Access method %q uses provider %q, which hyve doesn't support", accessMethodRef, provider)
	}
	return true
}

// mintViaRancher runs entirely client-side — no hyve API involvement in
// the credential exchange at all, per the design's "Rancher directly
// reachable from the user's machine" case. Reuses an already-held
// RANCHER_TOKEN (env var) when set, skipping the login prompt entirely;
// otherwise prompts for the user's own Rancher credentials — never a
// shared hyve service credential, see the design doc's rationale.
func mintViaRancher(clusterName, serverURL, rancherClusterID string) {
	client := rancher.New(serverURL)
	ctx := context.Background()

	token := os.Getenv("RANCHER_TOKEN")
	if token == "" {
		username, err := shared.PromptLine("Rancher username: ")
		if err != nil {
			log.Fatalf("Failed to read username: %v", err)
		}
		password, err := shared.PromptSecret("Rancher password: ")
		if err != nil {
			log.Fatalf("Failed to read password: %v", err)
		}
		token, err = client.Login(ctx, username, password)
		if err != nil {
			log.Fatalf("Rancher login failed: %v", err)
		}
	}

	kc, err := client.GenerateKubeconfig(ctx, token, rancherClusterID)
	if err != nil {
		log.Fatalf("Failed to generate kubeconfig via Rancher: %v", err)
	}

	kcPath, err := mod.DefaultKubeconfigPath()
	if err != nil {
		log.Fatalf("Failed to resolve local kubeconfig path: %v", err)
	}
	if err := kubeconfig.MergeKubeconfigEntry(kcPath, kc, clusterName); err != nil {
		log.Fatalf("Failed to merge kubeconfig: %v", err)
	}

	fmt.Printf("kubectl context for '%s' configured (via Rancher, %s)\n", clusterName, serverURL)
}
