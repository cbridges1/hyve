package hostapi

import (
	"net/http"

	"github.com/cbridges1/hyve/internal/release"
	"github.com/cbridges1/hyve/internal/signin"
)

// ReleaseRequest is POST /v1/hyve/clusters/{namespace}/{name}/release:
// 'hyve cluster release'. The host reaches the cluster to remove its
// agents, as hyve-controller does when it detaches an imported one; the
// rest runs as the user, who must be able to delete the cluster's objects.
type ReleaseRequest struct {
	Force  bool `json:"force,omitempty"`
	DryRun bool `json:"dryRun,omitempty"`
}

func (a *API) releaseCluster(w http.ResponseWriter, r *http.Request, ident signin.Identity) {
	var req ReleaseRequest
	if !decode(w, r, &req) {
		return
	}
	kc, err := a.kubeAs(ident)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := release.Release(r.Context(), kc, r.PathValue("namespace"), r.PathValue("name"), release.Options{Force: req.Force, DryRun: req.DryRun})
	if err != nil {
		writeError(w, statusOf(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
