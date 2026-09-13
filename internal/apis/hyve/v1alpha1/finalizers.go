package v1alpha1

// OrganizationNamespaceFinalizer is set on every organization's Namespace
// at creation time (internal/api's ensureNamespace) so a `DELETE
// /organizations/{name}`-issued `kubectl delete namespace` marks it
// Terminating and runs Kubernetes' own native cascade through everything
// inside it, but the Namespace itself can't fully disappear until
// internal/controller's NamespaceReconciler confirms every hyve-owned
// object in it is gone and removes this finalizer — mirrors
// ClusterDefinitionFinalizer's own reconcile-then-remove pattern, one
// level up. See HYVE-ORGANIZATION-MODEL-PROPOSAL.md (nexus-config/docs)
// for the full design. Lives here (not internal/controller, where the
// reconciler that clears it does) because internal/api's ensureNamespace
// needs the same constant to set it in the first place, and this package
// is already the shared home both import.
const OrganizationNamespaceFinalizer = "hyve.io/org-cleanup"
