import { apiFetch } from './client'
import type { AccessMethod, MintAccessMethodRequest, MintAccessMethodResponse } from './types'

export const accessMethodsApi = {
  list: () => apiFetch<AccessMethod[]>('/access-methods'),
  get: (name: string) => apiFetch<AccessMethod>(`/access-methods/${encodeURIComponent(name)}`),
  mint: (name: string, body: MintAccessMethodRequest) =>
    apiFetch<MintAccessMethodResponse>(`/access-methods/${encodeURIComponent(name)}/mint`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),
}
