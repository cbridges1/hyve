package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// TestNamespacePredicate_OnlyMatchesTargetNamespace proves the exact
// mechanism SetupWithManagerNamed relies on for Milestone 6's "one
// reconciler instance per organization namespace, several sharing one
// manager" design (HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md's
// Milestone 6 "Tests" section: "a controller process given one
// reconciling-cluster ID only reconciles namespaces mapped to it"). Tested
// directly against the predicate, independent of any real manager/cache
// or ClusterDefinitionReconciler/WorkflowRunReconciler internals, which
// would only prove each instance CAN reconcile its own namespace — not
// that it's actually isolated from every other instance sharing the same
// underlying watch.
func TestNamespacePredicate_OnlyMatchesTargetNamespace(t *testing.T) {
	pred := namespacePredicate("widget")

	inTarget := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "widget", Name: "anything"}}
	inOther := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "acorn", Name: "anything"}}

	if !pred.Create(event.CreateEvent{Object: inTarget}) {
		t.Error("Create: object in the target namespace must match")
	}
	if pred.Create(event.CreateEvent{Object: inOther}) {
		t.Error("Create: object in a different namespace must not match — this is the whole isolation property")
	}

	if !pred.Update(event.UpdateEvent{ObjectOld: inTarget, ObjectNew: inTarget}) {
		t.Error("Update: object in the target namespace must match")
	}
	if pred.Update(event.UpdateEvent{ObjectOld: inOther, ObjectNew: inOther}) {
		t.Error("Update: object in a different namespace must not match")
	}

	if !pred.Delete(event.DeleteEvent{Object: inTarget}) {
		t.Error("Delete: object in the target namespace must match")
	}
	if pred.Delete(event.DeleteEvent{Object: inOther}) {
		t.Error("Delete: object in a different namespace must not match")
	}
}
