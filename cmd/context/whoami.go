package contextcmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
)

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show whether you're currently authenticated to a hyve API server, and as whom",
	Long: `Reports the current cluster-mode session (see 'hyve context login' — one
global, machine-wide credential that does NOT depend on which context
is current), if any, and confirms it directly against the API server
rather than trusting the local record alone — a session can be locally
present but already expired or rejected server-side (e.g. after
'hyve context logout' revoked it from elsewhere). Attempts a silent refresh
first if the cached access token has expired but the underlying session
hasn't — the same thing every other command does before deciding whether
it's authenticated.

Exits non-zero when not authenticated, so it's scriptable:
  hyve context whoami >/dev/null || hyve context login --api-url ...`,
	Run: func(cmd *cobra.Command, args []string) {
		runWhoami()
	},
}

func init() {
	Cmd.AddCommand(whoamiCmd)
}

func runWhoami() {
	sess, err := shared.EnsureValidSession()
	if sess == nil && err == nil {
		fmt.Println("Not logged in (run 'hyve context login --api-url ...')")
		os.Exit(1)
	}
	if err != nil {
		if sess != nil {
			fmt.Printf("Session expired (%v) — run 'hyve context login --api-url %s'\n", err, sess.APIURL)
		} else {
			fmt.Printf("Failed to read local session: %v\n", err)
		}
		os.Exit(1)
	}

	client := shared.NewAPIClient(sess)
	who, err := client.Whoami()
	if err != nil {
		fmt.Printf("Local session for %s couldn't be confirmed by the server (%v) — run 'hyve context login --api-url %s'\n", sess.APIURL, err, sess.APIURL)
		os.Exit(1)
	}

	fmt.Printf("✅ Logged in as %s (role: %s) against %s\n", who.Username, who.Role, sess.APIURL)
	if who.Organization != "" {
		fmt.Printf("   Organization: %s\n", who.Organization)
	}
	if client.Env != "" {
		fmt.Printf("   Environment: %s\n", client.Env)
	}
	if who.ReconcilingCluster != "" {
		fmt.Printf("   Reconciling cluster: %s\n", who.ReconcilingCluster)
	}
	fmt.Printf("   Session expires %s\n", sess.SessionExpiresAt)
}
