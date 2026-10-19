package operator

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cbridges1/hyve/internal/templates"
)

// Expiry deletes Clusters whose hyve.io/expires-at has passed.
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
	if !ok {
		return reconcile.Result{}, nil
	}
	left := at.Sub(r.Now())
	if left > 0 {
		if left > time.Hour {
			left = time.Hour // pick up changes made without an event
		}
		return reconcile.Result{RequeueAfter: left}, nil
	}
	lg.Info("cluster expired, deleting", "expiresAt", at)
	if err := r.Client.Delete(ctx, c); err != nil && !apierrors.IsNotFound(err) {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}
