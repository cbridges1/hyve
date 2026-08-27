import {
  accessTokenValid,
  clearSession,
  loadSession,
  saveSession,
  sessionValid,
  type Session,
} from './session'

// A tiny external store (useSyncExternalStore-compatible) rather than
// threading the session through React context by hand — every API call
// needs the current access token, including ones made from plain async
// functions outside any component (e.g. a background refresh timer), so a
// module-level store that components can also subscribe to is a better fit
// here than context, which only reaches components.
let current: Session | null = loadSession()
const listeners = new Set<() => void>()

function notify() {
  for (const l of listeners) l()
}

export function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function getSession(): Session | null {
  return current
}

export function setSession(sess: Session | null): void {
  current = sess
  if (sess) saveSession(sess)
  else clearSession()
  notify()
}

/**
 * Mirrors cmd/shared.EnsureValidSession exactly: if the cached access token
 * is still valid, use it as-is; if it's expired but the underlying session
 * isn't, silently exchange it via POST /auth/refresh; otherwise the caller
 * needs to log in again. Returns null when nothing is logged in at all —
 * not an error, just "not authenticated."
 */
export async function ensureValidAccessToken(): Promise<string | null> {
  const sess = current
  if (!sess) return null
  if (accessTokenValid(sess)) return sess.accessToken

  if (!sessionValid(sess)) {
    setSession(null)
    return null
  }

  const res = await fetch(`/auth/refresh`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ sessionToken: sess.sessionToken }),
  })
  if (!res.ok) {
    // Session was revoked server-side (e.g. `hyve logout` elsewhere) or
    // otherwise rejected — nothing left to try but a fresh login.
    setSession(null)
    return null
  }
  const body = (await res.json()) as { accessToken: string; accessTokenExpiresAt: string }
  const updated: Session = {
    ...sess,
    accessToken: body.accessToken,
    accessTokenExpiresAt: body.accessTokenExpiresAt,
  }
  setSession(updated)
  return updated.accessToken
}
