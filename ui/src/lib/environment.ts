// The environment (a namespace labelled hyve.io/environment) the pages
// show, or '' for all of them; remembered in the browser.
import { useSyncExternalStore } from 'react'
import { useLoad } from './hooks'
import { LABELS, TYPES } from './types'

const KEY = 'hyve-environment'
const listeners = new Set<() => void>()

function stored(): string {
  try {
    return window.localStorage.getItem(KEY) || ''
  } catch {
    return ''
  }
}

let current = stored()

export function setEnvironment(env: string) {
  current = env
  try {
    window.localStorage.setItem(KEY, env)
  } catch {
    // Not remembered, but still used.
  }
  listeners.forEach((l) => l())
}

export function useEnvironment(): [string, (env: string) => void] {
  const env = useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => current,
  )
  return [env, setEnvironment]
}

// The environment names: namespaces labelled hyve.io/environment.
export function useEnvironments() {
  return useLoad(
    async (r) =>
      (await r.findAll(TYPES.NAMESPACE).catch(() => []))
        .filter((ns) => ns.metadata.labels?.[LABELS.ENVIRONMENT] !== undefined)
        .map((ns) => ns.metadata.name)
        .sort(),
    [],
  )
}
