// Package server implements `hyve serve` — a REST + WebSocket API exposing
// the same operations available through the CLI over HTTP. Every route
// handler (internal/server/handler) is a thin wrapper around the same
// internal/* packages the CLI already calls; this package owns the HTTP
// plumbing: routing, forward-auth middleware, and the execution registry
// backing polling/WebSocket log streaming.
package server

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/cbridges1/hyve/internal/execution"
	"github.com/cbridges1/hyve/internal/state"
)

// Options configures a Server. Only Port, Host, and RepoPath are required —
// everything auth-related defaults to no-op (auth.mode "none").
type Options struct {
	Port     int
	Host     string
	RepoPath string

	// AuthMode/ValidateURL/ValidateTimeout mirror hyve.yaml's server.auth
	// section (see internal/state.ServerAuthConfig) — the caller (cmd/serve.go)
	// resolves hyve.yaml + flags + env var precedence before constructing
	// Options.
	AuthMode        state.ServerAuthMode
	ValidateURL     string
	ValidateTimeout string // Go duration string, e.g. "3s"; defaults to 3s if empty

	// RequireAuth rejects unauthenticated requests regardless of AuthMode —
	// if AuthMode is "none", RequireAuth promotes the effective mode to
	// "forward" (ValidateURL must then be set, from hyve.yaml, a flag, or
	// HYVE_AUTH_VALIDATE_URL).
	RequireAuth bool
}

// Server is a running (or ready-to-run) hyve-server instance.
type Server struct {
	opts     Options
	repo     *RepoContext
	registry *execution.Registry
	httpSrv  *http.Server
}

// New builds a Server bound to opts.RepoPath. It resolves the effective auth
// mode, enforces the none-mode + --host 0.0.0.0 guard, and builds the full
// route table. It does not start listening — call ListenAndServe for that.
func New(opts Options) (*Server, error) {
	repo, err := NewRepoContext(opts.RepoPath)
	if err != nil {
		return nil, err
	}

	if opts.ValidateURL == "" {
		opts.ValidateURL = os.Getenv("HYVE_AUTH_VALIDATE_URL")
	}
	if opts.ValidateTimeout == "" {
		opts.ValidateTimeout = os.Getenv("HYVE_AUTH_VALIDATE_TIMEOUT")
	}
	if opts.ValidateTimeout == "" {
		opts.ValidateTimeout = "3s"
	}
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}

	effectiveMode := opts.AuthMode
	if effectiveMode == "" {
		effectiveMode = state.ServerAuthModeNone
	}
	if opts.RequireAuth {
		effectiveMode = state.ServerAuthModeForward
	}

	if effectiveMode == state.ServerAuthModeForward && opts.ValidateURL == "" {
		return nil, fmt.Errorf("server.auth.mode is forward (or --require-auth was set) but no validateUrl is configured — set server.auth.forward.validateUrl in hyve.yaml or HYVE_AUTH_VALIDATE_URL")
	}

	// In none mode the server binds to 127.0.0.1 and refuses to start with
	// --host 0.0.0.0 unless --require-auth is also set (which promotes
	// effectiveMode to forward above) — prevents accidentally exposing an
	// unauthenticated API on the network.
	if opts.Host == "0.0.0.0" && effectiveMode != state.ServerAuthModeForward {
		return nil, fmt.Errorf("refusing to bind to 0.0.0.0 with auth.mode=none — pass --require-auth (with a validateUrl configured) to expose the server on all interfaces")
	}

	timeout, err := time.ParseDuration(opts.ValidateTimeout)
	if err != nil {
		return nil, fmt.Errorf("invalid server.auth.forward.timeout %q: %w", opts.ValidateTimeout, err)
	}

	registry := execution.NewRegistry()

	router, err := newRouter(routerConfig{
		Repo:        repo,
		Registry:    registry,
		AuthMode:    effectiveMode,
		ValidateURL: opts.ValidateURL,
		Timeout:     timeout,
	})
	if err != nil {
		return nil, err
	}

	addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)
	return &Server{
		opts:     opts,
		repo:     repo,
		registry: registry,
		httpSrv:  &http.Server{Addr: addr, Handler: router},
	}, nil
}

// Addr returns the address the server listens (or will listen) on.
func (s *Server) Addr() string { return s.httpSrv.Addr }

// ListenAndServe starts the HTTP server and blocks until it stops.
func (s *Server) ListenAndServe() error {
	return s.httpSrv.ListenAndServe()
}

// OpenBrowser opens frontendURL (with ?server=http://<addr> appended) in the
// default browser, or http://<addr> directly if frontendURL is empty.
func (s *Server) OpenBrowser(frontendURL string) {
	addr := s.opts.Host
	if addr == "0.0.0.0" || addr == "" {
		addr = "127.0.0.1"
	}
	target := BuildFrontendURL(addr, s.opts.Port, frontendURL)
	log.Printf("Opening browser to %s", target)
	OpenURL(target)
}

// BuildFrontendURL returns frontendURL with ?server=http://<host>:<port>
// appended, or http://<host>:<port> directly if frontendURL is empty.
// Exported so `hyve open` can build the same URL for an already-running
// server it didn't start itself (and so doesn't have a *Server for).
func BuildFrontendURL(host string, port int, frontendURL string) string {
	serverURL := fmt.Sprintf("http://%s:%d", host, port)
	if frontendURL == "" {
		return serverURL
	}
	u, err := url.Parse(frontendURL)
	if err != nil {
		return frontendURL
	}
	q := u.Query()
	q.Set("server", serverURL)
	u.RawQuery = q.Encode()
	return u.String()
}

// OpenURL shells out to the platform's "open a URL" command.
func OpenURL(target string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	_ = cmd.Start()
}
