// Package clusterconfig groups the hyve CLI's server/controller-runtime
// commands (api, controller) under one "cluster-config" parent — these
// start the long-running infrastructure a cluster-mode deployment runs
// (see cmd/api's and cmd/controller's own doc comments). `api` also nests
// the local-user management commands (create-user, ...) a real operator
// does run interactively — e.g. bootstrapping the first login against a
// fresh cluster-mode install (see nexus-config/tunnel-test's own
// install-hyve-tunnel-host.yaml summary step) — which is why Cmd is NOT
// Hidden: an earlier revision hid the whole tree to keep `hyve --help`
// focused on cluster/template/workflow's everyday surface, but that also
// buried create-user where a first-time operator had no way to discover
// it, so visibility was restored at the user's own request.
package clusterconfig

import (
	"github.com/spf13/cobra"

	apicmd "github.com/cbridges1/hyve/cmd/api"
	controllercmd "github.com/cbridges1/hyve/cmd/controller"
)

// Cmd is the cluster-config command.
var Cmd = &cobra.Command{
	Use:   "cluster-config",
	Short: "Run hyve's server-side components (the API + auth layer, and the controller)",
	Long: `Commands for hyve's server-side components — the HTTP API + auth layer
(api, which also manages its local users — see 'hyve cluster-config api
create-user') and the controller-runtime reconcile loop (controller). The
'run' subcommands are long-running Kubernetes workloads a deployment runs,
not something a CLI user invokes interactively; 'api create-user' etc. are
the opposite — see their own subcommand help for details.`,
}

func init() {
	Cmd.AddCommand(apicmd.Cmd)
	Cmd.AddCommand(controllercmd.Cmd)
}
