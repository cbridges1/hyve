const STORAGE_KEY = 'hyve-environment-filter'

// Same tiny external-store pattern as actAsStore.ts/themeStore.ts: a
// module-level value notified through useSyncExternalStore, so the
// environment filter selected on one resource-list page (Clusters,
// Templates, Workflows, Resources) is still selected after navigating to
// another one, rather than each page owning its own local useState('') that
// forgets the choice on every navigation — confirmed live as the
// behavior users actually expect once they've picked an environment to
// work in. "" means "All environments" (EnvironmentFilterSelect's own
// default), matching every page's existing !envFilter-means-unfiltered
// convention.
let current: string = localStorage.getItem(STORAGE_KEY) ?? ''
const listeners = new Set<() => void>()

function notify() {
  for (const l of listeners) l()
}

export function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function getEnvironmentFilter(): string {
  return current
}

export function setEnvironmentFilter(environment: string): void {
  current = environment
  if (environment) {
    localStorage.setItem(STORAGE_KEY, environment)
  } else {
    localStorage.removeItem(STORAGE_KEY)
  }
  notify()
}
