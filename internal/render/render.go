// Package render is what 'hyve render' prints, for the CLI and the host's
// UI API alike: the Cluster and Fleet objects hyve would create for a
// cluster made from a ClusterTemplate, from the objects alone (no cluster
// is contacted).
package render

import (
	"context"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/resources"
	"github.com/cbridges1/hyve/internal/templates"
)

// Options say which template to render, and for what cluster.
type Options struct {
	// Template picks one when the objects hold several.
	Template string
	// Name and Env are the cluster's.
	Name, Env string
	// Variables are ClusterClass variables, as --set gives them.
	Variables map[string]any
	// TemplateNamespace is where a ClusterTemplate without a namespace is.
	TemplateNamespace string
	// Prepare, if set, runs on the chosen template before it's validated
	// (the CLI reads 'bundle: {path: DIR}' manifests here).
	Prepare func(*templates.Template) error
}

// Result is a rendered cluster.
type Result struct {
	Cluster *unstructured.Unstructured
	// Fleet holds one Fleet object per template resource.
	Fleet []*unstructured.Unstructured
	// Notes say what was left out.
	Notes []string
}

// Render fills in a cluster from the ClusterTemplates, shared Resources
// and ClusterClasses among objs.
func Render(ctx context.Context, objs []*unstructured.Unstructured, o Options) (*Result, error) {
	var tpls []*templates.Template
	shared := map[string]*resources.Entry{}
	classes := map[string]*unstructured.Unstructured{}
	for _, obj := range objs {
		switch obj.GetKind() {
		case "ClusterTemplate":
			if obj.GetNamespace() == "" {
				obj.SetNamespace(o.TemplateNamespace)
			}
			t, err := templates.FromUnstructured(obj)
			if err != nil {
				return nil, err
			}
			tpls = append(tpls, t)
		case "Resource":
			e, err := resources.EntryFromSpec(obj)
			if err != nil {
				return nil, err
			}
			shared[obj.GetName()] = &e
		case "ClusterClass":
			classes[obj.GetName()] = obj
		}
	}
	var t *templates.Template
	for _, x := range tpls {
		if o.Template == "" || x.Name == o.Template {
			if t != nil && o.Template == "" {
				return nil, errors.New("there's more than one ClusterTemplate; pick one")
			}
			t = x
		}
	}
	if t == nil {
		if o.Template != "" {
			return nil, fmt.Errorf("no ClusterTemplate %q", o.Template)
		}
		return nil, errors.New("no ClusterTemplate found")
	}
	if o.Prepare != nil {
		if err := o.Prepare(t); err != nil {
			return nil, err
		}
	}
	if err := templates.Validate(t); err != nil {
		return nil, err
	}
	res := &Result{}
	class, ok := classes[t.Spec.ClusterClassRef.Name]
	if !ok {
		res.Notes = append(res.Notes, fmt.Sprintf("ClusterClass %s isn't given, so its defaults are left out", t.Spec.ClusterClassRef.Name))
		class = &unstructured.Unstructured{Object: map[string]any{}}
	}
	c := clusters.New(o.Name, o.Env)
	if err := clusters.SetVariables(c, o.Variables); err != nil {
		return nil, err
	}
	templates.Label(c, t)
	if err := templates.Fill(c, t, class, time.Now()); err != nil {
		return nil, err
	}
	res.Cluster = c
	lookup := func(_ context.Context, _, n string) (*resources.Entry, error) {
		if e, ok := shared[n]; ok {
			return e, nil
		}
		return nil, fmt.Errorf("Resource %s isn't given", n)
	}
	owner := resources.Owner{Kind: resources.OwnerTemplate, Name: t.Name, Namespace: t.Namespace}
	for _, e := range t.Spec.Resources {
		r, err := resources.Resolve(ctx, e, t.Namespace, lookup)
		if err != nil {
			return nil, err
		}
		obj, err := resources.Object(r, owner)
		if err != nil {
			return nil, err
		}
		res.Fleet = append(res.Fleet, obj)
	}
	return res, nil
}
