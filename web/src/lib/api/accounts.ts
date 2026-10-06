import { apiDelete, apiFetch } from './client'

export type Account = { username: string; role: string; email?: string }
// password/email: required for a new user, omitted to add an existing one
// (a member of another organization), who keeps their own.
export type CreateAccountRequest = { username: string; role: string; password?: string; email?: string }
// emailSent: whether the new user's account-created email actually went out
// (false when SMTP isn't configured or the send failed — the account exists
// either way).
// existingUser: the username already had a login, so this only added them
// here (and sent no email).
export type CreatedAccount = Account & { emailSent: boolean; existingUser: boolean }
export type UpdateAccountPasswordRequest = { currentPassword?: string; newPassword: string }

// Role/email are undefined ("leave unchanged") vs "" (role: invalid, not
// meaningful; email: clear it) — mirrors the backend's updateAccountRequest
// pointer-field convention (nil vs a present-but-empty value), see
// internal/api/accounts.go's own doc comment on that type.
export type UpdateAccountRequest = {
  role?: string
  email?: string
  // Only meaningful when role moves a binding out of superadmin, which has
  // no tenant of its own to fall back to — see handleUpdateAccount's own
  // doc comment.
  namespace?: string
  environment?: string
}

export const accountsApi = {
  list: () => apiFetch<Account[]>('/accounts'),
  get: (username: string) => apiFetch<Account>(`/accounts/${encodeURIComponent(username)}`),
  create: (body: CreateAccountRequest) =>
    apiFetch<CreatedAccount>('/accounts', { method: 'POST', body: JSON.stringify(body) }),
  update: (username: string, body: UpdateAccountRequest) =>
    apiFetch<Account>(`/accounts/${encodeURIComponent(username)}`, { method: 'PATCH', body: JSON.stringify(body) }),
  delete: (username: string) => apiDelete(`/accounts/${encodeURIComponent(username)}`),
  updatePassword: (username: string, body: UpdateAccountPasswordRequest) =>
    apiFetch<void>(`/accounts/${encodeURIComponent(username)}/password`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
}
