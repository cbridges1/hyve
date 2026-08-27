// Mirrors internal/session/session.go's Session shape and validity checks
// exactly, so the browser's notion of "is my session still good" matches
// the CLI's (cmd/shared.EnsureValidSession) byte for byte — same two-token
// model (short-lived accessToken sent on every /api/* call, long-lived
// sessionToken used only to mint new access tokens via POST /auth/refresh).
export type Session = {
  username: string
  sessionToken: string
  sessionExpiresAt: string // RFC3339
  accessToken: string
  accessTokenExpiresAt: string // RFC3339
}

const STORAGE_KEY = 'hyve-console-session'

export function loadSession(): Session | null {
  const raw = localStorage.getItem(STORAGE_KEY)
  if (!raw) return null
  try {
    return JSON.parse(raw) as Session
  } catch {
    return null
  }
}

export function saveSession(sess: Session): void {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(sess))
}

export function clearSession(): void {
  localStorage.removeItem(STORAGE_KEY)
}

export function accessTokenValid(sess: Session | null): boolean {
  return sess !== null && new Date(sess.accessTokenExpiresAt).getTime() > Date.now()
}

export function sessionValid(sess: Session | null): boolean {
  return sess !== null && new Date(sess.sessionExpiresAt).getTime() > Date.now()
}
