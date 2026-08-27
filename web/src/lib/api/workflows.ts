import { apiDelete, apiFetch } from './client'
import type { CreateWorkflowRequest, Workflow } from './types'

export const workflowsApi = {
  list: () => apiFetch<Workflow[]>('/workflows'),
  get: (name: string) => apiFetch<Workflow>(`/workflows/${encodeURIComponent(name)}`),
  create: (body: CreateWorkflowRequest) =>
    apiFetch<Workflow>('/workflows', { method: 'POST', body: JSON.stringify(body) }),
  delete: (name: string) => apiDelete(`/workflows/${encodeURIComponent(name)}`),
}
