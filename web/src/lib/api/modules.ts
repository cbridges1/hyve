import { apiFetch } from './client'
import type { Module } from './types'

// Read-only — see internal/api/modules.go's own doc comment: modules
// resolve automatically as a side effect of the controller reconciling a
// ClusterDefinition, there's no user-triggered create/delete to expose.
export const modulesApi = {
  list: () => apiFetch<Module[]>('/modules'),
  get: (name: string) => apiFetch<Module>(`/modules/${encodeURIComponent(name)}`),
}
