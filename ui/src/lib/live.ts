// Live updates: what the mounted components have read is watched through
// the host's /v1/hyve/watch (Kubernetes watches run as the user,
// multiplexed onto one server-sent event stream), and when something
// changes, the components that read it load their data again, quietly.
//
// Each reader (a useLoad call, lib/hooks.ts) registers what it read; the
// stream watches the union of every mounted reader's reads, so a page that
// reads Secrets one namespace at a time is told about those namespaces
// only.
import { allSchemas, forget, myNamespaces } from './kube'

// How long to gather changes before telling readers.
const SETTLE = 400
// The host's limit per stream.
const MAX = 32

type Interest = { type: string; namespace: string; name: string }
type Listener = (types: Set<string>) => void

const readers = new Map<number, Map<string, Interest>>()
const listeners = new Set<Listener>()
// Whole-kind watches the user may not run: watched per namespace instead.
const denied = new Set<string>()
const expanded = new Map<string, Interest[]>()
let source: EventSource | null = null
let targets: { w: string; t: Interest }[] = []
let reconnect: ReturnType<typeof setTimeout> | null = null
let changed = new Set<string>()
let notify: ReturnType<typeof setTimeout> | null = null
let stopped = false
let nextId = 1

const keyOf = (t: Interest) => `${t.type}@${t.namespace}#${t.name}`

function target(t: Interest): string | undefined {
  const s = allSchemas()[t.type]
  return s && `${s.group}/${s.version}/${s.resource}/${s.namespaced ? t.namespace : ''}/${t.name}`
}

function fire() {
  const types = changed
  changed = new Set()
  notify = null
  types.forEach((type) => forget(type))
  listeners.forEach((fn) => fn(types))
}

function changedType(type: string) {
  changed.add(type)
  if (!notify) notify = setTimeout(fire, SETTLE)
}

async function expandDenied(t: Interest) {
  denied.add(t.type)
  const nss = await myNamespaces().catch(() => [])
  expanded.set(
    t.type,
    nss.map((n) => ({ type: t.type, namespace: n.name, name: '' })),
  )
  schedule()
}

function wanted(): { w: string; t: Interest }[] {
  const all = new Map<string, Interest>()
  readers.forEach((m) => m.forEach((t, k) => all.set(k, t)))
  const out: { w: string; t: Interest }[] = []
  const push = (t: Interest) => {
    const w = target(t)
    if (w && !out.some((x) => x.w === w)) out.push({ w, t })
  }
  all.forEach((t) => {
    if (!t.namespace && !t.name && denied.has(t.type)) (expanded.get(t.type) || []).forEach(push)
    else push(t)
  })
  return out.slice(0, MAX)
}

function connect() {
  reconnect = null
  if (stopped) return
  const want = wanted()
  const same = want.length === targets.length && want.every((x, i) => x.w === targets[i].w)
  if (same && source) return
  source?.close()
  source = null
  targets = want
  if (!targets.length) return
  const url = `/v1/hyve/watch?${targets.map((x) => `w=${encodeURIComponent(x.w)}`).join('&')}`
  let opened = false
  const current = targets
  source = new EventSource(url, { withCredentials: true })
  // A reconnect after a drop may have missed changes.
  source.onopen = () => {
    if (opened) current.forEach((x) => changedType(x.t.type))
    opened = true
  }
  source.onmessage = (m) => {
    let ev: { w: number; type: string }
    try {
      ev = JSON.parse(m.data)
    } catch {
      return
    }
    const x = current[ev.w]
    if (!x) return
    if (ev.type === 'ERROR') {
      if (!x.t.namespace && !x.t.name && !denied.has(x.t.type)) expandDenied(x.t)
      return
    }
    changedType(x.t.type)
  }
}

function schedule() {
  if (!reconnect) reconnect = setTimeout(connect, 300)
}

// A reader: report reads with read(), drop them with clear() (before
// reading again) or close() (unmounted).
export function newReader() {
  const id = nextId++
  readers.set(id, new Map())
  return {
    read(type: string, namespace?: string, name?: string) {
      const m = readers.get(id)
      if (!m) return
      const t = { type, namespace: namespace || '', name: name || '' }
      const k = keyOf(t)
      if (!m.has(k)) {
        m.set(k, t)
        schedule()
      }
    },
    clear() {
      readers.get(id)?.clear()
    },
    reads(): Set<string> {
      return new Set([...(readers.get(id)?.values() || [])].map((t) => t.type))
    },
    close() {
      readers.delete(id)
      schedule()
    },
  }
}

export function subscribe(fn: Listener): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function startLive() {
  stopped = false
  schedule()
}

export function stopLive() {
  stopped = true
  source?.close()
  source = null
  targets = []
}
