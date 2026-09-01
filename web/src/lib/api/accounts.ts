import { apiDelete, apiFetch } from './client'

export type Account = { username: string; role: string }
export type CreateAccountRequest = { username: string; password: string; role: string }

export const accountsApi = {
  list: () => apiFetch<Account[]>('/accounts'),
  create: (body: CreateAccountRequest) =>
    apiFetch<Account>('/accounts', { method: 'POST', body: JSON.stringify(body) }),
  delete: (username: string) => apiDelete(`/accounts/${encodeURIComponent(username)}`),
}
