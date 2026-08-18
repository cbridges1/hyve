package api

import (
	"fmt"
	"log"
	"net/http"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

// authContextDTO is the response shape for
// GET /api/clusters/<name>/auth-context — the data a CLI needs to resolve
// and run a driver module's auth operation entirely client-side (see
// cmd/cluster/auth.go's cluster-mode path, and
// ClusterDefinitionSpec.Access's doc comment for why this is the default).
// Deliberately a separate, more sensitive endpoint from clusterDTO
// (GET /api/clusters/<name>) — that one intentionally excludes
// driverOutputs/params (see its own doc comment); this one exists
// specifically to expose them for the one legitimate purpose that needs
// them, and only for clusters actually using client-side auth.
type authContextDTO struct {
	DriverSource  string            `json:"driverSource"`
	DriverVersion string            `json:"driverVersion"`
	Region        string            `json:"region,omitempty"`
	Params        map[string]string `json:"params,omitempty"`
	DriverOutputs map[string]string `json:"driverOutputs,omitempty"`
}

// registerAuthContextRoutes wires GET /clusters/{name}/auth-context —
// mounted under /api/ (behind requireAuth+requireRole) by Server.Routes.
func (s *Server) registerAuthContextRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /clusters/{name}/auth-context", s.handleAuthContext)
}

// handleAuthContext only serves clusters using the default client-side auth
// method (spec.access.method unset) — a cluster that's opted into the
// AccessMethodModuleAuth override or AccessMethodTunnel is server-minted
// via GET /api/kubeconfig instead, and returning driver secrets here for
// those would just be a second, weaker-guaranteed way to reach the same
// access (no authorization check baked in, unlike the override path's
// module-side check — see moduleEnvForClusterDefinition). The API's own
// primary cluster (s.PrimaryClusterName) has no driver module at all —
// its kubeconfig is always server-minted via TokenRequest — so it's
// likewise rejected here.
func (s *Server) handleAuthContext(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if s.PrimaryClusterName != "" && name == s.PrimaryClusterName {
		writeError(w, http.StatusConflict, fmt.Sprintf("cluster %q is this API's own primary cluster — fetch its kubeconfig via GET /api/kubeconfig instead", name))
		return
	}

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
	if cd.Spec.Access.Method != "" {
		writeError(w, http.StatusConflict, fmt.Sprintf("cluster %q uses access.method %q, not client-side auth — fetch its kubeconfig via GET /api/kubeconfig instead", name, cd.Spec.Access.Method))
		return
	}

	writeJSON(w, http.StatusOK, authContextDTO{
		DriverSource:  cd.Spec.Driver.Source,
		DriverVersion: cd.Spec.Driver.Version,
		Region:        cd.Spec.Region,
		Params:        cd.Spec.Params,
		DriverOutputs: cd.Status.DriverOutputs,
	})
}
