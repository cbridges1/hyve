package controller

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// namespacePredicate matches only objects in targetNamespace — the
// mechanism SetupWithManagerNamed (both ClusterDefinitionReconciler's and
// WorkflowRunReconciler's) relies on for Milestone 6's "one reconciler
// instance per organization namespace, several sharing one manager/cache"
// design (see HYVE-ORGANIZATION-MODEL-PROPOSAL.md's "Per-organization
// reconciling cluster" section, nexus-config/docs): every instance shares
// the manager's one underlying watch/cache for its kind, so this predicate
// — not a separate cache — is what actually keeps one organization's
// reconciler from also picking up another's objects. Pulled out as its own
// named function (rather than an inline closure at each call site)
// specifically so it's directly unit-testable without a real manager or
// envtest.
func namespacePredicate(targetNamespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == targetNamespace
	})
}
