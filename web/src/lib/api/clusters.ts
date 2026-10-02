import { apiDelete, apiFetch } from './client'
import type { ClusterActivity, ClusterDefinitionSpec, ClusterResources, ClusterSummary, CreateClusterRequest } from './types'

/**
 * Appends ?env= (or &env=) to path. A cluster's API name is its short name
 * plus the environment it belongs to: in an organization with environments
 * the real object is "<env>-<name>", so addressing it by short name without
 * env= finds nothing (internal/api's resolveAddressedName).
 */
export function withEnv(path: string, env?: string): string {
  if (!env) return path
  return `${path}${path.includes('?') ? '&' : '?'}env=${encodeURIComponent(env)}`
}

/** The console route for a cluster's detail page, carrying its environment. */
export function clusterPath(name: string, env?: string): string {
  return withEnv(`/clusters/${encodeURIComponent(name)}`, env)
}

export const clustersApi = {
  list: () => apiFetch<ClusterSummary[]>('/clusters'),
  get: (name: string, env?: string) => apiFetch<ClusterSummary>(withEnv(`/clusters/${encodeURIComponent(name)}`, env)),
  resources: (name: string, env?: string) =>
    apiFetch<ClusterResources>(withEnv(`/clusters/${encodeURIComponent(name)}/resources`, env)),
  events: (name: string, limit: number, offset: number, env?: string) =>
    apiFetch<ClusterActivity>(withEnv(`/clusters/${encodeURIComponent(name)}/events?limit=${limit}&offset=${offset}`, env)),
  // env selects which of the organization's environments a new cluster
  // lands in — required once a second environment exists (see
  // internal/api's resolveResourceEnvironment: a lone environment
  // resolves automatically with no ?env= needed, two or more is
  // "ambiguous" without one). Sent as a query param, not a body field —
  // matches every other environment-scoped create/get/patch/delete call.
  create: (body: CreateClusterRequest, env?: string) =>
    apiFetch<ClusterSummary>(`/clusters${env ? `?env=${encodeURIComponent(env)}` : ''}`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  update: (name: string, spec: ClusterDefinitionSpec, env?: string) =>
    apiFetch<ClusterSummary>(withEnv(`/clusters/${encodeURIComponent(name)}`, env), {
      method: 'PATCH',
      body: JSON.stringify({ spec }),
    }),
  delete: (name: string, env?: string) => apiDelete(withEnv(`/clusters/${encodeURIComponent(name)}`, env)),
}
