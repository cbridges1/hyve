export type Theme = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'hyve-theme'

// Same tiny external-store pattern as authStore.ts: a module-level value
// notified through useSyncExternalStore, so the resolved theme is available
// to plain code (the media-query listener below) as well as components.
let current: Theme = readStored()
const listeners = new Set<() => void>()

function readStored(): Theme {
  const raw = localStorage.getItem(STORAGE_KEY)
  return raw === 'light' || raw === 'dark' || raw === 'system' ? raw : 'system'
}

function notify() {
  for (const l of listeners) l()
}

export function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function getTheme(): Theme {
  return current
}

const media = window.matchMedia('(prefers-color-scheme: dark)')

function resolvedIsDark(theme: Theme): boolean {
  return theme === 'dark' || (theme === 'system' && media.matches)
}

function applyToDocument(theme: Theme) {
  const dark = resolvedIsDark(theme)
  document.documentElement.classList.toggle('dark', dark)
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
}

export function setTheme(theme: Theme): void {
  current = theme
  localStorage.setItem(STORAGE_KEY, theme)
  applyToDocument(theme)
  notify()
}

// index.html's inline script already applied the initial class before first
// paint (avoiding a flash); this just brings the store's in-memory value
// into agreement with whatever it landed on.
applyToDocument(current)

// Live-update while a 'system' preference is active and the OS setting
// changes out from under the open tab (no page reload).
media.addEventListener('change', () => {
  if (current === 'system') applyToDocument(current)
})
