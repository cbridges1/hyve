// Package clusterconfig groups the hyve CLI's server/controller-runtime
// commands (api, controller) under one "cluster-config" parent — these
// start the long-running infrastructure a cluster-mode deployment runs
// (see cmd/api's and cmd/controller's own doc comments), which is a
// different kind of thing from every other top-level command (cluster,
// module, template, workflow, ...), all of which are one-shot operations a
// human or script runs interactively. Nesting them here keeps `hyve --help`
// focused on that everyday surface while still shipping both from the same
// binary.
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
(api) and the controller-runtime reconcile loop (controller). Both are
long-running Kubernetes workloads a deployment runs, not something a CLI
user invokes interactively — see their own subcommand help for details.`,
}

func init() {
	Cmd.AddCommand(apicmd.Cmd)
	Cmd.AddCommand(controllercmd.Cmd)
}
