import { PRODUCT } from '@pkg/config/types';

// How long ago an RFC 3339 time was, as kubectl shows it.
export function age(ts) {
  if (!ts) {
    return '';
  }
  const s = Math.max(0, Math.floor((Date.now() - Date.parse(ts)) / 1000));

  if (s < 120) {
    return `${ s }s`;
  }
  if (s < 7200) {
    return `${ Math.floor(s / 60) }m`;
  }
  if (s < 172800) {
    return `${ Math.floor(s / 3600) }h`;
  }

  return `${ Math.floor(s / 86400) }d`;
}

export function detailRoute(type, m, query) {
  const md = m.metadata;

  return {
    name:   md.namespace ? `${ PRODUCT }-c-cluster-resource-namespace-id` : `${ PRODUCT }-c-cluster-resource-id`,
    params: {
      cluster: '_', resource: type, ...(md.namespace ? { namespace: md.namespace } : {}), id: md.name
    },
    query,
  };
}

export function createRoute(type, query) {
  return { name: `${ PRODUCT }-c-cluster-resource-create`, params: { cluster: '_', resource: type }, query };
}
