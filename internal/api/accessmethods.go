package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/module"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// accessMethodDTO is the response shape for GET/POST/PATCH
// /api/access-methods. Nothing here is sensitive (an AccessMethod never
// holds a credential, only where to reach a provider — see
// HYVE-ACCESS-METHOD-DESIGN.md), so this exposes spec directly rather than
// a narrower hand-picked view, same precedent as moduleDTO/templateDTO.
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
// Server.Routes. `hyve cluster auth` resolves a ClusterDefinition's
// accessMethodRef through the read side of this (there is no local-mode
// equivalent — an AccessMethod's driver module always runs server-side,
// see accessmethod_mint.go). Create/update/delete are admin-gated, same as
// Template/Workflow/Resource — AccessMethod is namespace-scoped exactly
// like those (+kubebuilder:resource:scope=Namespaced), and its mint
// operation is fully tenant-isolated (accessmethod_mint.go dispatches into
// the caller's own TenantNamespace(r), never a shared namespace), so an
// admin declaring one is no more sensitive than an admin already declaring
// an arbitrary server-side-executed Workflow step.
func (s *Server) registerAccessMethodRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /access-methods", s.handleListAccessMethods)
	mux.HandleFunc("GET /access-methods/{name}", s.handleGetAccessMethod)
	mux.HandleFunc("POST /access-methods", s.handleCreateAccessMethod)
	mux.HandleFunc("PATCH /access-methods/{name}", s.handleUpdateAccessMethod)
	mux.HandleFunc("DELETE /access-methods/{name}", s.handleDeleteAccessMethod)
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

// createAccessMethodRequest reuses hyvev1alpha1.AccessMethodSpec directly
// as the request body's spec shape, same precedent as createTemplateRequest.
type createAccessMethodRequest struct {
	Name string                        `json:"name"`
	Spec hyvev1alpha1.AccessMethodSpec `json:"spec"`
}

func (s *Server) handleCreateAccessMethod(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	var req createAccessMethodRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	cr := &hyvev1alpha1.AccessMethod{
		ObjectMeta: metav1.ObjectMeta{Name: req.Name, Namespace: s.TenantNamespace(r)},
		Spec:       req.Spec,
	}
	if err := s.Client.Create(r.Context(), cr); err != nil {
		if apierrors.IsAlreadyExists(err) {
			writeError(w, http.StatusConflict, "access method already exists")
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to create access method: %v", err))
		return
	}
	writeJSON(w, http.StatusCreated, toAccessMethodDTO(cr))
}

// updateAccessMethodRequest mirrors createAccessMethodRequest's Spec shape
// — a full-spec replace, not a merge-patch.
type updateAccessMethodRequest struct {
	Spec hyvev1alpha1.AccessMethodSpec `json:"spec"`
}

func (s *Server) handleUpdateAccessMethod(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	name := r.PathValue("name")
	var req updateAccessMethodRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

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
	cr.Spec = req.Spec
	if err := s.Client.Update(r.Context(), &cr); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to update access method: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, toAccessMethodDTO(&cr))
}

func (s *Server) handleDeleteAccessMethod(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	name := r.PathValue("name")
	cr := &hyvev1alpha1.AccessMethod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.TenantNamespace(r)}}
	if err := s.Client.Delete(r.Context(), cr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "access method not found")
			return
		}
		log.Printf("api: failed to delete access method %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to delete access method")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
