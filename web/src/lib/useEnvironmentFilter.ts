import { useSyncExternalStore } from 'react'
import { getEnvironmentFilter, setEnvironmentFilter, subscribe } from './environmentFilterStore'

/** Currently selected environment filter ("" = All environments) plus a setter, shared across every resource-list page and re-rendering the caller on change. */
export function useEnvironmentFilter(): [string, (environment: string) => void] {
  const environment = useSyncExternalStore(subscribe, getEnvironmentFilter)
  return [environment, setEnvironmentFilter]
}
