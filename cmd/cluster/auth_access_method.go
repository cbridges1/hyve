package cluster

import (
	"fmt"
	"log"
	"os"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
)

func requireAccessMethodClusterID(name, accessMethodRef, accessMethodClusterID string) {
	if accessMethodClusterID == "" {
		log.Fatalf("Cluster %q references access method %q but has no accessMethodClusterID set — "+
			"an admin needs to set spec.access.accessMethodClusterID to this cluster's identifier within %q", name, accessMethodRef, accessMethodRef)
	}
}

// runAccessMethodAuthCluster resolves accessMethodRef against a live
// cluster-mode API and mints a kubeconfig for it — the ONLY way an
// AccessMethod resolves now: its driver module's auth operation always
// runs server-side, inside a short-lived Job dispatched by
// POST /api/access-methods/<ref>/mint, never on this machine (see
// HYVE-ACCESS-METHOD-DESIGN.md's "Server-side dispatch" section for why —
// an AccessMethod's whole point is often exactly the case where the
// caller's own machine can't reach the identity service directly, or
// doesn't have its tools installed, so a purely client-side path was never
// going to cover the general case). Returns false (caller falls through
// to its own existing module-auth logic) when accessMethodRef is unset.
func runAccessMethodAuthCluster(name, accessMethodRef, accessMethodClusterID string, client *shared.APIClient) bool {
	if accessMethodRef == "" {
		return false
	}
	requireAccessMethodClusterID(name, accessMethodRef, accessMethodClusterID)

	am, err := client.GetAccessMethod(accessMethodRef)
	if err != nil {
		log.Fatalf("Failed to resolve access method %q: %v", accessMethodRef, err)
	}

	credentialEnv := make(map[string]string, len(am.RequiredEnv))
	for _, envName := range am.RequiredEnv {
		val, ok := os.LookupEnv(envName)
		if !ok || val == "" {
			log.Fatalf("Access method %q's driver module requires %s to be set in your environment", accessMethodRef, envName)
		}
		credentialEnv[envName] = val
	}

	resp, err := client.MintAccessMethodKubeconfig(accessMethodRef, name, accessMethodClusterID, credentialEnv)
	if err != nil {
		log.Fatalf("Failed to mint kubeconfig via access method %q: %v", accessMethodRef, err)
	}

	kcPath, err := mod.KubeconfigPathForCluster(name)
	if err != nil {
		log.Fatalf("Failed to resolve per-cluster kubeconfig path: %v", err)
	}
	if err := os.WriteFile(kcPath, []byte(resp.Kubeconfig), 0600); err != nil {
		log.Fatalf("Failed to write kubeconfig for cluster %q: %v", name, err)
	}

	mergeAuthResultIntoDefaultKubeconfig(name, kcPath)
	fmt.Printf("kubectl context for '%s' configured (via access method '%s')\n", name, accessMethodRef)
	return true
}
