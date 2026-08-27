import { ensureValidAccessToken, setSession } from '../authStore'

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

// Relative paths only, no server URL to configure — this console is served
// from the same origin as the API it talks to (see internal/api/server.go's
// Routes: everything except /auth, /healthz, /docs, /openapi.yaml lives
// under /api/). Local dev without a same-origin API reachable directly
// needs vite.config.ts's dev-server proxy (VITE_API_PROXY_TARGET) instead
// of a client-configurable server URL.
const API_BASE = '/api'

/**
 * Fetches an authenticated /api/* endpoint, refreshing the access token
 * first if it's expired (see authStore.ensureValidAccessToken). A 401 from
 * the server itself (e.g. the session was revoked between our local
 * validity check and this request landing) clears the local session so the
 * UI falls back to the login screen rather than looping on stale
 * credentials.
 */
export async function apiFetch<T>(path: string, options: RequestInit = {}): Promise<T> {
  const token = await ensureValidAccessToken()
  if (!token) throw new ApiError(401, 'not authenticated')

  const res = await fetch(`${API_BASE}${path}`, {
    ...options,
    headers: {
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
      ...(options.headers ?? {}),
    },
  })

  if (res.status === 401) {
    setSession(null)
    throw new ApiError(401, 'session expired or revoked')
  }

  if (!res.ok) {
    const body = await res.json().catch(() => null)
    const message = (body as { error?: string } | null)?.error ?? res.statusText
    throw new ApiError(res.status, message)
  }
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

export async function apiDelete(path: string): Promise<void> {
  await apiFetch<void>(path, { method: 'DELETE' })
}

/** Like apiFetch, but for the one endpoint (GET /api/kubeconfig) that returns raw YAML text on success rather than JSON — errors are still {"error": ...} JSON, per writeError. */
export async function apiFetchText(path: string, options: RequestInit = {}): Promise<string> {
  const token = await ensureValidAccessToken()
  if (!token) throw new ApiError(401, 'not authenticated')

  const res = await fetch(`${API_BASE}${path}`, {
    ...options,
    headers: { Authorization: `Bearer ${token}`, ...(options.headers ?? {}) },
  })

  if (res.status === 401) {
    setSession(null)
    throw new ApiError(401, 'session expired or revoked')
  }
  if (!res.ok) {
    const body = await res.json().catch(() => null)
    const message = (body as { error?: string } | null)?.error ?? res.statusText
    throw new ApiError(res.status, message)
  }
  return res.text()
}
