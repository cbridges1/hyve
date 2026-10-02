import { useSyncExternalStore } from 'react'
import { getSession, subscribe } from './authStore'

/** Re-renders the calling component whenever the session changes (login, logout, or a background refresh). */
export function useSession() {
  return useSyncExternalStore(subscribe, getSession)
}
