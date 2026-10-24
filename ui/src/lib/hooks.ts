import { useCallback, useEffect, useRef, useState, type DependencyList } from 'react'
import { Reader } from './kube'
import { newReader, subscribe } from './live'

// The least time between two live refreshes of one component.
const MIN_GAP = 1500

export type Loaded<T> = {
  data: T | undefined
  error: Error | undefined
  loading: boolean
  // Loads again; quiet keeps the current data on screen meanwhile.
  reload: (quiet?: boolean) => Promise<void>
  setData: (d: T) => void
}

// Loads data with fn, again when deps change, and again (quietly) when
// anything fn read through its Reader changes on the cluster. Pass
// { live: false } for data that shouldn't refresh by itself (a form being
// edited).
export function useLoad<T>(fn: (r: Reader) => Promise<T>, deps: DependencyList, opts: { live?: boolean } = {}): Loaded<T> {
  const live = opts.live ?? true
  const [state, setState] = useState<{ data?: T; error?: Error; loading: boolean }>({ loading: true })
  const fnRef = useRef(fn)
  fnRef.current = fn
  const readerRef = useRef<ReturnType<typeof newReader> | null>(null)
  const seq = useRef(0)

  const run = useCallback(async (quiet: boolean) => {
    const my = ++seq.current
    if (!readerRef.current) readerRef.current = newReader()
    const rd = readerRef.current
    rd.clear()
    if (!quiet) setState((s) => ({ ...s, loading: true, error: undefined }))
    try {
      const data = await fnRef.current(new Reader((t, ns, n) => rd.read(t, ns, n)))
      if (my === seq.current) setState({ data, loading: false })
    } catch (e) {
      if (my === seq.current) setState((s) => ({ data: quiet ? s.data : undefined, error: e as Error, loading: false }))
    }
  }, [])

  useEffect(() => {
    run(false)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  useEffect(() => {
    if (!live) return
    let timer: ReturnType<typeof setTimeout> | null = null
    let last = 0
    const off = subscribe((types) => {
      const reads = readerRef.current?.reads()
      if (!reads || ![...types].some((t) => reads.has(t))) return
      if (timer) return
      timer = setTimeout(
        () => {
          timer = null
          last = Date.now()
          run(true)
        },
        Math.max(0, MIN_GAP - (Date.now() - last)),
      )
    })
    return () => {
      off()
      if (timer) clearTimeout(timer)
    }
  }, [live, run])

  useEffect(
    () => () => {
      readerRef.current?.close()
      readerRef.current = null
    },
    [],
  )

  return {
    data: state.data,
    error: state.error,
    loading: state.loading,
    reload: (quiet = false) => run(quiet),
    setData: (d: T) => setState({ data: d, loading: false }),
  }
}

// An async action's state: run(fn) sets busy and catches the error.
export function useAction() {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>('')
  const run = useCallback(async <T,>(fn: () => Promise<T>): Promise<T | undefined> => {
    setBusy(true)
    setError('')
    try {
      return await fn()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      return undefined
    } finally {
      setBusy(false)
    }
  }, [])
  return { busy, error, setError, run }
}
