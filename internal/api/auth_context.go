package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/module"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

// authContextDTO is the response shape for
// GET /api/clusters/<name>/auth-context — everything a CLI needs to run a
// driver module's auth operation entirely client-side, with NO local
// hyve.lock/module resolution of its own (see cmd/cluster/auth.go's
// cluster-mode path, and ClusterDefinitionSpec.Access's doc comment for why
// this is the default). The module is resolved here, server-side, against
// this API's own baked-in ModulesDir (the same one ModuleAuthProvider uses
// for the AccessMethodModuleAuth override) — AuthFileContent is that
// resolved auth operation file's raw bytes, verbatim, so the CLI only has
// to write them to a temp directory and run Executor against it. Deriving
// this from the API's own module cache, not the CLI's, is what makes
// client-side auth possible for a machine with no local environment/git
// checkout at all — cluster mode and local directories are otherwise
// completely independent (see internal/session's own doc comment), and
// requiring a local hyve.lock here would silently reintroduce that
// coupling for this one command.
//
// Deliberately a separate, more sensitive endpoint from clusterDTO
// (GET /api/clusters/<name>) — that one intentionally excludes
// driverOutputs/params (see its own doc comment); this one exists
// specifically to expose them for the one legitimate purpose that needs
// them, and only for clusters actually using client-side auth.
type authContextDTO struct {
	DriverSource    string                `json:"driverSource"`
	DriverVersion   string                `json:"driverVersion"`
	Region          string                `json:"region,omitempty"`
	Params          map[string]string     `json:"params,omitempty"`
	DriverOutputs   map[string]string     `json:"driverOutputs,omitempty"`
	AuthFileName    string                `json:"authFileName"`
	AuthFileContent string                `json:"authFileContent"`
	Tools           []authToolRequirement `json:"tools,omitempty"`
}

// authToolRequirement mirrors module.ToolRequirement's Name/Description —
// just enough for the CLI to run the same local-PATH tool validation it
// always has, sourced from the server's resolved module.yaml instead of a
// local one (see cmd/cluster/auth.go's use of this).
type authToolRequirement struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// githubToken does a live, uncached read of hyve-cli-secrets' GITHUB_TOKEN
// (see internal/api/secrets.go's cliSecretsName — the same Secret
// `hyve env secrets set GITHUB_TOKEN ...` writes) so a private Git-sourced
// driver module can actually be cloned here, mirroring
// internal/controller/reconciler.go's own fetchCLISecrets/
// resolveModuleIfNeeded, which already does this for the controller's own
// reconcile-time module resolution — this endpoint had none of that,
// silently depending on either a pre-baked ModulesDir or a public repo.
// Best-effort: a missing Secret or key just means an empty token, which
// module.ResolveWithToken falls back to the process's own GITHUB_TOKEN env
// var for (almost certainly also unset here) — the same "not configured
// yet is fine, let the actual git clone fail with its own clear error"
// stance used everywhere else this token is threaded through.
//
// namespace is the caller's own TenantNamespace(r), threaded in by every
// call site rather than derived here — this Secret is per-tenant (see
// secrets.go's own doc comment on cliSecretsName), and this function has no
// *http.Request of its own to resolve it from.
func (s *Server) githubToken(ctx context.Context, namespace string) string {
	var secret corev1.Secret
	if err := s.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: cliSecretsName}, &secret); err != nil {
		return ""
	}
	return string(secret.Data["GITHUB_TOKEN"])
}

// registerAuthContextRoutes wires GET /clusters/{name}/auth-context —
// mounted under /api/ (behind requireAuth+requireRole) by Server.Routes.
func (s *Server) registerAuthContextRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /clusters/{name}/auth-context", s.handleAuthContext)
}

// handleAuthContext only serves clusters using the default client-side auth
// method (spec.access.method unset) — a cluster that's opted into the
// AccessMethodModuleAuth override, AccessMethodTunnel, or AccessMethodPrimary
// (the API's own host cluster — no driver module at all, always
// server-minted via TokenRequest, see PrimaryClusterProvider) is
// server-minted via GET /api/kubeconfig instead, and returning driver
// secrets here for those would just be a second, weaker-guaranteed way to
// reach the same access (no authorization check baked in, unlike the
// override path's module-side check — see moduleEnvForClusterDefinition).
// The generic access.method != "" rejection below already covers all three
// non-default cases uniformly — no special-casing needed per method.
func (s *Server) handleAuthContext(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	var cd hyvev1alpha1.ClusterDefinition
	if err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: s.TenantNamespace(r), Name: name}, &cd); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "cluster not found")
			return
		}
		log.Printf("api: failed to get cluster %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get cluster")
		return
	}
	if cd.Spec.Access.Method != "" {
		writeError(w, http.StatusConflict, fmt.Sprintf("cluster %q uses access.method %q, not client-side auth — fetch its kubeconfig via GET /api/kubeconfig instead", name, cd.Spec.Access.Method))
		return
	}

	lf, err := module.LoadLockFile(s.ModulesDir)
	if err != nil {
		log.Printf("api: failed to load hyve.lock for auth-context %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to resolve driver module")
		return
	}
	locked := lf.GetLocked(cd.Spec.Driver.Source, cd.Spec.Driver.Version)
	resolved, err := module.ResolveWithToken(cd.Spec.Driver.Source, cd.Spec.Driver.Version, locked, s.ModulesDir, s.githubToken(r.Context(), s.TenantNamespace(r)))
	if err != nil {
		log.Printf("api: failed to resolve driver module %s@%s for auth-context %q: %v", cd.Spec.Driver.Source, cd.Spec.Driver.Version, name, err)
		writeError(w, http.StatusInternalServerError, "failed to resolve driver module")
		return
	}

	authPath, ok := module.FindOperationFile(resolved.Dir, module.OperationAuth)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("driver module %s has no auth operation", cd.Spec.Driver.Source))
		return
	}
	authContent, err := os.ReadFile(authPath)
	if err != nil {
		log.Printf("api: failed to read auth file %s for %q: %v", authPath, name, err)
		writeError(w, http.StatusInternalServerError, "failed to read driver module auth file")
		return
	}

	var tools []authToolRequirement
	if manifest, _ := module.LoadManifestForSource(cd.Spec.Driver.Source, cd.Spec.Driver.Version, s.ModulesDir, lf); manifest != nil {
		for _, t := range manifest.Spec.Requirements.Tools {
			tools = append(tools, authToolRequirement{Name: t.Name, Description: t.Description})
		}
	}

	writeJSON(w, http.StatusOK, authContextDTO{
		DriverSource:    cd.Spec.Driver.Source,
		DriverVersion:   cd.Spec.Driver.Version,
		Region:          cd.Spec.Region,
		Params:          cd.Spec.Params,
		DriverOutputs:   cd.Status.DriverOutputs,
		AuthFileName:    filepath.Base(authPath),
		AuthFileContent: string(authContent),
		Tools:           tools,
	})
}
