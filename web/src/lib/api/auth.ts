import { setSession, getSession } from '../authStore'
import type { Session } from '../session'
import { apiFetch, ApiError } from './client'

// Field names copied verbatim from internal/api/auth_handlers.go's
// loginResponse/refreshResponse — POST /auth/login and /auth/refresh live
// outside /api/ (they ARE the auth mechanism, see Server.Routes' own doc
// comment: refresh's whole point is to work after the access token has
// already expired, so it can't itself require one).
type LoginResponse = {
  accessToken: string
  accessTokenExpiresAt: string
  sessionToken: string
  sessionExpiresAt: string
}

export async function login(username: string, password: string): Promise<void> {
  const res = await fetch('/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  if (!res.ok) {
    const body = await res.json().catch(() => null)
    throw new ApiError(res.status, (body as { error?: string } | null)?.error ?? 'login failed')
  }
  const data = (await res.json()) as LoginResponse
  const sess: Session = {
    username,
    sessionToken: data.sessionToken,
    sessionExpiresAt: data.sessionExpiresAt,
    accessToken: data.accessToken,
    accessTokenExpiresAt: data.accessTokenExpiresAt,
  }
  setSession(sess)
}

export async function logout(): Promise<void> {
  const sess = getSession()
  if (sess) {
    // Best-effort, mirroring internal/api's handleLogout — always report
    // success locally even if the network call fails, since the local
    // state clear is what the user actually cares about.
    await fetch('/auth/logout', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessionToken: sess.sessionToken }),
    }).catch(() => {})
  }
  setSession(null)
}

export type Whoami = { username: string; role: string }

export const whoami = () => apiFetch<Whoami>('/whoami')

export const RoleAdmin = 'admin'
export const RoleReadOnly = 'read-only'
