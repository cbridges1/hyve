package api

import (
	"encoding/json"
	"log"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// registerOrgConfigRoutes wires GET/PATCH /organizations/{name}/config —
// the organization-scoped counterpart to GET/PATCH /config
// (internal/api/config.go). That endpoint is hardcoded to s.Client/
// s.Namespace (the control plane's own home cluster and namespace) by
// design — HyveConfig there is genuinely install-wide, per that file's own
// doc comment — so it can't be reused for an organization's own reconciling
// cluster without conflating the two. This one instead resolves through
// resourceClient/org.Namespace, exactly like clusters.go/templates.go/etc.,
// reading or writing a HyveConfig singleton that lives in the
// organization's own namespace, on whichever cluster that organization
// currently reconciles against — never the control plane's own namespace,
// even for a superadmin.
func (s *Server) registerOrgConfigRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /organizations/{name}/config", s.handleGetOrgConfig)
	mux.HandleFunc("PATCH /organizations/{name}/config", s.handleUpdateOrgConfig)
}

// requireOrgOwnReconcilingCluster is requireOrgAccess plus one more gate:
// this endpoint only makes sense for an organization that actually has a
// reconciling cluster of its own (org.ReconcilingClusterID != nil) — an
// organization still on the control plane's own home cluster has no
// namespace-scoped HyveConfig of its own to configure here at all; that
// install-wide singleton is what GET/PATCH /config (superadmin, control
// plane view only) already manages. Writes its own error response and
// returns ok=false when access should be denied, matching requireOrgAccess.
func (s *Server) requireOrgOwnReconcilingCluster(w http.ResponseWriter, r *http.Request, orgName string) (hyveOrg orgReconcilingClusterOrg, ok bool) {
	org, ok := s.requireOrgAccess(w, r, orgName)
	if !ok {
		return orgReconcilingClusterOrg{}, false
	}
	if org.ReconcilingClusterID == nil {
		writeError(w, http.StatusBadRequest, "this organization has no reconciling cluster of its own — host cluster settings are managed by a superadmin from the control plane view")
		return orgReconcilingClusterOrg{}, false
	}
	return orgReconcilingClusterOrg{Name: org.Name, Namespace: org.Namespace}, true
}

// orgReconcilingClusterOrg carries just the two fields
// requireOrgOwnReconcilingCluster's callers actually need — not the full
// orgdb.Organization, so this file doesn't need to import orgdb only to
// pass one through.
type orgReconcilingClusterOrg struct {
	Name      string
	Namespace string
}

// handleGetOrgConfig mirrors handleGetConfig exactly (see that handler's
// own doc comment for the "exists: false, not 404" reasoning), reading
// from the organization's own namespace on its own reconciling cluster
// instead of the control plane's home cluster/namespace.
func (s *Server) handleGetOrgConfig(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireOrgOwnReconcilingCluster(w, r, r.PathValue("name"))
	if !ok {
		return
	}
	ctx := r.Context()
	targetClient, err := s.resourceClient(ctx, org.Namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for organization %q: %v", org.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to get config")
		return
	}
	var cfg hyvev1alpha1.HyveConfig
	err = targetClient.Get(ctx, types.NamespacedName{Namespace: org.Namespace, Name: s.configName()}, &cfg)
	if err != nil {
		if apierrors.IsNotFound(err) {
			writeJSON(w, http.StatusOK, hyveConfigDTO{Exists: false})
			return
		}
		log.Printf("api: failed to get HyveConfig %s/%s: %v", org.Namespace, s.configName(), err)
		writeError(w, http.StatusInternalServerError, "failed to get config")
		return
	}
	writeJSON(w, http.StatusOK, dtoFromHyveConfig(&cfg))
}

// handleUpdateOrgConfig mirrors handleUpdateConfig exactly (see that
// handler's own doc comment for the create-or-update upsert reasoning),
// writing to the organization's own namespace on its own reconciling
// cluster instead of the control plane's home cluster/namespace.
func (s *Server) handleUpdateOrgConfig(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireOrgOwnReconcilingCluster(w, r, r.PathValue("name"))
	if !ok {
		return
	}
	var req hyveConfigDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	targetClient, err := s.resourceClient(ctx, org.Namespace)
	if err != nil {
		log.Printf("api: failed to resolve reconciling cluster for organization %q: %v", org.Name, err)
		writeError(w, http.StatusInternalServerError, "failed to update config")
		return
	}
	key := types.NamespacedName{Namespace: org.Namespace, Name: s.configName()}

	var existing hyvev1alpha1.HyveConfig
	err = targetClient.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		cfg := &hyvev1alpha1.HyveConfig{
			ObjectMeta: metav1.ObjectMeta{Name: s.configName(), Namespace: org.Namespace},
			Spec:       specFromDTO(req),
		}
		if err := targetClient.Create(ctx, cfg); err != nil {
			log.Printf("api: failed to create HyveConfig %s/%s: %v", org.Namespace, s.configName(), err)
			writeError(w, http.StatusInternalServerError, "failed to create config")
			return
		}
		writeJSON(w, http.StatusOK, dtoFromHyveConfig(cfg))
		return
	}
	if err != nil {
		log.Printf("api: failed to get HyveConfig %s/%s: %v", org.Namespace, s.configName(), err)
		writeError(w, http.StatusInternalServerError, "failed to get config")
		return
	}

	existing.Spec = specFromDTO(req)
	if err := targetClient.Update(ctx, &existing); err != nil {
		log.Printf("api: failed to update HyveConfig %s/%s: %v", org.Namespace, s.configName(), err)
		writeError(w, http.StatusInternalServerError, "failed to update config")
		return
	}
	writeJSON(w, http.StatusOK, dtoFromHyveConfig(&existing))
}
