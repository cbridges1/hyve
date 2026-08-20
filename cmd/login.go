package cmd

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/repository"
)

var (
	loginAPIURL   string
	loginUsername string
	loginPassword string
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate against a hyve API server (cluster mode)",
	Long: `Logs in against a hyve API server's local (username/password) auth and
attaches the resulting session to the currently-active environment (see
'hyve env') for cluster-mode commands to use. If no environment is active
yet, one is created automatically (named from the API URL's host, directory
defaulted to the current working directory).

A pure local/GitOps environment (see 'hyve env create') never needs this —
login only matters once cluster mode's HTTP API is in use.`,
	Run: func(cmd *cobra.Command, args []string) {
		runLogin()
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout [name]",
	Short: "Discard cluster-mode credentials from an environment",
	Long: `Clears the cluster-mode session attached to the named environment (default:
the currently-active one), leaving its directory registration — and any
other environments — untouched. To remove the environment itself, see
'hyve env remove'.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		runLogout(name)
	},
}

func init() {
	loginCmd.Flags().StringVar(&loginAPIURL, "api-url", "", "Base URL of the hyve API server, e.g. https://hyve-api.example.com (required)")
	loginCmd.Flags().StringVar(&loginUsername, "username", "", "Username (omit to be prompted)")
	loginCmd.Flags().StringVar(&loginPassword, "password", "", "Password (scripting only — omit to be prompted without echo)")

	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
}

func runLogin() {
	if loginAPIURL == "" {
		log.Fatal("--api-url is required")
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

	apiURL := strings.TrimRight(loginAPIURL, "/")
	token, expiresAt, err := shared.PerformLogin(apiURL, username, password)
	if err != nil {
		log.Fatal(err)
	}

	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	current, err := repoMgr.GetCurrentRepository()
	if err != nil {
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatalf("Failed to resolve current directory: %v", err)
		}
		name := shared.UniqueEnvironmentName(repoMgr, environmentNameFromURL(apiURL))
		if _, err := repoMgr.AddRepository(name, "", cwd); err != nil {
			log.Fatalf("Failed to create environment '%s': %v", name, err)
		}
		if err := repoMgr.SetCurrentRepository(name); err != nil {
			log.Fatalf("Failed to activate environment '%s': %v", name, err)
		}
		current, err = repoMgr.GetRepositoryByName(name)
		if err != nil {
			log.Fatalf("Failed to look up environment '%s': %v", name, err)
		}
		log.Printf("No active environment — created '%s' (%s) and made it active", name, cwd)
	}

	if err := repoMgr.SetSession(current.Name, apiURL, token, expiresAt); err != nil {
		log.Fatalf("Failed to attach session: %v", err)
	}

	fmt.Printf("✅ Logged in as %s against '%s' (session expires %s)\n", username, current.Name, expiresAt)
}

func runLogout(name string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	if name == "" {
		current, err := repoMgr.GetCurrentRepository()
		if err != nil {
			log.Fatal("No active environment to log out of. Use 'hyve logout <name>' to target a specific one.")
		}
		name = current.Name
	}

	if err := repoMgr.ClearSession(name); err != nil {
		log.Fatalf("Failed to log out of '%s': %v", name, err)
	}
	fmt.Printf("✅ Logged out of '%s'\n", name)
}

// environmentNameFromURL derives a default environment name from an API
// URL's host when `hyve login` needs to auto-provision one — e.g.
// "https://hyve-api.example.com" -> "hyve-api.example.com".
func environmentNameFromURL(apiURL string) string {
	u, err := url.Parse(apiURL)
	if err != nil || u.Host == "" {
		return "default"
	}
	return u.Host
}
