export const PRODUCT = 'hyve';

export const HYVE = {
  TEMPLATE:         'hyve.io.clustertemplate',
  RESOURCE:         'hyve.io.resource',
  CLUSTER_RESOURCE: 'hyve.io.clusterresource',
  CREATE_CLUSTER:   'hyve-create-cluster',
};

export const CAPI_CLUSTER = 'cluster.x-k8s.io.cluster';
export const CAPI_CLUSTER_CLASS = 'cluster.x-k8s.io.clusterclass';
export const NAMESPACE = 'namespace';

export const LABELS = {
  TEMPLATE:           'hyve.io/template',
  TEMPLATE_NAMESPACE: 'hyve.io/template-namespace',
  ENVIRONMENT:        'hyve.io/environment',
  RESOURCE:           'hyve.io/resource',
  CLUSTER:            'hyve.io/cluster',
  OWNER_KIND:         'hyve.io/owner-kind',
  OWNER:              'hyve.io/owner',
  MANAGED_BY:         'app.kubernetes.io/managed-by',
  CLUSTER_INDEXED:    'auth.cattle.io/cluster-indexed',
};

export const ANNOTATIONS = {
  EXPIRES_AT:      'hyve.io/expires-at',
  CREATOR:         'hyve.io/creator',
  SOURCE:          'hyve.io/source',
  CLUSTER_LABELS:  'hyve.io/cluster-labels',
  DEFAULT_VERSION: 'hyve.io/default-version',
};

// "never" in hyve.io/expires-at: the cluster doesn't expire, whatever its
// template's schedule.
export const NEVER = 'never';

// Where templates and shared resources live unless chosen otherwise.
export const DEFAULT_TEMPLATE_NAMESPACE = 'capi-clusters';
