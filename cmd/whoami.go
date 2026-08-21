package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
)

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show whether you're currently authenticated to a hyve API server, and as whom",
	Long: `Reports the current cluster-mode session (see 'hyve login' — one global,
machine-wide credential, independent of 'hyve env'), if any, and confirms it
directly against the API server rather than trusting the local record
alone — a session can be locally present but already expired or rejected
server-side (e.g. after 'hyve logout' revoked it from elsewhere). Attempts
a silent refresh first if the cached access token has expired but the
underlying session hasn't — the same thing every other command does before
deciding whether it's authenticated.

Exits non-zero when not authenticated, so it's scriptable:
  hyve whoami >/dev/null || hyve login --api-url ...`,
	Run: func(cmd *cobra.Command, args []string) {
		runWhoami()
	},
}

func init() {
	rootCmd.AddCommand(whoamiCmd)
}

func runWhoami() {
	sess, err := shared.EnsureValidSession()
	if sess == nil && err == nil {
		fmt.Println("Not logged in (run 'hyve login --api-url ...')")
		os.Exit(1)
	}
	if err != nil {
		if sess != nil {
			fmt.Printf("Session expired (%v) — run 'hyve login --api-url %s'\n", err, sess.APIURL)
		} else {
			fmt.Printf("Failed to read local session: %v\n", err)
		}
		os.Exit(1)
	}

	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(sess.APIURL, "/")+"/api/whoami", nil)
	if err != nil {
		fmt.Printf("Failed to build request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+sess.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("Have a local session for %s, but couldn't reach it to confirm: %v\n", sess.APIURL, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Local session for %s was rejected by the server (%s) — run 'hyve login --api-url %s'\n", sess.APIURL, resp.Status, sess.APIURL)
		os.Exit(1)
	}

	var who struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := json.Unmarshal(body, &who); err != nil {
		fmt.Printf("Failed to parse server response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Logged in as %s (role: %s) against %s\n", who.Username, who.Role, sess.APIURL)
	fmt.Printf("   Session expires %s\n", sess.SessionExpiresAt)
}
