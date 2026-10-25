// The cluster explorer's view of a cluster, a child or the management
// cluster (HYVE-PROPOSAL.md,
// "Cluster explorer"): everything goes through the host's
// /k8s/clusters/<id> as the signed-in user, so the child's RBAC decides
// what each person sees. The Kubernetes API does the work: discovery for
// the types, server-side tables for lists, server-side apply for edits.
import { dump } from 'js-yaml'
import { ApiError, request } from './api'
import type { Tone } from './format'
import { discover, urlFor, type Obj, type Schema } from './kube'

// The management cluster's ID in proxy paths and the watch stream.
export const LOCAL_CLUSTER = 'local'

export const clusterBase = (id: string) => `/k8s/clusters/${encodeURIComponent(id)}`

export function discoverCluster(id: string): Promise<Record<string, Schema>> {
  return discover(clusterBase(id))
}

// Types worth finding first, in this order; the rest follow by group.
export const COMMON = [
  'pod',
  'apps.deployment',
  'apps.statefulset',
  'apps.daemonset',
  'batch.job',
  'batch.cronjob',
  'service',
  'networking.k8s.io.ingress',
  'configmap',
  'secret',
  'persistentvolumeclaim',
  'namespace',
  'node',
  'event',
]

export function groupLabel(s: Schema) {
  return s.group || 'core'
}

export type TableColumn = { name: string; type: string; format?: string; description?: string; priority?: number }
export type TableRow = { cells: unknown[]; object: { metadata: Obj['metadata']; [k: string]: unknown } }
export type Table = { columnDefinitions: TableColumn[]; rows: TableRow[]; metadata?: { continue?: string; resourceVersion?: string } }

const TABLE = 'application/json;as=Table;g=meta.k8s.io;v=v1,application/json;as=Table;g=meta.k8s.io;v=v1beta1,application/json'

// One page of a list, as `kubectl get` shows it: the columns the server
// prints, a CRD's own printer columns included.
export async function listTable(
  id: string,
  s: Schema,
  opts: { namespace?: string; labelSelector?: string; fieldSelector?: string; limit?: number; continueToken?: string },
): Promise<Table> {
  const q = new URLSearchParams({ limit: String(opts.limit ?? 100), includeObject: 'Metadata' })
  if (opts.labelSelector) q.set('labelSelector', opts.labelSelector)
  if (opts.fieldSelector) q.set('fieldSelector', opts.fieldSelector)
  if (opts.continueToken) q.set('continue', opts.continueToken)
  const t = await request<any>('GET', `${urlFor(s, opts.namespace, undefined, clusterBase(id))}?${q}`, undefined, { headers: { Accept: TABLE } })
  if (t?.kind !== 'Table') {
    // A server that doesn't do tables: names and ages only.
    return {
      columnDefinitions: [
        { name: 'Name', type: 'string', format: 'name' },
        { name: 'Age', type: 'date' },
      ],
      rows: (t?.items || []).map((o: Obj) => ({ cells: [o.metadata.name, o.metadata.creationTimestamp], object: { metadata: o.metadata } })),
      metadata: t?.metadata,
    }
  }
  return t
}

export function getObject(id: string, s: Schema, namespace: string | undefined, name: string): Promise<Obj> {
  return request('GET', urlFor(s, namespace, name, clusterBase(id)))
}

export function deleteObject(id: string, s: Schema, namespace: string | undefined, name: string): Promise<unknown> {
  return request('DELETE', urlFor(s, namespace, name, clusterBase(id)))
}

export class ConflictError extends Error {}

// Saves YAML with server-side apply as field manager hyve-ui, so the API
// server validates it and conflicts with other managers are shown, not
// overwritten (unless force).
export async function applyYaml(id: string, s: Schema, namespace: string | undefined, name: string, yaml: string, force = false): Promise<Obj> {
  const q = new URLSearchParams({ fieldManager: 'hyve-ui' })
  if (force) q.set('force', 'true')
  try {
    return await request('PATCH', `${urlFor(s, namespace, name, clusterBase(id))}?${q}`, yaml, { contentType: 'application/apply-patch+yaml' })
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) throw new ConflictError(e.message)
    throw e
  }
}

// An object as YAML for editing: without managedFields, status, or what
// the server sets.
export function editableYaml(o: Obj): string {
  const c = JSON.parse(JSON.stringify(o))
  delete c.status
  if (c.metadata) {
    for (const k of ['managedFields', 'resourceVersion', 'uid', 'creationTimestamp', 'generation', 'selfLink']) delete c.metadata[k]
    if (c.metadata.annotations) {
      delete c.metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']
      if (!Object.keys(c.metadata.annotations).length) delete c.metadata.annotations
    }
  }
  return dump(c, { lineWidth: 120, noRefs: true })
}

// An object as YAML to read; a Secret's values are masked unless shown.
export function viewYaml(o: Obj, showSecrets = false): string {
  const c = JSON.parse(JSON.stringify(o))
  if (c.metadata) delete c.metadata.managedFields
  if (c.kind === 'Secret' && !showSecrets) {
    for (const k of ['data', 'stringData']) if (c[k]) for (const key of Object.keys(c[k])) c[k][key] = '••••••••'
  }
  return dump(c, { lineWidth: 120, noRefs: true })
}

// The object's events, newest last.
export async function eventsFor(id: string, events: Schema | undefined, o: Obj): Promise<Obj[]> {
  if (!events) return []
  const fs = [`involvedObject.name=${o.metadata.name}`, o.metadata.uid ? `involvedObject.uid=${o.metadata.uid}` : ''].filter(Boolean).join(',')
  const q = new URLSearchParams({ fieldSelector: fs })
  const list = await request<any>('GET', `${urlFor(events, o.metadata.namespace, undefined, clusterBase(id))}?${q}`)
  return (list.items || []).sort((a: any, b: any) =>
    String(a.lastTimestamp || a.eventTime || a.metadata.creationTimestamp).localeCompare(String(b.lastTimestamp || b.eventTime || b.metadata.creationTimestamp)),
  )
}

// Follows a container's log, calling onText with each chunk, until the
// signal aborts or the log ends.
export async function followLog(
  id: string,
  namespace: string,
  pod: string,
  opts: { container?: string; previous?: boolean; tailLines?: number },
  onText: (s: string) => void,
  signal: AbortSignal,
): Promise<void> {
  const q = new URLSearchParams({ follow: 'true', tailLines: String(opts.tailLines ?? 500) })
  if (opts.container) q.set('container', opts.container)
  if (opts.previous) q.set('previous', 'true')
  const res = await fetch(`${clusterBase(id)}/api/v1/namespaces/${encodeURIComponent(namespace)}/pods/${encodeURIComponent(pod)}/log?${q}`, {
    credentials: 'same-origin',
    headers: { 'X-Hyve-UI': '1' },
    signal,
  })
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`
    try {
      msg = (await res.json()).message || msg
    } catch {
      // Not JSON: keep the status.
    }
    throw new Error(msg)
  }
  const reader = res.body!.getReader()
  const dec = new TextDecoder()
  for (;;) {
    const { done, value } = await reader.read()
    if (done) return
    onText(dec.decode(value, { stream: true }))
  }
}

// The exec WebSocket's URL: a login shell, bash if the image has it.
export function execURL(id: string, namespace: string, pod: string, container: string): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const q = new URLSearchParams({ container, stdin: 'true', stdout: 'true', stderr: 'true', tty: 'true' })
  for (const c of ['/bin/sh', '-c', 'TERM=xterm-256color; export TERM; if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi']) q.append('command', c)
  return `${proto}//${window.location.host}${clusterBase(id)}/api/v1/namespaces/${encodeURIComponent(namespace)}/pods/${encodeURIComponent(pod)}/exec?${q}`
}

// Changes on a cluster, through the host's watch stream (c=<id>):
// calls onChange (debounced) whenever the watched list changes.
export function watchList(id: string, s: Schema, namespace: string | undefined, onChange: () => void): () => void {
  const w = `${s.group}/${s.version}/${s.resource}/${s.namespaced ? namespace || '' : ''}/`
  const src = new EventSource(`/v1/hyve/watch?c=${encodeURIComponent(id)}&w=${encodeURIComponent(w)}`, { withCredentials: true })
  let timer: ReturnType<typeof setTimeout> | null = null
  let opened = false
  const fire = () => {
    if (!timer) timer = setTimeout(() => ((timer = null), onChange()), 600)
  }
  src.onopen = () => {
    if (opened) fire()
    opened = true
  }
  src.onmessage = (m) => {
    try {
      const ev = JSON.parse(m.data)
      if (ev.type !== 'ERROR') fire()
    } catch {
      // Heartbeats aren't JSON.
    }
  }
  return () => {
    src.close()
    if (timer) clearTimeout(timer)
  }
}

// A table cell as text.
export function cellText(v: unknown): string {
  if (v === null || v === undefined) return ''
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

// How a status-like cell reads at a glance: a pod's Status, a PVC's
// Phase, a condition's True/False, a READY count.
const GOOD = /^(running|ready|true|active|bound|available|succeeded|healthy|deployed|provisioned|established|normal)$/i
const BUSY = /^(pending|containercreating|podinitializing|terminating|progressing|provisioning|updating|unknown|waiting|init:.*)$/i
const BAD = /(error|failed|crashloopbackoff|imagepullbackoff|errimagepull|oomkilled|evicted|false|notready|lost|unhealthy|warning)/i
const STATUS_COLUMNS = /^(status|phase|state|ready|available|conditions?|health)$/i

export function cellTone(column: string, text: string): Tone | null {
  if (!text || !STATUS_COLUMNS.test(column)) return null
  const count = /^(\d+)\/(\d+)$/.exec(text)
  if (count) return count[1] === count[2] ? 'good' : 'busy'
  if (GOOD.test(text)) return 'good'
  if (/^completed?$/i.test(text)) return 'idle'
  if (BUSY.test(text)) return 'busy'
  if (BAD.test(text)) return 'bad'
  return 'idle'
}

// Columns that are short and read best on one line.
export function isCompactColumn(c: TableColumn): boolean {
  return c.type === 'integer' || c.type === 'number' || c.type === 'date' || /^(age|ready|restarts|status|phase|type|count)$/i.test(c.name)
}
