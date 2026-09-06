const STORAGE_KEY = 'hyve-act-as-namespace'

// Same tiny external-store pattern as authStore.ts/themeStore.ts: a
// module-level value notified through useSyncExternalStore. null means
// "Control plane" — no X-Hyve-Act-As-Namespace header sent, the
// superadmin's own default view (see internal/api/server.go's
// TenantNamespace, which honors this header only for a superadmin caller —
// an ordinary admin's requests never carry it at all, see apiFetch below).
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
