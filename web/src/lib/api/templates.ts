import { apiDelete, apiFetch } from './client'
import type { ClusterDefinitionSpec, CreateTemplateRequest, RenderTemplateRequest, Template, TemplateSpec } from './types'

export const templatesApi = {
  list: () => apiFetch<Template[]>('/templates'),
  get: (name: string) => apiFetch<Template>(`/templates/${encodeURIComponent(name)}`),
  create: (body: CreateTemplateRequest) =>
    apiFetch<Template>('/templates', { method: 'POST', body: JSON.stringify(body) }),
  update: (name: string, spec: TemplateSpec) =>
    apiFetch<Template>(`/templates/${encodeURIComponent(name)}`, { method: 'PATCH', body: JSON.stringify({ spec }) }),
  delete: (name: string) => apiDelete(`/templates/${encodeURIComponent(name)}`),
  render: (name: string, body: RenderTemplateRequest) =>
    apiFetch<ClusterDefinitionSpec>(`/templates/${encodeURIComponent(name)}/render`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),
}
