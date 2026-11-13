// The environment (a namespace labelled hyve.io/environment) the pages
// show, or '' for all of them; remembered in the browser.
import { useEffect, useSyncExternalStore } from 'react'
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

// The pages whose content depends on the environment say so with
// useEnvironmentScope; the header shows the picker while one is open, so
// a page is never filtered by an environment it gives no way to change.
let scoped = 0
const scopeListeners = new Set<() => void>()

function subscribeScope(l: () => void) {
  scopeListeners.add(l)
  return () => scopeListeners.delete(l)
}

export function useEnvironmentScope() {
  useEffect(() => {
    scoped++
    scopeListeners.forEach((l) => l())
    return () => {
      scoped--
      scopeListeners.forEach((l) => l())
    }
  }, [])
}

export function useEnvironmentScoped() {
  return useSyncExternalStore(subscribeScope, () => scoped > 0)
}

// The environment hyve makes on install, and the CLI's when none is given.
export const DEFAULT_ENVIRONMENT = 'default'

// The host cluster (local) is in no environment; it's listed with all of
// them and with the default one, so another environment's list holds only
// its own clusters.
export const showsLocal = (env: string) => !env || env === DEFAULT_ENVIRONMENT
