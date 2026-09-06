import { apiDelete, apiFetch } from './client'
import type { AccessMethod, AccessMethodSpec, CreateAccessMethodRequest, MintAccessMethodRequest, MintAccessMethodResponse } from './types'

export const accessMethodsApi = {
  list: () => apiFetch<AccessMethod[]>('/access-methods'),
  get: (name: string) => apiFetch<AccessMethod>(`/access-methods/${encodeURIComponent(name)}`),
  create: (body: CreateAccessMethodRequest) =>
    apiFetch<AccessMethod>('/access-methods', { method: 'POST', body: JSON.stringify(body) }),
  update: (name: string, spec: AccessMethodSpec) =>
    apiFetch<AccessMethod>(`/access-methods/${encodeURIComponent(name)}`, { method: 'PATCH', body: JSON.stringify({ spec }) }),
  delete: (name: string) => apiDelete(`/access-methods/${encodeURIComponent(name)}`),
  mint: (name: string, body: MintAccessMethodRequest) =>
    apiFetch<MintAccessMethodResponse>(`/access-methods/${encodeURIComponent(name)}/mint`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),
}
