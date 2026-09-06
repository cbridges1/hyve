import { apiFetch } from './client'
import type { CreateWorkflowRunRequest, WorkflowRunStatus } from './types'

export const workflowRunsApi = {
  create: (body: CreateWorkflowRunRequest) =>
    apiFetch<{ name: string }>('/workflow-runs', { method: 'POST', body: JSON.stringify(body) }),
  get: (name: string) => apiFetch<WorkflowRunStatus>(`/workflow-runs/${encodeURIComponent(name)}`),
}
