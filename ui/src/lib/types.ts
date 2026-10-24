// Type IDs (lib/kube.ts) and the labels and annotations hyve uses.

export const TYPES = {
  CLUSTER: 'cluster.x-k8s.io.cluster',
  CLUSTER_CLASS: 'cluster.x-k8s.io.clusterclass',
  MACHINE: 'cluster.x-k8s.io.machine',
  TEMPLATE: 'hyve.io.clustertemplate',
  RESOURCE: 'hyve.io.resource',
  CLUSTER_RESOURCE: 'hyve.io.clusterresource',
  CLUSTER_ACCESS: 'hyve.io.clusteraccess',
  USER: 'hyve.io.user',
  NAMESPACE: 'namespace',
  SECRET: 'secret',
  CONFIG_MAP: 'configmap',
  ROLE_BINDING: 'rbac.authorization.k8s.io.rolebinding',
  FLEET_HELM_OP: 'fleet.cattle.io.helmop',
  FLEET_GIT_REPO: 'fleet.cattle.io.gitrepo',
  FLEET_BUNDLE: 'fleet.cattle.io.bundle',
  FLEET_BUNDLE_DEPLOYMENT: 'fleet.cattle.io.bundledeployment',
  FLEET_CLUSTER: 'fleet.cattle.io.cluster',
} as const

export const LABELS = {
  TEMPLATE: 'hyve.io/template',
  TEMPLATE_NAMESPACE: 'hyve.io/template-namespace',
  ENVIRONMENT: 'hyve.io/environment',
  RESOURCE: 'hyve.io/resource',
  CLUSTER: 'hyve.io/cluster',
  OWNER_KIND: 'hyve.io/owner-kind',
  OWNER: 'hyve.io/owner',
  MANAGED_BY: 'app.kubernetes.io/managed-by',
  CLUSTER_INDEXED: 'auth.cattle.io/cluster-indexed',
} as const

export const ANNOTATIONS = {
  EXPIRES_AT: 'hyve.io/expires-at',
  CREATOR: 'hyve.io/creator',
  SOURCE: 'hyve.io/source',
  CLUSTER_LABELS: 'hyve.io/cluster-labels',
  DEFAULT_VERSION: 'hyve.io/default-version',
} as const

// "never" in hyve.io/expires-at: the cluster doesn't expire, whatever its
// template's schedule.
export const NEVER = 'never'

// Where templates and shared resources live unless chosen otherwise.
export const DEFAULT_TEMPLATE_NAMESPACE = 'capi-clusters'
