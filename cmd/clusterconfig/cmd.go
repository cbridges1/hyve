//go:build ignore

// Package clusterconfig groups the hyve CLI's server-side commands under
// one "cluster-config" parent: the controller a deployment runs (see
// cmd/controller's own doc comment). hyve-api's 'api' commands were removed
// with hyve-api's login; sign-in is now 'hyve login' and 'hyve host run'.
package clusterconfig

import (
	"github.com/spf13/cobra"

	controllercmd "github.com/cbridges1/hyve/cmd/controller"
)

// Cmd is the cluster-config command.
var Cmd = &cobra.Command{
	Use:   "cluster-config",
	Short: "Run hyve's server-side controller",
	Long: `Commands for hyve's server-side controller, the controller-runtime
reconcile loop. 'controller run' is a long-running Kubernetes workload a
deployment runs, not something a CLI user invokes interactively.`,
}

func init() {
	Cmd.AddCommand(controllercmd.Cmd)
}
