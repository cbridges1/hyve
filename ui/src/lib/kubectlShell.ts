// The kubectl shell: one at a time, docked under the page, kept open
// while the user moves between pages. The host runs it as a short-lived
// pod on the cluster (internal/shell); closing the terminal ends it.
import { useSyncExternalStore } from 'react'
import { request } from './api'

// cluster is "local" or "<environment>.<name>"; label is what to call it.
export type ShellTarget = { cluster: string; label: string }

export type StartedShell = { id: string; cluster: string; pod: string; expiresAt: string }

let current: ShellTarget | null = null
const listeners = new Set<() => void>()

export function openShell(t: ShellTarget) {
  current = t
  listeners.forEach((l) => l())
}

export function closeShell() {
  current = null
  listeners.forEach((l) => l())
}

export function useShellTarget(): ShellTarget | null {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => current,
  )
}

export function startShell(cluster: string): Promise<StartedShell> {
  return request<StartedShell>('POST', '/v1/hyve/shells', { cluster })
}

export function shellURL(s: StartedShell): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${window.location.host}/v1/hyve/shells/${encodeURIComponent(s.cluster)}/${encodeURIComponent(s.id)}/exec`
}

// Ends a shell nobody will use (the panel closed while it started): the
// host stops a shell when its terminal closes.
export function discardShell(s: StartedShell) {
  const ws = new WebSocket(shellURL(s), ['v5.channel.k8s.io'])
  ws.onopen = () => ws.close()
}
