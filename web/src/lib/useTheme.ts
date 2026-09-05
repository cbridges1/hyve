import { useSyncExternalStore } from 'react'
import { getTheme, setTheme, subscribe, type Theme } from './themeStore'

/** Current theme preference ('light' | 'dark' | 'system') plus a setter, re-rendering the caller on change. */
export function useTheme(): [Theme, (theme: Theme) => void] {
  const theme = useSyncExternalStore(subscribe, getTheme)
  return [theme, setTheme]
}
