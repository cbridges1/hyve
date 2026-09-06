import { apiFetch } from './client'
import type { Environment } from './types'

export const environmentsApi = {
  list: () => apiFetch<Environment[]>('/environments'),
  create: (name: string) => apiFetch<Environment>('/environments', { method: 'POST', body: JSON.stringify({ name }) }),
}
