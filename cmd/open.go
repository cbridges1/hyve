package cmd

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/server"
)

var openCmd = &cobra.Command{
	Use:   "open",
	Short: "Open a browser window to the configured frontend, starting the server if needed",
	Long: `Opens a browser window to the configured frontendUrl, passing the server
address as a query parameter. Starts the server first if it is not already
running:

  hyve open
  # Equivalent to: hyve serve --open (if server is not running)
  # Or: open <frontendUrl>?server=http://localhost:<port> (if server is already running)

If no frontendUrl is configured, hyve open opens http://localhost:<port> directly.`,
	Run: func(cmd *cobra.Command, args []string) {
		opts, frontendURL, err := resolveServeOptions(cmd)
		if err != nil {
			log.Fatalf("%v", err)
		}

		if isServerRunning(opts.Host, opts.Port) {
			addr := opts.Host
			if addr == "0.0.0.0" || addr == "" {
				addr = "127.0.0.1"
			}
			log.Printf("hyve server already running on %s:%d — opening browser", addr, opts.Port)
			server.OpenURL(server.BuildFrontendURL(addr, opts.Port, frontendURL))
			return
		}

		srv, err := server.New(opts)
		if err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
		go srv.OpenBrowser(frontendURL)

		log.Printf("hyve serve listening on %s (repo: %s)", srv.Addr(), opts.RepoPath)
		if err := srv.ListenAndServe(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	},
}

func init() {
	openCmd.Flags().Int("port", 0, "Port hyve serve listens on (default 8080). Also read from HYVE_PORT.")
	openCmd.Flags().StringP("path", "p", "", "Path to the hyve repository root. Defaults to the registered current repository (see `hyve git use`); falls back to the working directory if none is registered.")
	openCmd.Flags().Bool("require-auth", false, "Reject unauthenticated requests regardless of hyve.yaml auth mode.")
	openCmd.Flags().String("host", "127.0.0.1", "Bind address. Set to 0.0.0.0 to listen on all interfaces (required in Docker).")
}

// isServerRunning does a quick GET /health with a short timeout to check
// whether a hyve server is already listening on host:port.
func isServerRunning(host string, port int) bool {
	addr := host
	if addr == "0.0.0.0" || addr == "" {
		addr = "127.0.0.1"
	}
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://%s:%d/health", addr, port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
