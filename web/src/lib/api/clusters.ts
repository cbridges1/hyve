import { apiDelete, apiFetch } from './client'
import type { ClusterResources, ClusterSummary, CreateClusterRequest } from './types'

export const clustersApi = {
  list: () => apiFetch<ClusterSummary[]>('/clusters'),
  get: (name: string) => apiFetch<ClusterSummary>(`/clusters/${encodeURIComponent(name)}`),
  resources: (name: string) => apiFetch<ClusterResources>(`/clusters/${encodeURIComponent(name)}/resources`),
  create: (body: CreateClusterRequest) =>
    apiFetch<ClusterSummary>('/clusters', { method: 'POST', body: JSON.stringify(body) }),
  delete: (name: string) => apiDelete(`/clusters/${encodeURIComponent(name)}`),
}
