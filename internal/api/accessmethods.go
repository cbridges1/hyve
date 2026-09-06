package api

import (
	"context"
	"log"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/module"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// accessMethodDTO is the response shape for GET /api/access-methods and
// GET /api/access-methods/<name> — read-only, since admins own writes via
// kubectl apply directly (see AccessMethod's own doc comment). Nothing
// here is sensitive (an AccessMethod never holds a credential, only where
// to reach a provider — see HYVE-ACCESS-METHOD-DESIGN.md), so this exposes
// spec directly rather than a narrower hand-picked view, same precedent as
// moduleDTO/templateDTO.
type accessMethodDTO struct {
	Name string                        `json:"name"`
	Spec hyvev1alpha1.AccessMethodSpec `json:"spec"`

	// RequiredEnv is either am.Spec.RequiredEnv verbatim (InlineAuth case)
	// or the driver module's own declared spec.requirements.env entries
	// (its module.yaml) — only populated by the single-object GET
	// (handleGetAccessMethod), not the list, since the Driver case
	// requires resolving the module. `hyve cluster auth` reads these exact
	// names from the caller's own local environment and forwards only
	// them as POST /access-methods/<name>/mint's credentialEnv — never
	// the caller's whole environment. Omitted (nil) if module resolution
	// fails; that failure surfaces properly at mint time instead.
	RequiredEnv []string `json:"requiredEnv,omitempty"`
}

func toAccessMethodDTO(cr *hyvev1alpha1.AccessMethod) accessMethodDTO {
	return accessMethodDTO{Name: cr.Name, Spec: cr.Spec}
}

// registerAccessMethodRoutes wires the /access-methods endpoints onto mux —
// mounted under /api/ (and behind requireAuth+requireRole) by
// Server.Routes. Read-only: `hyve cluster auth` resolves a
// ClusterDefinition's accessMethodRef through this lookup (there is no
// local-mode equivalent — an AccessMethod's driver module always runs
// server-side, see accessmethod_mint.go) — no create/delete, since admins
// manage these directly via kubectl, the same stance HyveConfig already
// takes.
func (s *Server) registerAccessMethodRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /access-methods", s.handleListAccessMethods)
	mux.HandleFunc("GET /access-methods/{name}", s.handleGetAccessMethod)
}

func (s *Server) handleListAccessMethods(w http.ResponseWriter, r *http.Request) {
	var list hyvev1alpha1.AccessMethodList
	if err := s.Client.List(r.Context(), &list, client.InNamespace(s.TenantNamespace(r))); err != nil {
		log.Printf("api: failed to list access methods: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list access methods")
		return
	}
	dtos := make([]accessMethodDTO, 0, len(list.Items))
	for i := range list.Items {
		dtos = append(dtos, toAccessMethodDTO(&list.Items[i]))
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleGetAccessMethod(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var cr hyvev1alpha1.AccessMethod
	if err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: s.TenantNamespace(r), Name: name}, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "access method not found")
			return
		}
		log.Printf("api: failed to get access method %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get access method")
		return
	}
	dto := toAccessMethodDTO(&cr)
	dto.RequiredEnv = s.accessMethodRequiredEnv(r.Context(), &cr)
	writeJSON(w, http.StatusOK, dto)
}

// accessMethodRequiredEnv returns am.Spec.RequiredEnv directly when
// InlineAuth is set (declared right on the object, nothing to resolve),
// otherwise best-effort resolves am's driver module and returns its
// declared spec.requirements.env names — see accessMethodDTO's own doc
// comment on RequiredEnv. Returns nil on any Driver-case resolution
// failure; the caller (handleAccessMethodMint) surfaces that failure
// properly when it actually tries to mint, so silently omitting this here
// is fine.
func (s *Server) accessMethodRequiredEnv(ctx context.Context, am *hyvev1alpha1.AccessMethod) []string {
	if am.Spec.InlineAuth != "" {
		return am.Spec.RequiredEnv
	}

	lf, err := module.LoadLockFile(s.ModulesDir)
	if err != nil {
		return nil
	}
	manifest, err := module.LoadManifestForSource(am.Spec.Driver.Source, am.Spec.Driver.Version, s.ModulesDir, lf)
	if err != nil || manifest == nil {
		return nil
	}
	if len(manifest.Spec.Requirements.Env) == 0 {
		return nil
	}
	names := make([]string, len(manifest.Spec.Requirements.Env))
	for i, e := range manifest.Spec.Requirements.Env {
		names[i] = e.Name
	}
	return names
}
