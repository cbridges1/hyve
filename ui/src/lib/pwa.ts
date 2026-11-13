// The UI as an installable app (a PWA): public/sw.js, registered in a
// production build, keeps the app shell for offline starts. Here: its
// registration, whether a new build is waiting (an "update available"
// prompt), whether the browser is online, and clearing what the worker
// stored, on sign-out.
import { useSyncExternalStore } from 'react'

// The worker's caches (sw.js's PREFIX).
const PREFIX = 'hyve-'

// unreachable: the browser is online but the host didn't answer at start
// (the stored shell opened without it).
type State = { waiting: ServiceWorker | null; online: boolean; unreachable: boolean }

let state: State = { waiting: null, online: typeof navigator === 'undefined' ? true : navigator.onLine, unreachable: false }
const listeners = new Set<() => void>()

function set(s: Partial<State>) {
  state = { ...state, ...s }
  listeners.forEach((l) => l())
}

export function usePWA(): State {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => state,
  )
}

window.addEventListener('online', () => set({ online: true }))
window.addEventListener('offline', () => set({ online: false }))

export function setUnreachable(unreachable: boolean) {
  set({ unreachable })
}

// Registers the worker. Dev (Vite) has none: it would serve stale modules.
// A browser that refuses one (no HTTPS, or a certificate it doesn't trust)
// runs the UI as a plain page.
export function registerServiceWorker() {
  if (!import.meta.env.PROD || !('serviceWorker' in navigator)) return
  const sw = navigator.serviceWorker
  // The new build took over (after applyUpdate): load it.
  let reloading = false
  sw.addEventListener('controllerchange', () => {
    if (reloading) return
    reloading = true
    window.location.reload()
  })
  window.addEventListener('load', () => {
    sw.register('/sw.js', { scope: '/' })
      .then((reg) => {
        // A worker waiting at start is a build that came out while the
        // app was closed; only one with a worker in control is an update.
        if (reg.waiting && sw.controller) set({ waiting: reg.waiting })
        reg.addEventListener('updatefound', () => {
          const next = reg.installing
          next?.addEventListener('statechange', () => {
            if (next.state === 'installed' && sw.controller) set({ waiting: next })
          })
        })
        // An installed app stays open for days: look for a new build hourly.
        setInterval(() => reg.update().catch(() => {}), 60 * 60 * 1000)
      })
      .catch(() => {})
  })
}

// Switches to the waiting build; the page reloads once it's in control.
export function applyUpdate() {
  state.waiting?.postMessage('skip-waiting')
}

// Removes everything the worker stored. Signing out calls it, so nothing
// of this session stays behind on a shared device.
export async function clearAppCaches() {
  if (!('caches' in window)) return
  try {
    const keys = await caches.keys()
    await Promise.all(keys.filter((k) => k.startsWith(PREFIX)).map((k) => caches.delete(k)))
  } catch {
    // Storage refused (a private window): there's nothing to clear.
  }
}
