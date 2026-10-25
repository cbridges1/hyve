package hostapi

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/cbridges1/hyve/internal/audit"
	"github.com/cbridges1/hyve/internal/shell"
	"github.com/cbridges1/hyve/internal/signin"
)

// The kubectl shell (internal/shell): POST /v1/hyve/shells starts one on
// a cluster, and GET /v1/hyve/shells/<cluster>/<id>/exec is its terminal,
// a WebSocket in Kubernetes' exec protocol. Closing the terminal ends the
// shell.

// ShellRequest is POST /v1/hyve/shells.
type ShellRequest struct {
	// Cluster is "local" or "<environment>.<name>".
	Cluster string `json:"cluster"`
}

// shellAdmin reaches a cluster as the host, for the shell's objects.
func (a *API) shellAdmin(ctx context.Context, cluster string) (*rest.Config, error) {
	if cluster == signin.LocalClusterID {
		return a.Config, nil
	}
	if a.ChildAdmin == nil {
		return nil, fmt.Errorf("this host can't reach child clusters")
	}
	return a.ChildAdmin(ctx, cluster)
}

// mayShell checks the user may use the cluster: get its Cluster (so it's
// one of theirs), or, for the management cluster, list its namespaces.
// kubectl in the shell is them, so this only decides who gets a pod.
func (a *API) mayShell(ctx context.Context, ident signin.Identity, cluster string) error {
	attrs := &authorizationv1.ResourceAttributes{Verb: "list", Resource: "namespaces"}
	what := "the management cluster"
	if cluster != signin.LocalClusterID {
		ns, name, ok := strings.Cut(cluster, ".")
		if !ok || ns == "" || name == "" {
			return fmt.Errorf("%q isn't a cluster ID (local or <environment>.<name>)", cluster)
		}
		attrs = &authorizationv1.ResourceAttributes{Verb: "get", Group: "cluster.x-k8s.io", Resource: "clusters", Namespace: ns, Name: name}
		what = "cluster " + cluster
	}
	cs, err := kubernetes.NewForConfig(a.Config)
	if err != nil {
		return err
	}
	res, err := cs.AuthorizationV1().SubjectAccessReviews().Create(ctx, &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{
		User: ident.KubeUser(), Groups: ident.KubeGroups(), ResourceAttributes: attrs,
	}}, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	if !res.Status.Allowed {
		return apierrors.NewForbidden(schema.GroupResource{Group: attrs.Group, Resource: attrs.Resource}, attrs.Name, fmt.Errorf("%s has no access to %s", ident.Username, what))
	}
	return nil
}

func (a *API) startShell(w http.ResponseWriter, r *http.Request, ident signin.Identity) {
	var req ShellRequest
	if !decode(w, r, &req) {
		return
	}
	if err := a.mayShell(r.Context(), ident, req.Cluster); err != nil {
		writeError(w, statusOf(err), err.Error())
		return
	}
	cfg, err := a.shellAdmin(r.Context(), req.Cluster)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sh, err := a.Shells.Start(r.Context(), cs, req.Cluster, ident, ident.Username)
	if err != nil {
		log.Printf("hostapi: starting a shell on %s for %s: %v", req.Cluster, ident.Username, err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sh)
}

func (a *API) shellExec(w http.ResponseWriter, r *http.Request, ident signin.Identity) {
	cluster, id := r.PathValue("cluster"), r.PathValue("id")
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		writeError(w, http.StatusBadRequest, "the terminal is a WebSocket")
		return
	}
	cfg, err := a.shellAdmin(r.Context(), cluster)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Someone else's shell is as good as missing.
	if owner, err := shell.Owner(r.Context(), cs, id); err != nil || owner != ident.Username {
		writeError(w, http.StatusNotFound, "no such shell (it ends when its terminal closes)")
		return
	}
	target, err := url.Parse(cfg.Host)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	c := rest.CopyConfig(cfg)
	// Upgrades need HTTP/1.1.
	c.NextProtos = []string{"http/1.1"}
	c.Timeout = 0
	rt, err := rest.TransportFor(c)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	exec, _ := url.Parse(shell.ExecPath(id))
	out := r.Clone(r.Context())
	out.Header.Del("Cookie")
	out.Header.Del("Authorization")
	for h := range out.Header {
		if strings.HasPrefix(strings.ToLower(h), "impersonate-") {
			out.Header.Del(h)
		}
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = strings.TrimSuffix(target.Path, "/") + exec.Path
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = exec.RawQuery
			pr.Out.Host = target.Host
		},
		Transport:     rt,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("hostapi: shell %s on %s: %v", id, cluster, err)
			writeError(w, http.StatusBadGateway, "the shell's pod didn't answer")
		},
	}
	aw, done := audit.Start(w, r, ident.KubeUser(), ident.KubeGroups(), cluster)
	rp.ServeHTTP(aw, out)
	done()
	// The terminal closed: the shell is over. The way to the cluster is
	// looked up again: the terminal may have closed because the agent's
	// tunnel went, and then the direct path is the one left.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	if cfg, err := a.shellAdmin(ctx, cluster); err == nil {
		if fresh, err := kubernetes.NewForConfig(cfg); err == nil {
			cs = fresh
		}
	}
	if err := shell.Stop(ctx, cs, id); err != nil {
		log.Printf("hostapi: stopping shell %s on %s: %v", id, cluster, err)
	}
}
