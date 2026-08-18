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

// clusterDTO is the response shape for GET /api/clusters and
// GET /api/clusters/<name> — deliberately excludes driverOutputs and any
// kubeconfig data, see HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase 6.4.
type clusterDTO struct {
	Name               string             `json:"name"`
	Driver             string             `json:"driver"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration"`
	AccessMethod       string             `json:"accessMethod,omitempty"`
	AccessLastMinted   string             `json:"accessLastMinted,omitempty"`
}

func toClusterDTO(cd *hyvev1alpha1.ClusterDefinition) clusterDTO {
	return clusterDTO{
		Name:               cd.Name,
		Driver:             cd.Spec.Driver.Source,
		Conditions:         cd.Status.Conditions,
		ObservedGeneration: cd.Status.ObservedGeneration,
		AccessMethod:       cd.Status.Access.Method,
		AccessLastMinted:   cd.Status.Access.LastMinted,
	}
}

// registerClusterRoutes wires the /clusters endpoints onto mux — mounted
// under /api/ (and behind requireAuth+requireRole) by Server.Routes.
func (s *Server) registerClusterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /clusters", s.handleListClusters)
	mux.HandleFunc("GET /clusters/{name}", s.handleGetCluster)
	mux.HandleFunc("POST /clusters", s.handleCreateCluster)
	mux.HandleFunc("DELETE /clusters/{name}", s.handleDeleteCluster)
}

func (s *Server) handleListClusters(w http.ResponseWriter, r *http.Request) {
	var list hyvev1alpha1.ClusterDefinitionList
	// client.InNamespace is required, not optional: ClusterDefinition is a
	// namespaced resource, but hyve-api's own Role (deploy/helm/hyve-api's
	// rbac.yaml) is deliberately namespace-scoped too, not a ClusterRole —
	// an unscoped List() call is a genuine cluster-wide list attempt, which
	// that Role can never satisfy regardless of what it grants within
	// s.Namespace. Confirmed live: this exact mismatch 500'd every request
	// with no server-side log line at all until the error below was added.
	if err := s.Client.List(r.Context(), &list, client.InNamespace(s.Namespace)); err != nil {
		log.Printf("api: failed to list clusters: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list clusters")
		return
	}
	dtos := make([]clusterDTO, 0, len(list.Items))
	for i := range list.Items {
		dtos = append(dtos, toClusterDTO(&list.Items[i]))
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleGetCluster(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var cd hyvev1alpha1.ClusterDefinition
	if err := s.Client.Get(r.Context(), types.NamespacedName{Namespace: s.Namespace, Name: name}, &cd); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "cluster not found")
			return
		}
		log.Printf("api: failed to get cluster %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to get cluster")
		return
	}
	writeJSON(w, http.StatusOK, toClusterDTO(&cd))
}

// createClusterRequest reuses hyvev1alpha1.ClusterDefinitionSpec directly
// as the request body's spec shape — the Kubernetes API server's own CRD
// schema validation on the Create call below is "the CRD's own OpenAPI"
// validation the plan asks for; no second hand-rolled validator needed.
type createClusterRequest struct {
	Name string                             `json:"name"`
	Spec hyvev1alpha1.ClusterDefinitionSpec `json:"spec"`
}

func (s *Server) handleCreateCluster(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	var req createClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	cd := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: req.Name, Namespace: s.Namespace},
		Spec:       req.Spec,
	}
	if err := s.Client.Create(r.Context(), cd); err != nil {
		if apierrors.IsAlreadyExists(err) {
			writeError(w, http.StatusConflict, "cluster already exists")
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to create cluster: %v", err))
		return
	}
	writeJSON(w, http.StatusCreated, toClusterDTO(cd))
}

func (s *Server) handleDeleteCluster(w http.ResponseWriter, r *http.Request) {
	if !RequireRole(w, r, hyvev1alpha1.RoleAdmin) {
		return
	}
	name := r.PathValue("name")
	cd := &hyvev1alpha1.ClusterDefinition{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.Namespace}}
	if err := s.Client.Delete(r.Context(), cd); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "cluster not found")
			return
		}
		log.Printf("api: failed to delete cluster %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, "failed to delete cluster")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
