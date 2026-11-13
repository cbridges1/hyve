// Talks to hyve's host: sign-in under /v1 and /v1-public, the management
// cluster's API at /k8s/clusters/local as the signed-in user, and what
// needs hyve's own code under /v1/hyve. The session is an HttpOnly cookie;
// every request carries X-Hyve-UI, which the host requires of
// cookie-authenticated writes.

import { clearAppCaches } from './pwa'

export const LOCAL = '/k8s/clusters/local'

export class ApiError extends Error {
  status: number
  body: unknown

  constructor(status: number, message: string, body: unknown) {
    super(message)
    this.status = status
    this.body = body
  }
}

let onUnauthorized = () => {}

export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

export type RequestOptions = { quiet401?: boolean; headers?: Record<string, string>; contentType?: string }

export async function request<T = any>(method: string, url: string, body?: unknown, opts: RequestOptions = {}): Promise<T> {
  const res = await fetch(url, {
    method,
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json',
      'X-Hyve-UI': '1',
      ...(body !== undefined ? { 'Content-Type': opts.contentType ?? 'application/json' } : {}),
      ...opts.headers,
    },
    body: body === undefined ? undefined : typeof body === 'string' ? body : JSON.stringify(body),
  })
  const text = await res.text()
  let data: any = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = text
  }
  if (!res.ok) {
    if (res.status === 401 && !opts.quiet401) onUnauthorized()
    // A Kubernetes Status, or the host's {code, message}.
    const message = (data && typeof data === 'object' && (data.message || data.code)) || `${res.status} ${res.statusText}`
    throw new ApiError(res.status, message, data)
  }
  return data as T
}

export type Me = {
  username: string
  displayName?: string
  groups: string[]
  version: string
  autoSignIn?: boolean
  // '' a sign-in, 'token' an API token, 'federated' a CI job's session.
  kind?: string
  expiresAt?: string
}

// An API token, as the host lists it; the token itself is shown once.
export type TokenInfo = {
  user: string
  name: string
  id: string
  description?: string
  createdBy?: string
  createdAt: string
  expiresAt: string
  lastUsed?: string
}

// API tokens (for scripts and CI). Your own are yours to manage; another
// user's take the users/tokens right (admins).
export const tokens = {
  list: (user?: string) => request<TokenInfo[]>('GET', `/v1/tokens${user ? `?user=${encodeURIComponent(user)}` : ''}`),
  create: (t: { user?: string; name: string; description?: string; expiresIn: string }) =>
    request<TokenInfo & { token: string }>('POST', '/v1/tokens', t),
  revoke: (user: string, name: string) => request('DELETE', `/v1/tokens/${encodeURIComponent(user)}/${encodeURIComponent(name)}`),
}

export type SessionInfo = {
  user: string
  id: string
  kind: string
  userAgent?: string
  address?: string
  createdAt: string
  expiresAt: string
  lastUsed?: string
  current?: boolean
}

// Signed-in sessions (browsers, CLIs, CI jobs). Your own are yours to
// manage; another user's take the users/sessions right (admins).
export const sessions = {
  list: (user?: string) => request<SessionInfo[]>('GET', `/v1/sessions${user ? `?user=${encodeURIComponent(user)}` : ''}`),
  revoke: (user: string, id: string) => request('DELETE', `/v1/sessions/${encodeURIComponent(user)}/${encodeURIComponent(id)}`),
  // Every session of user's but this one.
  revokeOthers: (user: string) => request('DELETE', `/v1/sessions/${encodeURIComponent(user)}`),
}

export type SignedIn = { token?: string; expiresAt?: string; userId?: string }

// Sign-in.
export const auth = {
  me: () => request<Me>('GET', '/v1/me', undefined, { quiet401: true }),
  login: (username: string, password: string, newPassword?: string) =>
    request<SignedIn>(
      'POST',
      '/v1-public/login',
      { type: 'localProvider', responseType: 'cookie', username, password, ...(newPassword ? { newPassword } : {}) },
      { quiet401: true },
    ),
  // `hyve ui` hands the browser a session in the URL's fragment.
  redeem: (token: string) => request('POST', '/v1-public/ui-session', { token }, { quiet401: true }),
  // Local mode: the host signs a browser on its machine in without a
  // password (404 elsewhere).
  local: () => request('POST', '/v1-public/local-session', {}, { quiet401: true }),
  // Signing out also clears what the app's service worker stored.
  logout: () => request('POST', '/v1/logout', {}, { quiet401: true }).finally(clearAppCaches),
  password: (username: string, currentPassword: string, newPassword: string) =>
    request('PUT', `/v1/users/${encodeURIComponent(username)}/password`, { currentPassword, newPassword }),
  // Emails a user a link and code to set their own password (onboarding,
  // or again); takes the right to set their password.
  invite: (username: string) => request('POST', `/v1/users/${encodeURIComponent(username)}/invite`, {}),
  // Forgot password: the host emails a code to the user a username or
  // address names (and says the same whether or not there is one).
  requestReset: (identifier: string) => request('POST', '/v1-public/password-reset', { identifier }, { quiet401: true }),
  confirmReset: (identifier: string, code: string, newPassword: string) =>
    request('POST', '/v1-public/password-reset/confirm', { identifier, code, newPassword }, { quiet401: true }),
}

// The host's SMTP relay, for password resets and account emails. Reading
// or changing it takes get or update on its Secret; the password never
// comes back, passwordSet says whether there is one.
export type EmailSettings = {
  host: string
  port: number
  username: string
  passwordSet: boolean
  useTLS: boolean
  skipVerify: boolean
  fromAddress: string
  fromName: string
  configured: boolean
}

export type EmailSettingsUpdate = Partial<Omit<EmailSettings, 'passwordSet' | 'configured'>> & { password?: string }

export const emailSettings = {
  get: () => request<EmailSettings>('GET', '/v1/settings/email'),
  update: (u: EmailSettingsUpdate) => request<EmailSettings>('PUT', '/v1/settings/email', u),
  test: (to: string) => request('POST', '/v1/settings/email/test', { to }),
}

export function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

export type AuthMethods = { passwords: boolean; oidc?: { displayName: string; startUrl: string } }

export function authMethods(): Promise<AuthMethods> {
  return request<AuthMethods>('GET', '/v1-public/auth-methods', undefined, { quiet401: true }).catch(() => ({ passwords: true }))
}
