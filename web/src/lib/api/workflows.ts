import { apiDelete, apiFetch } from './client'
import type { CreateWorkflowRequest, Workflow, WorkflowSpec } from './types'

export const workflowsApi = {
  list: () => apiFetch<Workflow[]>('/workflows'),
  get: (name: string) => apiFetch<Workflow>(`/workflows/${encodeURIComponent(name)}`),
  create: (body: CreateWorkflowRequest) =>
    apiFetch<Workflow>('/workflows', { method: 'POST', body: JSON.stringify(body) }),
  update: (name: string, spec: WorkflowSpec) =>
    apiFetch<Workflow>(`/workflows/${encodeURIComponent(name)}`, { method: 'PATCH', body: JSON.stringify({ spec }) }),
  delete: (name: string) => apiDelete(`/workflows/${encodeURIComponent(name)}`),
}
