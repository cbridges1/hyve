package cluster

import (
	"fmt"
	"log"
	"os"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/kubeconfig"
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
//
// credentialParams is `hyve cluster auth`'s --set KEY=VALUE flags — an
// explicit way to supply a required credential value without needing it
// already set in the caller's shell environment (useful for scripting/CI,
// where exporting env vars ahead of time is often more awkward than
// passing them inline). Takes precedence over the same name found in the
// environment; the environment is still consulted as a fallback, not
// removed as an option.
func runAccessMethodAuthCluster(name, accessMethodRef, accessMethodClusterID string, client *shared.APIClient, credentialParams map[string]string) bool {
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
		if val, ok := credentialParams[envName]; ok && val != "" {
			credentialEnv[envName] = val
			continue
		}
		val, ok := os.LookupEnv(envName)
		if !ok || val == "" {
			log.Fatalf("Access method %q's driver module requires %s — pass it via --set %s=<value> or set it in your environment", accessMethodRef, envName, envName)
		}
		credentialEnv[envName] = val
	}

	resp, err := client.MintAccessMethodKubeconfig(accessMethodRef, name, accessMethodClusterID, credentialEnv)
	if err != nil {
		log.Fatalf("Failed to mint kubeconfig via access method %q: %v", accessMethodRef, err)
	}

	// Merged directly, no ~/.hyve/kubeconfigs/<name>.yaml staging file —
	// unlike the module-auth paths, nothing local ever ran a script that
	// needed somewhere concrete to write to; the kubeconfig arrives
	// pre-formed as bytes in the mint response, same shape as the
	// server-minted GetKubeconfig path (authClusterAPI's tunnel/
	// module-auth-override branch), which already skips the staging file
	// for the same reason.
	kcPath, err := mod.DefaultKubeconfigPath()
	if err != nil {
		log.Fatalf("Failed to resolve local kubeconfig path: %v", err)
	}
	if err := kubeconfig.MergeKubeconfigEntry(kcPath, []byte(resp.Kubeconfig), name); err != nil {
		log.Fatalf("Failed to merge kubeconfig: %v", err)
	}

	fmt.Printf("kubectl context for '%s' configured (via access method '%s')\n", name, accessMethodRef)
	return true
}
