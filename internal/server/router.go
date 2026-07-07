package server

import (
	"net/http"
	"time"

	"github.com/cbridges1/hyve/internal/execution"
	"github.com/cbridges1/hyve/internal/server/handler"
	"github.com/cbridges1/hyve/internal/state"
)

type routerConfig struct {
	Repo        *RepoContext
	Registry    *execution.Registry
	AuthMode    state.ServerAuthMode
	ValidateURL string
	Timeout     time.Duration
}

// newRouter builds the full route table. Unauthenticated routes (/health,
// /openapi.json) are registered directly; everything else is registered on a
// submux wrapped with the forward-auth middleware (a no-op passthrough when
// AuthMode is "none").
func newRouter(cfg routerConfig) (http.Handler, error) {
	stateMgr, err := cfg.Repo.StateManager()
	if err != nil {
		return nil, err
	}
	deps := &handler.Deps{
		RepoPath: cfg.Repo.Path,
		StateMgr: stateMgr,
		Registry: cfg.Registry,
	}

	health := handler.NewHealthHandlers()
	clusters := handler.NewClustersHandlers(deps)
	templates := handler.NewTemplatesHandlers(deps)
	workflows := handler.NewWorkflowsHandlers(deps)
	modules := handler.NewModulesHandlers(deps)
	gitH := handler.NewGitHandlers(deps, cfg.Repo.GitBackend)
	config := handler.NewConfigHandlers(deps)
	reconcileH := handler.NewReconcileHandlers(deps)
	kubeconfigH := handler.NewKubeconfigHandlers(deps)
	executions := handler.NewExecutionsHandlers(deps)
	ws := handler.NewExecutionsWSHandlers(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health.Health)
	mux.HandleFunc("GET /openapi.json", ServeOpenAPI)

	protected := http.NewServeMux()
	protected.HandleFunc("GET /auth/check", health.AuthCheck)

	protected.HandleFunc("GET /clusters", clusters.List)
	protected.HandleFunc("GET /clusters/{name}", clusters.Get)
	protected.HandleFunc("POST /clusters", clusters.Create)
	protected.HandleFunc("PUT /clusters/{name}", clusters.Put)
	protected.HandleFunc("PATCH /clusters/{name}", clusters.Patch)
	protected.HandleFunc("DELETE /clusters/{name}", clusters.Delete)
	protected.HandleFunc("GET /clusters/{name}/resources", clusters.Resources)
	protected.HandleFunc("POST /clusters/{name}/kubeconfig", kubeconfigH.Auth)
	protected.HandleFunc("DELETE /clusters/{name}/kubeconfig", kubeconfigH.Deauth)
	protected.HandleFunc("POST /clusters/kubeconfig/sync", kubeconfigH.Sync)

	protected.HandleFunc("GET /templates", templates.List)
	protected.HandleFunc("GET /templates/{name}", templates.Get)
	protected.HandleFunc("POST /templates", templates.Create)
	protected.HandleFunc("PUT /templates/{name}", templates.Put)
	protected.HandleFunc("DELETE /templates/{name}", templates.Delete)
	protected.HandleFunc("POST /templates/{name}/validate", templates.Validate)

	protected.HandleFunc("GET /workflows", workflows.List)
	protected.HandleFunc("GET /workflows/{name}", workflows.Get)
	protected.HandleFunc("POST /workflows", workflows.Create)
	protected.HandleFunc("PUT /workflows/{name}", workflows.Put)
	protected.HandleFunc("DELETE /workflows/{name}", workflows.Delete)
	protected.HandleFunc("POST /workflows/{name}/run", workflows.Run)
	protected.HandleFunc("POST /workflows/{name}/validate", workflows.Validate)
	protected.HandleFunc("POST /workflows/install", workflows.Install)
	protected.HandleFunc("POST /workflows/refs/update", workflows.UpdateRef)
	protected.HandleFunc("GET /workflows/refs/verify", workflows.VerifyRefs)

	protected.HandleFunc("GET /modules", modules.List)
	protected.HandleFunc("GET /modules/{source...}", modules.Info)
	protected.HandleFunc("POST /modules", modules.Add)
	protected.HandleFunc("POST /modules/update", modules.Update)
	protected.HandleFunc("POST /modules/remove", modules.Remove)
	protected.HandleFunc("POST /modules/validate", modules.Validate)
	protected.HandleFunc("POST /modules/install", modules.Install)
	protected.HandleFunc("POST /modules/init", modules.Init)
	protected.HandleFunc("GET /lock", modules.Lock)

	protected.HandleFunc("GET /config", config.Get)
	protected.HandleFunc("PATCH /config", config.Patch)

	protected.HandleFunc("GET /git/status", gitH.Status)
	protected.HandleFunc("POST /git/sync", gitH.Sync)

	protected.HandleFunc("POST /reconcile", reconcileH.All)
	protected.HandleFunc("POST /reconcile/clusters/{name}", reconcileH.Cluster)

	protected.HandleFunc("GET /executions", executions.List)
	protected.HandleFunc("GET /executions/{id}", executions.Get)
	protected.HandleFunc("GET /executions/{id}/logs", executions.Logs)
	protected.HandleFunc("GET /executions/{id}/stream", ws.Stream)

	var authMW func(http.Handler) http.Handler
	if cfg.AuthMode == state.ServerAuthModeForward {
		authMW = ForwardAuthMiddleware(ForwardAuthOptions{ValidateURL: cfg.ValidateURL, Timeout: cfg.Timeout})
	} else {
		authMW = noAuthMiddleware
	}
	mux.Handle("/", authMW(protected))

	return corsMiddleware(mux), nil
}
