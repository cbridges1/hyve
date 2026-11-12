package operator

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TemplateStatus marks each ClusterTemplate as seen: observedGeneration and
// a Ready condition. Fleet, which applies templates from git, counts one
// whose observedGeneration lags its generation as still progressing.
type TemplateStatus struct {
	Client client.Client
}

// Reconcile implements reconcile.Reconciler.
func (r *TemplateStatus) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	u := object(templateGVK)
	if err := r.Client.Get(ctx, req.NamespacedName, u); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if u.GetDeletionTimestamp() != nil {
		return reconcile.Result{}, nil
	}
	gen := u.GetGeneration()
	var conds []metav1.Condition
	if raw, ok, _ := unstructured.NestedSlice(u.Object, "status", "conditions"); ok {
		for _, c := range raw {
			if m, ok := c.(map[string]any); ok {
				if mc, err := conditionFromMap(m); err == nil {
					conds = append(conds, mc)
				}
			}
		}
	}
	observed, _, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
	ready := meta.FindStatusCondition(conds, "Ready")
	_, oldRows, _ := unstructured.NestedFieldNoCopy(u.Object, "status", "resources")
	_, oldMissing, _ := unstructured.NestedFieldNoCopy(u.Object, "status", "missingSecrets")
	if observed == gen && ready != nil && ready.Status == metav1.ConditionTrue && ready.ObservedGeneration == gen && !oldRows && !oldMissing {
		return reconcile.Result{}, nil
	}
	orig := u.DeepCopy()
	meta.SetStatusCondition(&conds, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Synced", ObservedGeneration: gen})
	condsRaw := make([]any, 0, len(conds))
	for _, c := range conds {
		m, err := conditionToMap(c)
		if err != nil {
			return reconcile.Result{}, err
		}
		condsRaw = append(condsRaw, m)
	}
	// The whole status, so rc.10's resources and missingSecrets go.
	u.Object["status"] = map[string]any{"observedGeneration": gen, "conditions": condsRaw}
	if err := r.Client.Status().Patch(ctx, u, client.MergeFrom(orig)); err != nil && !apierrors.IsNotFound(err) {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}
