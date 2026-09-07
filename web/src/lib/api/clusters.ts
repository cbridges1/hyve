import { apiDelete, apiFetch } from './client'
import type { ClusterActivity, ClusterDefinitionSpec, ClusterResources, ClusterSummary, CreateClusterRequest } from './types'

export const clustersApi = {
  list: () => apiFetch<ClusterSummary[]>('/clusters'),
  get: (name: string) => apiFetch<ClusterSummary>(`/clusters/${encodeURIComponent(name)}`),
  resources: (name: string) => apiFetch<ClusterResources>(`/clusters/${encodeURIComponent(name)}/resources`),
  events: (name: string) => apiFetch<ClusterActivity>(`/clusters/${encodeURIComponent(name)}/events`),
  create: (body: CreateClusterRequest) =>
    apiFetch<ClusterSummary>('/clusters', { method: 'POST', body: JSON.stringify(body) }),
  update: (name: string, spec: ClusterDefinitionSpec) =>
    apiFetch<ClusterSummary>(`/clusters/${encodeURIComponent(name)}`, { method: 'PATCH', body: JSON.stringify({ spec }) }),
  delete: (name: string) => apiDelete(`/clusters/${encodeURIComponent(name)}`),
}
