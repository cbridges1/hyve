const STORAGE_KEY = 'hyve-act-as-namespace'

// Same tiny external-store pattern as authStore.ts/themeStore.ts: a
// module-level value notified through useSyncExternalStore: the namespace
// of the organization picked in "Viewing", sent as X-Hyve-Organization
// (see apiFetch). null sends nothing and lets the server pick — the
// control plane for a superadmin, a user's only (or first) organization
// otherwise (internal/api's resolveAccess).
let current: string | null = localStorage.getItem(STORAGE_KEY)
const listeners = new Set<() => void>()

function notify() {
  for (const l of listeners) l()
}

export function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function getActAsNamespace(): string | null {
  return current
}

export function setActAsNamespace(namespace: string | null): void {
  current = namespace
  if (namespace) {
    localStorage.setItem(STORAGE_KEY, namespace)
  } else {
    localStorage.removeItem(STORAGE_KEY)
  }
  notify()
}
