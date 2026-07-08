package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/server"
	"github.com/cbridges1/hyve/internal/state"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run hyve as a local REST API server",
	Long: `Runs the hyve engine as a local REST + WebSocket API service — the same
operations available through the CLI become available over HTTP. See
docs/HYVE-SERVER-MODE.md for the full route table and authentication model.`,
	Run: func(cmd *cobra.Command, args []string) {
		opts, frontendURL, err := resolveServeOptions(cmd)
		if err != nil {
			log.Fatalf("%v", err)
		}

		openBrowser, _ := cmd.Flags().GetBool("open")

		srv, err := server.New(opts)
		if err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}

		if openBrowser {
			go srv.OpenBrowser(frontendURL)
		}

		log.Printf("hyve serve listening on %s (repo: %s)", srv.Addr(), opts.RepoPath)
		if err := srv.ListenAndServe(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	},
}

func init() {
	serveCmd.Flags().Int("port", 0, "Port to listen on (default 8080). Also read from HYVE_PORT.")
	serveCmd.Flags().StringP("path", "p", "", "Path to the hyve repository root. Defaults to the registered current repository (see `hyve git use`); falls back to the working directory if none is registered.")
	serveCmd.Flags().Bool("require-auth", false, "Reject unauthenticated requests regardless of hyve.yaml auth mode.")
	serveCmd.Flags().Bool("open", false, "Open browser to frontendUrl (or http://localhost:<port>) after server starts.")
	serveCmd.Flags().String("host", "127.0.0.1", "Bind address. Set to 0.0.0.0 to listen on all interfaces (required in Docker).")
}

// resolveServeOptions builds server.Options from flags, falling back to
// hyve.yaml's server section, then environment variables, matching the
// precedence documented in docs/HYVE-SERVER-MODE.md.
func resolveServeOptions(cmd *cobra.Command) (server.Options, string, error) {
	repoPath, _ := cmd.Flags().GetString("path")
	if repoPath == "" {
		// No --path given: match every other hyve command's default repo
		// resolution (the sqlite-registered "current repository") rather
		// than silently defaulting to the working directory, which is easy
		// to get wrong if hyve serve/open isn't run from inside the repo.
		if repoMgr, err := repository.NewManager(); err == nil {
			if currentRepo, err := repoMgr.GetCurrentRepository(); err == nil {
				repoPath = currentRepo.LocalPath
			}
			repoMgr.Close()
		}
	}
	if repoPath == "" {
		repoPath = "."
	}
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return server.Options{}, "", fmt.Errorf("failed to resolve repo path: %w", err)
	}
	log.Printf("Using repository path: %s", absPath)

	var cfg state.RepoConfig
	if stateMgr, err := state.NewManager("", absPath, "", os.Getenv("HYVE_GIT_TOKEN")); err == nil {
		if loaded, err := stateMgr.LoadRepoConfig(); err == nil {
			cfg = *loaded
		}
	}

	port, _ := cmd.Flags().GetInt("port")
	if port == 0 {
		port = cfg.Server.Port
	}
	if port == 0 {
		if envPort := os.Getenv("HYVE_PORT"); envPort != "" {
			if v, err := strconv.Atoi(envPort); err == nil {
				port = v
			}
		}
	}
	if port == 0 {
		port = 8080
	}

	host, _ := cmd.Flags().GetString("host")
	requireAuth, _ := cmd.Flags().GetBool("require-auth")

	opts := server.Options{
		Port:            port,
		Host:            host,
		RepoPath:        absPath,
		AuthMode:        cfg.Server.Auth.Mode,
		ValidateURL:     cfg.Server.Auth.Forward.ValidateURL,
		ValidateTimeout: cfg.Server.Auth.Forward.Timeout,
		RequireAuth:     requireAuth,
	}
	return opts, cfg.Server.FrontendUrl, nil
}
