import { apiDelete, apiFetch } from './client'

export const secretsApi = {
  /** Key names only — readable by any authenticated role. */
  listNames: () => apiFetch<string[]>('/secrets'),
  /** Full key->value map — requires RoleAdmin server-side. */
  listValues: () => apiFetch<Record<string, string>>('/secrets?values=true'),
  get: (key: string) => apiFetch<{ key: string; value: string }>(`/secrets/${encodeURIComponent(key)}`),
  set: (key: string, value: string) =>
    apiFetch<void>(`/secrets/${encodeURIComponent(key)}`, { method: 'PUT', body: JSON.stringify({ value }) }),
  unset: (key: string) => apiDelete(`/secrets/${encodeURIComponent(key)}`),
}
