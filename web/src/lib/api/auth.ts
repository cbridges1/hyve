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

// resolveOrgToNamespace mirrors cmd/shared/login.go's ResolveOrgToNamespace
// exactly — today a trivial identity mapping (org name IS namespace name),
// kept as its own function so a future hosted-directory lookup replaces
// just this one spot, matching the CLI's own isolation of this seam. An
// empty org resolves to no namespace at all (a superadmin logging in with
// no tenant), not an empty-string namespace.
function resolveOrgToNamespace(org: string): string | undefined {
  return org.trim() || undefined
}

export async function login(username: string, password: string, org: string = ''): Promise<void> {
  const namespace = resolveOrgToNamespace(org)
  const res = await fetch('/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password, ...(namespace ? { namespace } : {}) }),
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
// Mirrors internal/apis/hyve/v1alpha1's RoleSuperadmin — cluster-scoped,
// the one tier that spans namespaces (see HYVE-MULTI-TENANCY-PLAN.md's
// "Phase 2" section). A superadmin has no "own" tenant namespace.
export const RoleSuperadmin = 'superadmin'
