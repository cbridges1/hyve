import { apiFetch } from './client'
import type { HyveConfig } from './types'

export const configApi = {
  get: () => apiFetch<HyveConfig>('/config'),
  update: (body: Omit<HyveConfig, 'exists'>) =>
    apiFetch<HyveConfig>('/config', { method: 'PATCH', body: JSON.stringify(body) }),
}
