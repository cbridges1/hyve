// Command hyve-agent is the standing, per-cluster workload described in
// docs/HYVE-AGENT-ARCHITECTURE-PROPOSAL.md — deliberately its own
// standalone binary (plain flag/env config, no cobra command tree), not a
// subcommand nested under the main `hyve` CLI the way cmd/api/cmd/
// controller currently are. That nesting is itself something
// docs/HYVE-ORGANIZATION-MODEL-PROPOSAL.md's own "cmd/api/cmd/controller
// come out of the CLI's command tree" section calls out as inconsistent
// with this binary's own shape — hyve-agent is the precedent that change
// is modeled on, not an exception to it.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/cbridges1/hyve/internal/agent"

	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
)

// version is overridable at build time via -ldflags
// "-X main.version=...", matching this repo's own established convention
// for the main hyve binary. Left as "dev" for a plain `go run`/`go build`
// — reported as-is in ClusterDefinitionStatus.Agent.Version, so a locally
// built agent is honestly distinguishable from a real release rather than
// silently claiming to be one.
var version = "dev"

func main() {
	var (
		controlPlaneURL = flag.String("control-plane-url", envOr("HYVE_CONTROL_PLANE_URL", ""), "hyve-api's own base URL, for POST /agent/bootstrap (env HYVE_CONTROL_PLANE_URL)")
		tunnelAddress   = flag.String("tunnel-address", envOr("HYVE_TUNNEL_ADDRESS", ""), "hyve-api's own SSH tunnel listener, host:port (env HYVE_TUNNEL_ADDRESS)")
		namespace       = flag.String("namespace", envOr("HYVE_NAMESPACE", ""), "this agent's own namespace, for persisting its bootstrapped identity (env HYVE_NAMESPACE)")
		clusterName     = flag.String("cluster-name", envOr("HYVE_CLUSTER_NAME", ""), "this ClusterDefinition's own name, matching what the control plane signed a certificate for (env HYVE_CLUSTER_NAME)")
		bootstrapToken  = flag.String("bootstrap-token", envOr("HYVE_BOOTSTRAP_TOKEN", ""), "single-use bootstrap token — only needed until identity is first persisted (env HYVE_BOOTSTRAP_TOKEN)")
	)
	flag.Parse()

	if *controlPlaneURL == "" || *tunnelAddress == "" || *namespace == "" {
		fmt.Fprintln(os.Stderr, "hyve-agent: --control-plane-url, --tunnel-address, and --namespace are all required")
		os.Exit(2)
	}
	// clusterName is only actually used for logging here — the identity
	// that matters is whichever (namespace, clusterName) the control
	// plane itself encoded into the certificate at bootstrap time (see
	// internal/agentpki.SignAgentUserCertificate); this flag existing at
	// all is a convenience for operators reading this process's own logs,
	// not a value this binary asserts trust in.
	_ = clusterName

	ctx := ctrl.SetupSignalHandler()

	cfg, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("hyve-agent: failed to load Kubernetes config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("hyve-agent: failed to build Kubernetes clientset: %v", err)
	}

	identity, err := agent.LoadOrBootstrapIdentity(ctx, clientset, *namespace, *controlPlaneURL, *bootstrapToken)
	if err != nil {
		log.Fatalf("hyve-agent: failed to load or bootstrap identity: %v", err)
	}

	log.Printf("hyve-agent %s starting — cluster=%s tunnel=%s", version, *clusterName, *tunnelAddress)
	if err := agent.Run(ctx, agent.Config{
		TunnelAddress: *tunnelAddress,
		Identity:      identity,
		Version:       version,
	}); err != nil {
		log.Fatalf("hyve-agent: exited with error: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
