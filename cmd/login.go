package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cbridges1/hyve/cmd/shared"
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
stores the resulting session at ~/.hyve/session.json for cluster-mode
commands to use.

Local mode (a git-backed repo or 'hyve set-state' directory) never needs
this — login only matters once cluster mode's HTTP API (see
HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 6) is in use.`,
	Run: func(cmd *cobra.Command, args []string) {
		runLogin()
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Discard the locally-stored cluster-mode session",
	Run: func(cmd *cobra.Command, args []string) {
		runLogout()
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
		username, err = promptLine("Username: ")
		if err != nil {
			log.Fatalf("Failed to read username: %v", err)
		}
	}
	password := loginPassword
	if password == "" {
		var err error
		password, err = promptSecret("Password: ")
		if err != nil {
			log.Fatalf("Failed to read password: %v", err)
		}
	}

	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		log.Fatalf("Failed to build request: %v", err)
	}

	apiURL := strings.TrimRight(loginAPIURL, "/")
	resp, err := http.Post(apiURL+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("Failed to reach %s: %v", apiURL, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Login failed (%s): %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	var loginResp struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.Unmarshal(respBody, &loginResp); err != nil {
		log.Fatalf("Failed to parse login response: %v", err)
	}

	sess := shared.Session{APIURL: apiURL, Token: loginResp.Token, ExpiresAt: loginResp.ExpiresAt}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		log.Fatalf("Failed to serialize session: %v", err)
	}
	path := shared.SessionPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		log.Fatalf("Failed to create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		log.Fatalf("Failed to write %s: %v", path, err)
	}

	fmt.Printf("✅ Logged in as %s (session expires %s)\n", username, loginResp.ExpiresAt)
}

func runLogout() {
	path := shared.SessionPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Fatalf("Failed to remove %s: %v", path, err)
	}
	fmt.Println("✅ Logged out")
}

func promptLine(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// promptSecret reads a line without echoing it — falls back to a plain
// (echoed) read when stdin isn't a real terminal, since term.ReadPassword
// requires an actual TTY file descriptor.
func promptSecret(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		data, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
