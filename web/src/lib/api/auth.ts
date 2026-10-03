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

// login needs no organization: one login reaches every organization the
// user belongs to — the "Viewing" picker selects one per request (see
// apiFetch's X-Hyve-Organization header).
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

// requestPasswordReset always resolves — the backend deliberately
// responds 200 {"sent": true} whether or not identifier actually matched
// an account (see internal/api.handleRequestPasswordReset's own doc
// comment on why: a login endpoint's neighbor shouldn't reveal which
// usernames/emails exist). A network-level failure (server unreachable)
// still throws, so the caller can distinguish "couldn't even ask" from
// "asked, response is deliberately uninformative."
export async function requestPasswordReset(identifier: string): Promise<void> {
  const res = await fetch('/auth/request-password-reset', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ identifier }),
  })
  if (!res.ok) {
    const body = await res.json().catch(() => null)
    throw new ApiError(res.status, (body as { error?: string } | null)?.error ?? 'failed to request password reset')
  }
}

// resetPassword consumes a reset link's email/token query parameters (see
// internal/api.buildPasswordResetLink).
export async function resetPassword(email: string, token: string, newPassword: string): Promise<void> {
  const res = await fetch('/auth/reset-password', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, token, newPassword }),
  })
  if (!res.ok) {
    const body = await res.json().catch(() => null)
    throw new ApiError(res.status, (body as { error?: string } | null)?.error ?? 'failed to reset password')
  }
}

export type Whoami = {
  username: string
  role: string
  namespace: string
  // organization is the name of the organization owning namespace —
  // usually identical, but an organization can be renamed while its
  // namespace can't. Omitted when namespace has no organization.
  organization?: string
  // reconcilingCluster/migrating mirror Organization's own fields for this
  // caller's own namespace — omitted for a namespace with no registered
  // organization (see internal/api's whoamiResponse).
  reconcilingCluster?: string
  migrating?: boolean
  // organizations is every organization this login can act in (all of
  // them, as superadmin, for a superadmin) — the "Viewing" picker's
  // options.
  organizations: WhoamiOrganization[]
}

export type WhoamiOrganization = { name: string; namespace: string; role: string }

export const whoami = () => apiFetch<Whoami>('/whoami')

export const RoleAdmin = 'admin'
export const RoleReadOnly = 'read-only'
// Mirrors internal/apis/hyve/v1alpha1's RoleSuperadmin — cluster-scoped,
// the one tier that spans namespaces (see HYVE-MULTI-TENANCY-PLAN.md's
// "Phase 2" section). A superadmin has no "own" tenant namespace.
export const RoleSuperadmin = 'superadmin'

// roleLabel is display-only — the wire value stays "read-only" (backend
// role constant, API bodies, RequireRole checks) everywhere outside this
// one presentation layer. "Regular" reads better to an end user than
// "read-only" for what's really just "standard, non-admin access."
export function roleLabel(role: string): string {
  if (role === RoleReadOnly) return 'regular'
  return role
}
