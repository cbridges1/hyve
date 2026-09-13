package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// resourceDTO is the response shape for GET /api/resources and
// GET /api/resources/<name> — mirrors workflowDTO exactly, including the
// Spec-vs-RefStatus split (see that type's doc comment).
type resourceDTO struct {
	// Name is the short, user-facing name — see clusterDTO's own doc
	// comment on Name for the exact same convention. Never joined/split
	// for a RefStatus row, same reasoning as workflowDTO's own Name field.
	Name        string                     `json:"name"`
	Environment string                     `json:"environment,omitempty"`
	Spec        *hyvev1alpha1.ResourceSpec `json:"spec,omitempty"`
	RefStatus   *resourceRefStatusDTO      `json:"refStatus,omitempty"`
}

// resourceRefStatusDTO mirrors workflowRefStatusDTO, minus
// ResolvedVersion — ResourceRefStatusStatus has no such field.
type resourceRefStatusDTO struct {
	Source     string `json:"source"`
	Resolved   bool   `json:"resolved"`
	RawVersion string `json:"rawVersion,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	Error      string `json:"error,omitempty"`
}

func toResourceDTO(cr *hyvev1alpha1.Resource) resourceDTO {
	spec := cr.Spec
	name, environment := cr.Name, cr.Labels[hyveEnvironmentLabel]
	if environment != "" {
		name = splitEnvironmentPrefix(cr.Name, environment)
	}
	return resourceDTO{Name: name, Environment: environment, Spec: &spec}
}

// toResourceRefStatusDTO mirrors toWorkflowRefStatusDTO — see its doc
// comment for why Name comes from spec.Name, not cr.Name.
func toResourceRefStatusDTO(cr *hyvev1alpha1.ResourceRefStatus) resourceDTO {
	return resourceDTO{
		Name: cr.Spec.Name,
		RefStatus: &resourceRefStatusDTO{
			Source:     cr.Spec.Source,
			Resolved:   cr.Status.Resolved,
			RawVersion: cr.Status.RawVersion,
			SHA256:     cr.Status.SHA256,
			Error:      cr.Status.Error,
		},
	}
}

// registerResourceRoutes wires the /resources endpoints onto mux — mounted
// under /api/ (and behind requireAuth+requireRole) by Server.Routes.
func (s *Server) registerResourceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /resources", s.requireOrganizationNotMigrating(s.handleListResources))
	mux.HandleFunc("GET /resources/{name}", s.requireOrganizationNotMigrating(s.handleGetResource))
	mux.HandleFunc("POST /resources", s.requireOrganizationNotMigrating(s.handleCreateResource))
	mux.HandleFunc("PATCH /resources/{name}", s.requireOrganizationNotMigrating(s.handleUpdateResource))
	mux.HandleFunc("DELETE /resources/{name}", s.requireOrganizationNotMigrating(s.handleDeleteResource))
}

func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := s.TenantNamespace(r)
	rc, err := s.resourceClient(ctx, namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, err)
		writeError(w, http.StatusInternalServerError, "failed to list resources")
		return
	}
	var list hyvev1alpha1.ResourceList
	if err := rc.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		log.Printf("api: failed to list resources: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list resources")
		return
	}
	var refStatusList hyvev1alpha1.ResourceRefStatusList
	if err := rc.List(ctx, &refStatusList, client.InNamespace(namespace)); err != nil {
		log.Printf("api: failed to list resource ref statuses: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list resources")
		return
	}
	dtos := make([]resourceDTO, 0, len(list.Items)+len(refStatusList.Items))
	for i := range list.Items {
		dtos = append(dtos, toResourceDTO(&list.Items[i]))
	}
	for i := range refStatusList.Items {
		dtos = append(dtos, toResourceRefStatusDTO(&refStatusList.Items[i]))
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleGetResource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := s.TenantNamespace(r)
	name := r.PathValue("name")
	resolvedName := s.resolveAddressedName(r, namespace, name)
	rc, rcErr := s.resourceClient(ctx, namespace)
	if rcErr != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, rcErr)
		writeError(w, http.StatusInternalServerError, "failed to get resource")
		return
	}
	var cr hyvev1alpha1.Resource
	err := rc.Get(ctx, types.NamespacedName{Namespace: namespace, Name: resolvedName}, &cr)
	if err == nil {
		writeJSON(w, http.StatusOK, toResourceDTO(&cr))
		return
	}
	if !apierrors.IsNotFound(err) {
		log.Printf("api: failed to get resource %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get resource")
		return
	}

	// Not a real Resource CR — check whether it's a git-referenced one
	// instead, mirrored under a derived metadata.name (see
	// toResourceRefStatusDTO). If more than one ref shares this short
	// Name (see resourceref.NameCollision), the first match wins — full
	// disambiguation isn't supported here yet.
	var refStatusList hyvev1alpha1.ResourceRefStatusList
	if err := rc.List(ctx, &refStatusList, client.InNamespace(namespace)); err != nil {
		log.Printf("api: failed to list resource ref statuses: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to get resource")
		return
	}
	for i := range refStatusList.Items {
		if refStatusList.Items[i].Spec.Name == name {
			writeJSON(w, http.StatusOK, toResourceRefStatusDTO(&refStatusList.Items[i]))
			return
		}
	}
	writeError(w, http.StatusNotFound, "resource not found")
}

// createResourceRequest reuses hyvev1alpha1.ResourceSpec directly as the
// request body's spec shape, same precedent as createWorkflowRequest.
type createResourceRequest struct {
	Name string                    `json:"name"`
	Spec hyvev1alpha1.ResourceSpec `json:"spec"`
}

func (s *Server) handleCreateResource(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	var req createResourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	namespace := s.TenantNamespace(r)
	rc, err := s.resourceClient(r.Context(), namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, err)
		writeError(w, http.StatusInternalServerError, "failed to create resource")
		return
	}
	envResult, ok := s.resolveCreateName(w, r, namespace, req.Name)
	if !ok {
		return
	}
	meta := metav1.ObjectMeta{Name: envResult.RealName, Namespace: namespace}
	if envResult.HasEnvironment {
		meta.Labels = map[string]string{hyveEnvironmentLabel: envResult.Label}
	}
	cr := &hyvev1alpha1.Resource{
		ObjectMeta: meta,
		Spec:       req.Spec,
	}
	if err := rc.Create(r.Context(), cr); err != nil {
		if apierrors.IsAlreadyExists(err) {
			writeError(w, http.StatusConflict, "resource already exists")
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to create resource: %v", err))
		return
	}
	writeJSON(w, http.StatusCreated, toResourceDTO(cr))
}

// updateResourceRequest mirrors createResourceRequest's Spec shape — a
// full-spec replace, not a merge-patch.
type updateResourceRequest struct {
	Spec hyvev1alpha1.ResourceSpec `json:"spec"`
}

// handleUpdateResource mirrors handleUpdateWorkflow's Get-by-name stance —
// a git-ref-backed resource has no real Resource CR under `name` (see
// toResourceRefStatusDTO), so this naturally 404s for that case, same as
// handleDeleteResource already does.
func (s *Server) handleUpdateResource(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	ctx := r.Context()
	namespace := s.TenantNamespace(r)
	name := s.resolveAddressedName(r, namespace, r.PathValue("name"))
	var req updateResourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	rc, err := s.resourceClient(ctx, namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, err)
		writeError(w, http.StatusInternalServerError, "failed to update resource")
		return
	}
	var cr hyvev1alpha1.Resource
	if err := rc.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "resource not found")
			return
		}
		log.Printf("api: failed to get resource %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get resource")
		return
	}
	cr.Spec = req.Spec
	if err := rc.Update(ctx, &cr); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to update resource: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, toResourceDTO(&cr))
}

func (s *Server) handleDeleteResource(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	ctx := r.Context()
	namespace := s.TenantNamespace(r)
	name := s.resolveAddressedName(r, namespace, r.PathValue("name"))
	rc, err := s.resourceClient(ctx, namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for %q: %v", namespace, err)
		writeError(w, http.StatusInternalServerError, "failed to delete resource")
		return
	}
	cr := &hyvev1alpha1.Resource{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := rc.Delete(ctx, cr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "resource not found")
			return
		}
		log.Printf("api: failed to delete resource %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to delete resource")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
