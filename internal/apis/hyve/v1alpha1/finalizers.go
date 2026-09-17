package v1alpha1

// OrganizationNamespaceFinalizer was set on every organization's Namespace
// at creation time so a `DELETE /organizations/{name}`-issued namespace
// delete would wait for internal/controller's (now-retired)
// NamespaceReconciler to confirm every hyve-owned object inside it was
// gone before the Namespace itself could fully disappear — mirroring
// ClusterDefinitionFinalizer's own reconcile-then-remove pattern, one
// level up.
//
// Retired: once an organization could self-register a reconciling cluster
// by kubeconfig directly (see internal/api's handlePutOrgReconcilingCluster),
// nothing guaranteed a hyve-controller instance was ever actually deployed
// against that specific physical cluster — this finalizer could then never
// be cleared, and DELETE /organizations/{name} hung forever with the
// Namespace stuck Terminating. Confirmed live. New namespaces no longer
// carry this finalizer at all (ensureNamespace in internal/api/organizations.go
// no longer adds it); organization deletion now relies solely on
// Kubernetes' own native namespace-content cascade, the same mechanism
// every other kind of deletion in this application already goes through —
// nothing hyve-specific gates the Namespace object's own final removal
// anymore. The constant itself is kept only so
// trySweepOrganizationDeletion can recognize and strip a leftover one from
// a namespace created before this retirement, self-healing it rather than
// requiring a manual operator cleanup.
const OrganizationNamespaceFinalizer = "hyve.io/org-cleanup"
