// Talks to hyve's host: sign-in under /v1 and /v1-public, the management
// cluster's API at /k8s/clusters/local as the signed-in user, and what
// needs hyve's own code under /v1/hyve. The session is an HttpOnly cookie;
// every request carries X-Hyve-UI, which the host requires of
// cookie-authenticated writes.

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

export type Me = { username: string; displayName?: string; groups: string[]; version: string; autoSignIn?: boolean }

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
  logout: () => request('POST', '/v1/logout', {}, { quiet401: true }),
  password: (username: string, currentPassword: string, newPassword: string) =>
    request('PUT', `/v1/users/${encodeURIComponent(username)}/password`, { currentPassword, newPassword }),
}

export function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

export type AuthMethods = { passwords: boolean; oidc?: { displayName: string; startUrl: string } }

export function authMethods(): Promise<AuthMethods> {
  return request<AuthMethods>('GET', '/v1-public/auth-methods', undefined, { quiet401: true }).catch(() => ({ passwords: true }))
}
