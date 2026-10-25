// Who's signed in, as a tiny external store components read with
// useSession().
import { useSyncExternalStore } from 'react'
import type { Me } from './api'

// canExplore: the user may list the management cluster's namespaces, so
// its explorer is worth a place in the sidebar.
export type Session = { user: Me | null; version: string; canExplore?: boolean }

let current: Session = { user: null, version: '' }
const listeners = new Set<() => void>()

export function getSession(): Session {
  return current
}

export function setSession(s: Partial<Session>) {
  current = { ...current, ...s }
  listeners.forEach((l) => l())
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => listeners.delete(l)
}

export function useSession(): Session {
  return useSyncExternalStore(subscribe, getSession)
}

// The server the CLI saved for this host, when 'hyve ui' opened the page:
// kubeconfigs from the UI run 'hyve token --server' for it. Without one,
// the page's own address is the host's.
const CLI_SERVER = 'hyve.cliServer'

export function rememberCLIServer(name: string, url: string) {
  try {
    window.sessionStorage.setItem(CLI_SERVER, JSON.stringify({ name, url }))
  } catch {
    // Private windows can refuse storage; kubeconfigs then use the page's address.
  }
}

export function cliServer(): { name: string; url: string } {
  try {
    const s = JSON.parse(window.sessionStorage.getItem(CLI_SERVER) || 'null')
    if (s?.name && s?.url) return s
  } catch {
    // As above.
  }
  return { name: window.location.origin, url: window.location.origin }
}
