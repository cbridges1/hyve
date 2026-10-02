// Same tiny external-store pattern as authStore.ts/actAsStore.ts/
// themeStore.ts — but this one holds no data of its own, just a version
// counter every organizations-list call site subscribes to (via useApi's
// own `deps` array) so a create/delete anywhere in the app invalidates
// every other already-mounted list, not just the one that made the call.
// Needed because useApi (lib/useApi.ts) is a fully local, per-call-site
// hook with no shared cache: OrganizationsPage's own list and AppShell's
// "Viewing" switcher each call organizationsApi.list() independently, so
// without this, creating an organization on the management page left the
// switcher showing a stale list until an unrelated remount (e.g. a full
// page reload) happened to refetch it — confirmed live.
let version = 0
const listeners = new Set<() => void>()

function notify() {
  for (const l of listeners) l()
}

export function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function getOrganizationsVersion(): number {
  return version
}

/** Call after any mutation (create/delete/patch) that changes the set of organizations or their contents. */
export function invalidateOrganizations(): void {
  version += 1
  notify()
}
