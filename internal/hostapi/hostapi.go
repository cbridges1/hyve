// Package hostapi is the host's API for what hyve's UI can't do with the
// Kubernetes API alone, because it's hyve's own Go code: adding a Cluster
// API provider (which may mean applying hyve's embedded manifests),
// rendering a template, and converting old templates. The same code runs
// for 'hyve provider add', 'hyve render' and 'hyve import'. It also streams
// change notifications for the UI's live updates (watch.go).
//
// Requests are authenticated with the hyve session, as the
// /k8s/clusters/local proxy does. Adding a provider runs as the signed-in
// user (impersonated), so it needs the same RBAC as the CLI; rendering and
// converting only transform the YAML they're sent.
package hostapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"

	"github.com/cbridges1/hyve/internal/hyveimport"
	"github.com/cbridges1/hyve/internal/install"
	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/render"
	"github.com/cbridges1/hyve/internal/signin"
	"github.com/cbridges1/hyve/internal/templates"
)

// maxBody bounds what a request may send: YAML for render and import.
const maxBody = 8 << 20

// API serves /v1/hyve/*.
type API struct {
	// Config reaches the management cluster as the host; requests that
	// write impersonate the user with it.
	Config *rest.Config
	// Identify resolves a request's session (signin.Server.SessionIdentity).
	Identify func(*http.Request) (signin.Identity, bool)
	// DefaultTemplateNamespace is where templates without a namespace go.
	DefaultTemplateNamespace string
	// Dynamic, when set, replaces the impersonating client watches use
	// (tests).
	Dynamic func(signin.Identity) (dynamic.Interface, error)
	// Child, when set, reaches a child cluster as the user (through the
	// host's /k8s/clusters/<id> proxy), for watches with c=<id>.
	Child func(id string, ident signin.Identity) (*rest.Config, error)
}

// Register adds the routes.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/hyve/providers", a.authed(a.addProvider))
	mux.HandleFunc("POST /v1/hyve/render", a.authed(a.render))
	mux.HandleFunc("POST /v1/hyve/import", a.authed(a.importTemplates))
	mux.HandleFunc("GET /v1/hyve/watch", a.authed(a.watchStream))
}

func (a *API) authed(h func(http.ResponseWriter, *http.Request, signin.Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := a.Identify(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "sign in first (a cookie needs the "+signin.UIHeader+" header)")
			return
		}
		h(w, r, ident)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers in the host's error shape, which the UI reads.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"type": "error", "status": status, "code": http.StatusText(status), "message": message})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body: "+err.Error())
		return false
	}
	return true
}

// kubeAs is a client for the management cluster as the signed-in user.
func (a *API) kubeAs(ident signin.Identity) (*kube.Client, error) {
	cfg := rest.CopyConfig(a.Config)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: ident.KubeUser(), Groups: ident.KubeGroups()}
	return kube.New(cfg)
}

// statusOf maps a Kubernetes error to its HTTP status (403 for RBAC), or
// 400.
func statusOf(err error) int {
	var s apierrors.APIStatus
	if errors.As(err, &s) {
		if c := int(s.Status().Code); c >= 400 {
			return c
		}
	}
	return http.StatusBadRequest
}

func (a *API) addProvider(w http.ResponseWriter, r *http.Request, ident signin.Identity) {
	var req install.ProviderRequest
	if !decode(w, r, &req) {
		return
	}
	kc, err := a.kubeAs(ident)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	msg, err := install.AddProvider(r.Context(), kc, req)
	if err != nil {
		writeError(w, statusOf(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

// RenderRequest is POST /v1/hyve/render: what 'hyve render' takes, with
// the files' contents instead of their paths.
type RenderRequest struct {
	// YAML holds ClusterTemplates, shared Resources and ClusterClasses.
	YAML      string         `json:"yaml"`
	Template  string         `json:"template,omitempty"`
	Name      string         `json:"name,omitempty"`
	Env       string         `json:"env,omitempty"`
	Variables map[string]any `json:"variables,omitempty"`
}

func (a *API) render(w http.ResponseWriter, r *http.Request, _ signin.Identity) {
	var req RenderRequest
	if !decode(w, r, &req) {
		return
	}
	objs, err := kube.Decode([]byte(req.YAML))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		req.Name = "example"
	}
	if req.Env == "" {
		req.Env = install.DefaultEnvironment
	}
	res, err := render.Render(r.Context(), objs, render.Options{
		Template: req.Template, Name: req.Name, Env: req.Env, Variables: req.Variables,
		TemplateNamespace: a.DefaultTemplateNamespace,
		Prepare: func(t *templates.Template) error {
			for _, e := range t.Spec.Resources {
				if p, _ := e.Bundle["path"].(string); p != "" {
					return fmt.Errorf("resource %s: 'bundle: {path: ...}' reads files next to the template, which only the CLI can do", e.Name)
				}
			}
			return nil
		},
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	docs := []map[string]any{res.Cluster.Object}
	for _, o := range res.Fleet {
		docs = append(docs, o.Object)
	}
	text, err := yamlDocs(docs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"objects": docs, "yaml": text, "notes": res.Notes})
}

// ImportRequest is POST /v1/hyve/import: old hyve Templates or herd
// ClusterTemplates, as YAML.
type ImportRequest struct {
	YAML              string `json:"yaml"`
	TemplateNamespace string `json:"templateNamespace,omitempty"`
	PrefixNamespace   bool   `json:"prefixNamespace,omitempty"`
}

// Imported is one conversion. The UI creates Template itself, as the
// user, and for a herd template labels its clusters.
type Imported struct {
	Source   string         `json:"source"`
	Kind     string         `json:"kind"`
	Name     string         `json:"name,omitempty"`
	Template map[string]any `json:"template,omitempty"`
	Notes    []string       `json:"notes,omitempty"`
	Skipped  string         `json:"skipped,omitempty"`
	// Herd is the herd ClusterTemplate's namespace and name: its clusters
	// carry herd.io/template=<name> there.
	Herd *struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	} `json:"herd,omitempty"`
}

func (a *API) importTemplates(w http.ResponseWriter, r *http.Request, _ signin.Identity) {
	var req ImportRequest
	if !decode(w, r, &req) {
		return
	}
	all, err := kube.Decode([]byte(req.YAML))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var objs []*unstructured.Unstructured
	for _, o := range all {
		if o.GetKind() == "Template" || hyveimport.IsHerdTemplate(o) {
			objs = append(objs, o)
		}
	}
	if len(objs) == 0 {
		writeError(w, http.StatusBadRequest, "no hyve Templates or herd ClusterTemplates found")
		return
	}
	ns := req.TemplateNamespace
	if ns == "" {
		ns = a.DefaultTemplateNamespace
	}
	var out []Imported
	for _, c := range hyveimport.ConvertAll(objs, ns, req.PrefixNamespace) {
		im := Imported{Source: strings.TrimPrefix(c.Source, "/"), Kind: c.Object.GetKind(), Notes: c.Notes, Skipped: c.Skipped}
		if c.Skipped == "" {
			if err := templates.Validate(c.Template); err != nil && !strings.Contains(err.Error(), "bundle.resources is empty") {
				im.Skipped = err.Error()
			} else if u, err := c.Template.ToUnstructured(); err != nil {
				im.Skipped = err.Error()
			} else {
				im.Name, im.Template = c.Template.Name, u.Object
			}
		}
		if c.Herd {
			im.Herd = &struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			}{c.Object.GetNamespace(), c.Object.GetName()}
		}
		out = append(out, im)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

func yamlDocs(docs []map[string]any) (string, error) {
	var b strings.Builder
	for i, d := range docs {
		if i > 0 {
			b.WriteString("---\n")
		}
		raw, err := yaml.Marshal(d)
		if err != nil {
			return "", err
		}
		b.Write(raw)
	}
	return b.String(), nil
}
