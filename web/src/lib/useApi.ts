import { useCallback, useEffect, useState } from 'react'
import { ApiError } from './api/client'

type AsyncState<T> = { data: T | null; loading: boolean; error: string | null }

/** Fetches `fetcher()` on mount and whenever `deps` changes; exposes `reload` for actions (create/delete) to refresh the list afterward. */
export function useApi<T>(fetcher: () => Promise<T>, deps: unknown[] = []): AsyncState<T> & { reload: () => void } {
  const [state, setState] = useState<AsyncState<T>>({ data: null, loading: true, error: null })
  const [tick, setTick] = useState(0)

  const load = useCallback(() => {
    let cancelled = false
    setState((s) => ({ ...s, loading: true, error: null }))
    fetcher()
      .then((data) => {
        if (!cancelled) setState({ data, loading: false, error: null })
      })
      .catch((err) => {
        if (!cancelled) {
          setState({ data: null, loading: false, error: err instanceof ApiError ? err.message : String(err) })
        }
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick])

  useEffect(() => load(), [load])

  return { ...state, reload: () => setTick((t) => t + 1) }
}

/** Like useApi, but re-fetches every `intervalMs` — for the cluster detail view's condition polling (see Phase 11's "no watch/SSE mechanism exists yet" note). */
export function usePolledApi<T>(fetcher: () => Promise<T>, intervalMs: number, deps: unknown[] = []): AsyncState<T> {
  const [state, setState] = useState<AsyncState<T>>({ data: null, loading: true, error: null })

  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>

    const run = () => {
      fetcher()
        .then((data) => {
          if (!cancelled) setState({ data, loading: false, error: null })
        })
        .catch((err) => {
          if (!cancelled) {
            setState((s) => ({ data: s.data, loading: false, error: err instanceof ApiError ? err.message : String(err) }))
          }
        })
        .finally(() => {
          if (!cancelled) timer = setTimeout(run, intervalMs)
        })
    }
    run()

    return () => {
      cancelled = true
      clearTimeout(timer)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  return state
}
