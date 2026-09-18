import { apiDelete, apiFetch } from './client'
import type { CreateResourceRequest, ResourceItem, ResourceSpec } from './types'

export const resourcesApi = {
  list: () => apiFetch<ResourceItem[]>('/resources'),
  get: (name: string) => apiFetch<ResourceItem>(`/resources/${encodeURIComponent(name)}`),
  // env — see clustersApi.create's own comment.
  create: (body: CreateResourceRequest, env?: string) =>
    apiFetch<ResourceItem>(`/resources${env ? `?env=${encodeURIComponent(env)}` : ''}`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  update: (name: string, spec: ResourceSpec) =>
    apiFetch<ResourceItem>(`/resources/${encodeURIComponent(name)}`, { method: 'PATCH', body: JSON.stringify({ spec }) }),
  delete: (name: string) => apiDelete(`/resources/${encodeURIComponent(name)}`),
}
