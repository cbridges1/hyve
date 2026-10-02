import { useSyncExternalStore } from 'react'
import { getActAsNamespace, setActAsNamespace, subscribe } from './actAsStore'

/** Current superadmin "act as" namespace override (null = Control plane) plus a setter, re-rendering the caller on change. */
export function useActAs(): [string | null, (namespace: string | null) => void] {
  const namespace = useSyncExternalStore(subscribe, getActAsNamespace)
  return [namespace, setActAsNamespace]
}
