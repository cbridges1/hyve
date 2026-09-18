import { apiDelete, apiFetch } from './client'
import type { ClusterActivity, ClusterDefinitionSpec, ClusterResources, ClusterSummary, CreateClusterRequest } from './types'

export const clustersApi = {
  list: () => apiFetch<ClusterSummary[]>('/clusters'),
  get: (name: string) => apiFetch<ClusterSummary>(`/clusters/${encodeURIComponent(name)}`),
  resources: (name: string) => apiFetch<ClusterResources>(`/clusters/${encodeURIComponent(name)}/resources`),
  events: (name: string, limit: number, offset: number) =>
    apiFetch<ClusterActivity>(`/clusters/${encodeURIComponent(name)}/events?limit=${limit}&offset=${offset}`),
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
  update: (name: string, spec: ClusterDefinitionSpec) =>
    apiFetch<ClusterSummary>(`/clusters/${encodeURIComponent(name)}`, { method: 'PATCH', body: JSON.stringify({ spec }) }),
  delete: (name: string) => apiDelete(`/clusters/${encodeURIComponent(name)}`),
}
