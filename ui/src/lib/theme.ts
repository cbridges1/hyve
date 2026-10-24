// Light, dark, or the system's: the console's three-way theme, remembered
// in the browser (public/theme.js applies it before first paint).
import { useSyncExternalStore } from 'react'

export type Theme = 'light' | 'dark' | 'system'

const KEY = 'hyve-theme'
const media = window.matchMedia('(prefers-color-scheme: dark)')
const listeners = new Set<() => void>()

function stored(): Theme {
  try {
    const v = window.localStorage.getItem(KEY)
    return v === 'light' || v === 'dark' || v === 'system' ? v : 'system'
  } catch {
    return 'system'
  }
}

let current: Theme = stored()

function apply() {
  const dark = current === 'dark' || (current === 'system' && media.matches)
  document.documentElement.classList.toggle('dark', dark)
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
}

export function setTheme(t: Theme) {
  current = t
  try {
    window.localStorage.setItem(KEY, t)
  } catch {
    // Not remembered, but still applied.
  }
  apply()
  listeners.forEach((l) => l())
}

export function useTheme(): [Theme, (t: Theme) => void] {
  const t = useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => current,
  )
  return [t, setTheme]
}

media.addEventListener('change', () => current === 'system' && apply())
apply()
