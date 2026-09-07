package cluster

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
)

// logsCmd is cluster mode only: it answers "I see no logs that indicate the
// job responsible for creating/deleting this cluster does anything" —
// there's no local-mode equivalent to be a counterpart to, since a local/CLI
// mode reconcile's create/delete op already runs inline in the invoking
// process, streaming straight to that terminal's own stdout/stderr — nothing
// is ever hidden inside a dispatched, log-discarding Job the way cluster
// mode's Job dispatch is (see k8sjob.Run, which always deletes its Job right
// after fetching logs, and module.Executor.executeScript, which previously
// discarded a script's raw stdout entirely once parsed).
var logsCmd = &cobra.Command{
	Use:   "logs [cluster-name]",
	Short: "Show recent lifecycle events and captured create/delete output for a cluster (cluster mode only)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		sess, ok := shared.UseClusterMode()
		if !ok {
			log.Fatal("`hyve cluster logs` requires cluster mode — run `hyve login` first. In local/CLI mode, a create/delete operation's output already streams straight to this terminal when `hyve reconcile` runs it.")
		}
		showClusterLogsAPI(shared.NewAPIClient(sess), args[0])
	},
}

func init() { Cmd.AddCommand(logsCmd) }

func showClusterLogsAPI(client *shared.APIClient, clusterName string) {
	activity, err := client.GetClusterEvents(clusterName)
	if err != nil {
		log.Fatalf("Failed to get cluster events: %v", err)
	}

	if len(activity.Events) == 0 {
		fmt.Println("No events recorded yet for this cluster.")
	} else {
		fmt.Println("Events:")
		for _, ev := range activity.Events {
			count := ""
			if ev.Count > 1 {
				count = fmt.Sprintf(" (x%d)", ev.Count)
			}
			fmt.Printf("  [%s] %-16s %s%s  (%s)\n", ev.Type, ev.Reason, ev.Message, count, ev.LastSeen)
		}
	}

	if activity.LastCreateOutput != "" {
		fmt.Printf("\n── Last create output ──\n%s\n", activity.LastCreateOutput)
	}
	if activity.LastDeleteOutput != "" {
		fmt.Printf("\n── Last delete output ──\n%s\n", activity.LastDeleteOutput)
	}
}
