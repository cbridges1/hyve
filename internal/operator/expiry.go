package operator

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cbridges1/hyve/internal/clusters"
	"github.com/cbridges1/hyve/internal/templates"
)

// expiredWait is how long an expired adopted cluster waits for CAPI to
// apply its template's own variables before it's deleted anyway.
const expiredWait = 10 * time.Minute

// Expiry deletes Clusters whose hyve.io/expires-at has passed.
//
// An adopted cluster's adopt.variables usually tell its provider to keep
// the cluster it adopted when it's deleted (deletionPolicy: Retain), which
// is right when someone deletes it in hyve but not when it expires: so an
// expired adopted cluster first gets its template's own values back for
// those variables (adopt.unset is left alone, so its size doesn't change),
// and is deleted once CAPI has applied them.
type Expiry struct {
	Client client.Client
	Now    func() time.Time
}

// Reconcile implements reconcile.Reconciler.
func (r *Expiry) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	lg := log.FromContext(ctx).WithValues("cluster", req.NamespacedName)
	c := object(clusterGVK)
	if err := r.Client.Get(ctx, req.NamespacedName, c); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if c.GetDeletionTimestamp() != nil {
		return reconcile.Result{}, nil
	}
	at, ok, err := templates.ExpiresAt(c)
	if err != nil {
		lg.Info("ignoring unparseable expiry", "error", err.Error())
		return reconcile.Result{}, nil
	}
	expired := c.GetAnnotations()[templates.AnnotationExpired]
	if !ok || at.After(r.Now()) {
		if expired != "" {
			// Extended (or made not to expire) before it was deleted.
			return reconcile.Result{}, r.adoptVariables(ctx, c, false)
		}
		if !ok {
			return reconcile.Result{}, nil
		}
		left := at.Sub(r.Now())
		if left > time.Hour {
			left = time.Hour // pick up changes made without an event
		}
		return reconcile.Result{RequeueAfter: left}, nil
	}
	if expired == "" && clusters.Origin(c) == clusters.OriginAdopted {
		t, err := getTemplate(ctx, r.Client, c)
		if err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
		if t != nil && t.Spec.Adopt != nil && len(t.Spec.Adopt.Variables) > 0 {
			lg.Info("adopted cluster expired; putting its template's variables back before deleting it", "expiresAt", at)
			return reconcile.Result{RequeueAfter: 10 * time.Second}, r.adoptVariables(ctx, c, true)
		}
	}
	if expired != "" && !topologyApplied(c) {
		since, err := time.Parse(time.RFC3339, expired)
		if err == nil && r.Now().Sub(since) < expiredWait {
			return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
		}
		lg.Info("CAPI hasn't applied the expired cluster's variables; deleting it anyway", "expiredAt", expired)
	}
	lg.Info("cluster expired, deleting", "expiresAt", at)
	if err := r.Client.Delete(ctx, c); err != nil && !apierrors.IsNotFound(err) {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

// adoptVariables takes the template's adopt.variables off an expired
// adopted cluster, marking it hyve.io/expired (expire), or puts them back
// and drops the mark.
func (r *Expiry) adoptVariables(ctx context.Context, c *unstructured.Unstructured, expire bool) error {
	t, err := getTemplate(ctx, r.Client, c)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	ann := c.GetAnnotations()
	if expire {
		ann[templates.AnnotationExpired] = r.Now().UTC().Format(time.RFC3339)
	} else {
		delete(ann, templates.AnnotationExpired)
	}
	c.SetAnnotations(ann)
	if t != nil && t.Spec.Adopt != nil {
		vars := clusters.Variables(c)
		for k, v := range t.Spec.Adopt.Variables {
			if tv, has := t.Spec.Variables[k]; expire && has && tv != nil {
				vars[k] = tv
			} else if expire || v == nil {
				delete(vars, k)
			} else {
				vars[k] = v
			}
		}
		if err := clusters.SetVariables(c, vars); err != nil {
			return err
		}
	}
	return r.Client.Update(ctx, c)
}

// topologyApplied reports whether CAPI has applied the Cluster's current
// generation to its topology's objects.
func topologyApplied(c *unstructured.Unstructured) bool {
	conds, _, _ := unstructured.NestedSlice(c.Object, "status", "conditions")
	for _, x := range conds {
		m, ok := x.(map[string]any)
		if !ok || m["type"] != "TopologyReconciled" {
			continue
		}
		if m["status"] != "True" {
			return false
		}
		g, found, _ := unstructured.NestedInt64(m, "observedGeneration")
		if !found {
			g, _, _ = unstructured.NestedInt64(c.Object, "status", "observedGeneration")
		}
		return g >= c.GetGeneration()
	}
	return false
}
