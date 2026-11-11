package hostapi

import (
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"

	"github.com/cbridges1/hyve/internal/delivery"
	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/signin"
)

// progressResponse is a Deployment's progress (`hyve deploy wait`, show).
type progressResponse struct {
	delivery.Progress
	Done bool `json:"done"`
}

// deploymentProgress is GET /v1/hyve/deployments/{namespace}/{kind}/{name}/progress:
// how far a Deployment's latest change has got on its clusters. That's
// read from BundleDeployments, which live in each cluster's Fleet
// namespace, where users have no access. So the host reads them, for
// anyone who can read the Deployment itself: the Deployment is read as
// the user first.
func (a *API) deploymentProgress(w http.ResponseWriter, r *http.Request, ident signin.Identity) {
	ns, kind, name := r.PathValue("namespace"), r.PathValue("kind"), r.PathValue("name")
	gvr, ok := resources.FleetGVRs[kind]
	if !ok {
		writeError(w, http.StatusBadRequest, "kind is HelmOp, GitRepo or Bundle")
		return
	}
	as, err := a.dynamicAs(ident)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o, err := as.Resource(gvr).Namespace(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		writeError(w, statusOf(err), err.Error())
		return
	}
	host, err := a.hostDynamic()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, err := delivery.Check(r.Context(), host, o)
	if err != nil {
		writeError(w, statusOf(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, progressResponse{Progress: p, Done: p.Done()})
}

// hostDynamic reaches the management cluster as the host.
func (a *API) hostDynamic() (dynamic.Interface, error) {
	if a.HostDynamic != nil {
		return a.HostDynamic, nil
	}
	return dynamic.NewForConfig(a.Config)
}
