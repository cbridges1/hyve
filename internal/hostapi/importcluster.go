package hostapi

import (
	"fmt"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/importer"
	"github.com/cbridges1/hyve/internal/signin"
	"github.com/cbridges1/hyve/internal/templates"
)

// ClusterImportRequest is POST /v1/hyve/clusters/import: what 'hyve cluster
// import' takes, with the kubeconfig's contents. The host reaches the
// cluster with it, so it must hold a token or certificate: one that runs a
// command (aws, gcloud) only works from the CLI.
type ClusterImportRequest struct {
	Name              string            `json:"name"`
	Environment       string            `json:"environment"`
	Kubeconfig        string            `json:"kubeconfig"`
	Context           string            `json:"context,omitempty"`
	APIServer         string            `json:"apiServer,omitempty"`
	Template          string            `json:"template,omitempty"`
	TemplateNamespace string            `json:"templateNamespace,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
}

var secretsGVR = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}

func (a *API) importCluster(w http.ResponseWriter, r *http.Request, ident signin.Identity) {
	var req ClusterImportRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Name == "" || req.Environment == "" || req.Kubeconfig == "" {
		writeError(w, http.StatusBadRequest, "name, environment and kubeconfig are required")
		return
	}
	ctx := r.Context()
	kc, err := a.kubeAs(ident)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	c := clusters.NewImported(req.Name, req.Environment)
	l := c.GetLabels()
	for k, v := range req.Labels {
		l[k] = v
	}
	c.SetLabels(l)
	if req.Template != "" {
		ns := req.TemplateNamespace
		if ns == "" {
			ns = a.DefaultTemplateNamespace
		}
		u, err := kc.Dynamic.Resource(templates.GVR).Namespace(ns).Get(ctx, req.Template, metav1.GetOptions{})
		if err != nil {
			writeError(w, statusOf(err), err.Error())
			return
		}
		t, err := templates.FromUnstructured(u)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		templates.Label(c, t)
	}

	// The user must be able to make what an import makes (Secrets
	// included) before the host reaches out to the cluster.
	dry := metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}
	if _, err := kc.Dynamic.Resource(clusters.ClusterGVR).Namespace(req.Environment).Create(ctx, c, dry); err != nil {
		writeError(w, statusOf(err), err.Error())
		return
	}
	probe := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": req.Name + "-kubeconfig", "namespace": req.Environment}}}
	if _, err := kc.Dynamic.Resource(secretsGVR).Namespace(req.Environment).Create(ctx, probe, dry); err != nil {
		writeError(w, statusOf(err), err.Error())
		return
	}

	raw, err := clientcmd.Load([]byte(req.Kubeconfig))
	if err != nil {
		writeError(w, http.StatusBadRequest, "the kubeconfig: "+err.Error())
		return
	}
	if req.Context != "" {
		raw.CurrentContext = req.Context
	}
	if cx := raw.Contexts[raw.CurrentContext]; cx != nil {
		if u := raw.AuthInfos[cx.AuthInfo]; u != nil && (u.Exec != nil || u.AuthProvider != nil) {
			writeError(w, http.StatusBadRequest, "this kubeconfig signs in by running a command, which only works on your machine: import it with 'hyve cluster import' instead")
			return
		}
	}
	cfg, err := clientcmd.NewDefaultClientConfig(*raw, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		writeError(w, http.StatusBadRequest, "the kubeconfig: "+err.Error())
		return
	}
	if req.APIServer != "" {
		// The host is what reaches the cluster, here too.
		cfg.Host = req.APIServer
	}
	access, err := importer.Prepare(ctx, cfg, req.APIServer)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := importer.Create(ctx, kc.Dynamic, c, access.Kubeconfig); err != nil {
		writeError(w, statusOf(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Imported cluster %s (Kubernetes %s) into environment %s", req.Name, access.Version, req.Environment)})
}
