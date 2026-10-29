// Package kube applies manifests and waits on objects with client-go, so
// hyve needs no kubectl.
package kube

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

// FieldManager is the server-side-apply field manager the hyve CLI uses.
const FieldManager = "hyve"

// Client is a dynamic client plus a REST mapper that refreshes on misses
// (CRDs appear while an install runs).
type Client struct {
	Dynamic dynamic.Interface
	// Core is the typed client, for core objects (Secrets, Pods).
	Core   kubernetes.Interface
	mapper *restmapper.DeferredDiscoveryRESTMapper
	cached discovery.CachedDiscoveryInterface
}

// New builds a Client for cfg.
func New(cfg *rest.Config) (*Client, error) {
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	cached := memory.NewMemCacheClient(disc)
	return &Client{Dynamic: dc, Core: cs, cached: cached, mapper: restmapper.NewDeferredDiscoveryRESTMapper(cached)}, nil
}

// Decode splits multi-document YAML into objects, skipping empty documents.
func Decode(manifest []byte) ([]*unstructured.Unstructured, error) {
	var objs []*unstructured.Unstructured
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096)
	for {
		var m map[string]any
		if err := dec.Decode(&m); errors.Is(err, io.EOF) {
			return objs, nil
		} else if err != nil {
			return nil, err
		}
		if len(m) == 0 {
			continue
		}
		objs = append(objs, &unstructured.Unstructured{Object: m})
	}
}

// Apply server-side-applies every object in manifest, in order.
func (c *Client) Apply(ctx context.Context, manifest []byte) error {
	objs, err := Decode(manifest)
	if err != nil {
		return err
	}
	for _, o := range objs {
		if err := c.ApplyObject(ctx, o); err != nil {
			return err
		}
	}
	return nil
}

// ApplyObject server-side-applies one object.
func (c *Client) ApplyObject(ctx context.Context, o *unstructured.Unstructured) error {
	ri, err := c.resource(o)
	if err != nil {
		return err
	}
	raw, err := o.MarshalJSON()
	if err != nil {
		return err
	}
	force := true
	_, err = ri.Patch(ctx, o.GetName(), types.ApplyPatchType, raw, metav1.PatchOptions{FieldManager: FieldManager, Force: &force})
	if err != nil {
		return fmt.Errorf("apply %s %s: %w", o.GetKind(), o.GetName(), err)
	}
	return nil
}

// DeleteObject deletes o (by kind, namespace and name); one that's already
// gone, or whose kind isn't served any more, is fine.
func (c *Client) DeleteObject(ctx context.Context, o *unstructured.Unstructured) error {
	ri, err := c.resource(o)
	if err != nil {
		if meta.IsNoMatchError(err) {
			return nil
		}
		return err
	}
	if err := ri.Delete(ctx, o.GetName(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete %s %s: %w", o.GetKind(), o.GetName(), err)
	}
	return nil
}

func (c *Client) resource(o *unstructured.Unstructured) (dynamic.ResourceInterface, error) {
	gvk := o.GroupVersionKind()
	m, err := c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if meta.IsNoMatchError(err) {
		c.mapper.Reset()
		m, err = c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", gvk, err)
	}
	if m.Scope.Name() == meta.RESTScopeNameNamespace {
		ns := o.GetNamespace()
		if ns == "" {
			ns = metav1.NamespaceDefault
		}
		return c.Dynamic.Resource(m.Resource).Namespace(ns), nil
	}
	return c.Dynamic.Resource(m.Resource), nil
}

// HasKind reports whether the API server serves group/version kind yet.
func (c *Client) HasKind(group, version, kind string) bool {
	c.mapper.Reset()
	_, err := c.mapper.RESTMapping(schema.GroupKind{Group: group, Kind: kind}, version)
	return err == nil
}

// Poll calls cond every interval until it returns true, an error, or
// timeout passes; each still-waiting result's message goes to progress.
func Poll(ctx context.Context, interval, timeout time.Duration, progress func(string), cond func(context.Context) (bool, string, error)) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	last := ""
	for {
		done, msg, err := cond(ctx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if msg != last && progress != nil && msg != "" {
			progress(msg)
			last = msg
		}
		select {
		case <-ctx.Done():
			if msg != "" {
				return fmt.Errorf("timed out (%s): %s", timeout, msg)
			}
			return fmt.Errorf("timed out after %s", timeout)
		case <-time.After(interval):
		}
	}
}

// ServesKind says whether the API server serves a kind, at any version.
func (c *Client) ServesKind(gk schema.GroupKind) bool {
	return len(c.Unserved([]schema.GroupKind{gk})) == 0
}

// Unserved are the kinds the API server doesn't serve, from one fresh
// discovery.
func (c *Client) Unserved(gks []schema.GroupKind) []schema.GroupKind {
	c.mapper.Reset()
	var out []schema.GroupKind
	for _, gk := range gks {
		if _, err := c.mapper.RESTMapping(gk); err != nil {
			out = append(out, gk)
		}
	}
	return out
}
