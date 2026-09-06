package cmd

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/session"
)

var (
	loginAPIURL   string
	loginUsername string
	loginPassword string
	loginOrg      string
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate against a hyve API server (cluster mode)",
	Long: `Logs in against a hyve API server's local (username/password) auth. This is
one global, machine-wide credential — like 'gh auth login' or 'docker
login' — completely independent of whichever local directory 'hyve env' has
selected: cluster-mode commands use this session regardless of the current
environment, and a pure local/GitOps environment never needs it at all.

--api-url defaults to the current environment's --api-url (see 'hyve env
create --api-url') when omitted — registering a cluster environment ahead
of time and logging into it later ('hyve env use that-cluster && hyve
login') are two separate, independently-timed steps; pass --api-url
explicitly to log into a URL that isn't (or isn't yet) a registered
environment at all.

If --api-url doesn't match any already-registered environment, one is
registered automatically (named from the URL's host, deduplicated on
collision) — so a bare 'hyve login --api-url ...' is enough to both
authenticate and make that cluster visible in 'hyve env list' afterward,
without a separate 'hyve env create' step. Only made the active
environment if you had none registered yet; otherwise your existing
current environment (e.g. a local directory) is left alone.

The session returned stays usable for a while (see the server's own
SessionTTL) — a short-lived access token cached from it is silently
refreshed as needed, so routine use never requires logging in again until
the underlying session itself expires or 'hyve logout' revokes it.`,
	Run: func(cmd *cobra.Command, args []string) {
		runLogin()
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Discard the current cluster-mode session",
	Long: `Revokes the current session server-side (immediate — the underlying
credential stops working right away) and clears it locally. Any cached
access token keeps responding to API requests for up to its own short
remaining TTL regardless (see AccessTokenTTL) — there's no cheaper way to
invalidate an already-issued one.`,
	Run: func(cmd *cobra.Command, args []string) {
		runLogout()
	},
}

func init() {
	loginCmd.Flags().StringVar(&loginAPIURL, "api-url", "", "Base URL of the hyve API server, e.g. https://hyve-api.example.com (default: the current environment's --api-url, see 'hyve env create')")
	loginCmd.Flags().StringVar(&loginUsername, "username", "", "Username (omit to be prompted)")
	loginCmd.Flags().StringVar(&loginPassword, "password", "", "Password (scripting only — omit to be prompted without echo)")
	loginCmd.Flags().StringVar(&loginOrg, "org", "", "Tenant to log into (omit for the control-plane/superadmin tier) — resolved to a namespace client-side, see cmd/shared.ResolveOrgToNamespace")

	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
}

func runLogin() {
	apiURLFlag := loginAPIURL
	if apiURLFlag == "" {
		apiURLFlag = currentEnvironmentAPIURL()
	}
	if apiURLFlag == "" {
		log.Fatal("--api-url is required (no current environment has one registered — see 'hyve env create --api-url')")
	}
	username := loginUsername
	if username == "" {
		var err error
		username, err = shared.PromptLine("Username: ")
		if err != nil {
			log.Fatalf("Failed to read username: %v", err)
		}
	}
	password := loginPassword
	if password == "" {
		var err error
		password, err = shared.PromptSecret("Password: ")
		if err != nil {
			log.Fatalf("Failed to read password: %v", err)
		}
	}

	namespace := shared.ResolveOrgToNamespace(loginOrg)

	apiURL := strings.TrimRight(apiURLFlag, "/")
	sess, err := shared.PerformLogin(apiURL, username, password, namespace)
	if err != nil {
		log.Fatal(err)
	}
	if err := session.Save(sess); err != nil {
		log.Fatalf("Failed to save session: %v", err)
	}

	fmt.Printf("✅ Logged in as %s against %s (session expires %s)\n", username, apiURL, sess.SessionExpiresAt)

	ensureClusterEnvironmentRegistered(apiURL)
}

func runLogout() {
	sess, err := session.Load()
	if err != nil {
		log.Fatalf("Failed to read local session: %v", err)
	}
	if sess == nil {
		fmt.Println("Not logged in.")
		return
	}

	if err := shared.RevokeSession(sess); err != nil {
		log.Printf("⚠️  Failed to revoke session server-side (%v) — clearing the local record anyway.", err)
	}
	if err := session.Clear(); err != nil {
		log.Fatalf("Failed to clear local session: %v", err)
	}
	fmt.Println("✅ Logged out")
}

// currentEnvironmentAPIURL returns the active environment's registered
// --api-url (see 'hyve env create --api-url'), or "" if there's no current
// environment or it has none set. Never fatal — an empty result just means
// --api-url must be passed explicitly, which runLogin reports itself.
func currentEnvironmentAPIURL() string {
	repoMgr, err := repository.NewManager()
	if err != nil {
		return ""
	}
	defer repoMgr.Close()

	current, err := repoMgr.GetCurrentRepository()
	if err != nil {
		return ""
	}
	return current.APIURL
}

// ensureClusterEnvironmentRegistered makes sure apiURL shows up in 'hyve
// env list' after a successful login, registering one automatically if no
// existing environment already has it. This stores no credential — only
// the URL, same as 'hyve env create --api-url' — so it doesn't reintroduce
// the old bug where login and environment selection were the same row:
// the actual session stays exactly where session.Save just put it,
// entirely independent of this registry entry. Best-effort: a failure here
// only means the new environment doesn't show up in 'hyve env list' yet,
// not that login itself failed, so it only warns, never log.Fatal.
func ensureClusterEnvironmentRegistered(apiURL string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Printf("⚠️  Logged in, but couldn't register '%s' as an environment: %v", apiURL, err)
		return
	}
	defer repoMgr.Close()

	envs, err := repoMgr.ListRepositories()
	if err != nil {
		log.Printf("⚠️  Logged in, but couldn't check existing environments: %v", err)
		return
	}
	for _, e := range envs {
		if e.APIURL == apiURL {
			return // already registered under some name — nothing to do
		}
	}

	name := shared.UniqueEnvironmentName(repoMgr, environmentNameFromURL(apiURL))
	hadAny := len(envs) > 0

	if _, err := repoMgr.AddRepository(name, "", "", apiURL); err != nil {
		log.Printf("⚠️  Logged in, but couldn't register '%s' as an environment: %v", apiURL, err)
		return
	}
	fmt.Printf("📁 Registered '%s' as a new environment (api: %s)\n", name, apiURL)

	if !hadAny {
		if err := repoMgr.SetCurrentRepository(name); err != nil {
			log.Printf("⚠️  Registered '%s' but couldn't activate it: %v", name, err)
		}
	}
}

// environmentNameFromURL derives a registry-safe name from apiURL's host —
// e.g. "https://hyve-api.example.com" -> "hyve-api-example-com". Falls
// back to the raw URL, similarly sanitized, if it doesn't parse as one
// with a host (shouldn't happen in practice: PerformLogin already
// succeeded against this URL by the time this runs).
func environmentNameFromURL(apiURL string) string {
	host := ""
	if u, err := url.Parse(apiURL); err == nil {
		host = u.Hostname()
	}
	if host == "" {
		host = apiURL
	}

	var b strings.Builder
	for _, r := range strings.ToLower(host) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "cluster"
	}
	return name
}
