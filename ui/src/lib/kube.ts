// The management cluster's Kubernetes API, as the signed-in user, through
// the host's /k8s/clusters/local. Kinds are named by type ID: the core
// group's singular name ("namespace", "secret"), or "<group>.<singular>"
// ("cluster.x-k8s.io.cluster", "hyve.io.clustertemplate"); see types.ts.
//
// Objects are plain JSON. Reads go through a Reader, which tells whoever
// made it (a page's useLoad, lib/hooks.ts) what was read, so the page is
// refreshed when any of it changes (lib/live.ts).
import { ApiError, LOCAL, request } from './api'

export type Metadata = {
  name: string
  namespace?: string
  labels?: Record<string, string>
  annotations?: Record<string, string>
  resourceVersion?: string
  creationTimestamp?: string
  deletionTimestamp?: string
  ownerReferences?: { apiVersion: string; kind: string; name: string; uid?: string; controller?: boolean }[]
  managedFields?: unknown
  uid?: string
  generation?: number
  finalizers?: string[]
}

export type Obj = {
  apiVersion?: string
  kind?: string
  metadata: Metadata
  spec?: any
  status?: any
  data?: Record<string, string>
  stringData?: Record<string, string>
  [k: string]: any
}

export type Schema = {
  id: string
  group: string
  version: string
  resource: string
  kind: string
  namespaced: boolean
  verbs: string[]
}

let schemas: Record<string, Schema> = {}

function typeId(group: string, singular: string) {
  return group ? `${group}.${singular}` : singular
}

// Discovery: every served kind, by type ID, at its preferred version, on
// the management cluster or (base /k8s/clusters/<id>) a child.
export async function discover(base = LOCAL): Promise<Record<string, Schema>> {
  const out: Record<string, Schema> = {}
  const add = (group: string, version: string, r: any) => {
    if (r.resource?.includes('/') || r.name?.includes('/')) return
    const resource = r.resource || r.name
    const kind = r.responseKind?.kind || r.kind
    const singular = r.singularResource || r.singularName || kind.toLowerCase()
    const id = typeId(group, singular)
    if (out[id]) return
    out[id] = {
      id,
      group,
      version,
      resource,
      kind,
      namespaced: r.scope ? r.scope === 'Namespaced' : !!r.namespaced,
      verbs: r.verbs || [],
    }
  }

  // Aggregated discovery (Kubernetes 1.30+): two requests for everything.
  const accept =
    'application/json;g=apidiscovery.k8s.io;v=v2;as=APIGroupDiscoveryList,application/json;g=apidiscovery.k8s.io;v=v2beta1;as=APIGroupDiscoveryList'
  try {
    const [core, groups] = await Promise.all([
      request('GET', `${base}/api`, undefined, { headers: { Accept: accept } }),
      request('GET', `${base}/apis`, undefined, { headers: { Accept: accept } }),
    ])
    if (core?.kind !== 'APIGroupDiscoveryList' || groups?.kind !== 'APIGroupDiscoveryList') throw new Error('no aggregated discovery')
    for (const g of [...core.items, ...groups.items]) {
      const v = g.versions?.[0]
      for (const r of v?.resources || []) add(g.metadata?.name || '', v.version, r)
    }
    return out
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) throw e
  }

  // The older way: one request per group.
  const core = await request('GET', `${base}/api/v1`)
  for (const r of core.resources) add('', 'v1', r)
  const groups = await request('GET', `${base}/apis`)
  await Promise.all(
    groups.groups.map(async (g: any) => {
      const v = g.preferredVersion.version
      const list = await request('GET', `${base}/apis/${g.name}/${v}`).catch(() => null)
      for (const r of list?.resources || []) add(g.name, v, r)
    }),
  )
  return out
}

export async function loadSchemas() {
  schemas = await discover()
}

export function allSchemas(): Record<string, Schema> {
  return schemas
}

export function schemaFor(type: string): Schema | undefined {
  return schemas[type]
}

function schemaOrThrow(type: string): Schema {
  const s = schemas[type]
  if (!s) throw new Error(`The server doesn't serve ${type}`)
  return s
}

export function can(type: string, verb: string): boolean {
  return !!schemas[type]?.verbs.includes(verb)
}

// The URL of a kind's collection (in ns, or across namespaces), or of one
// object, on the management cluster (or under base, for a child cluster).
export function urlFor(s: Schema, namespace?: string, name?: string, base = LOCAL): string {
  const root = s.group ? `${base}/apis/${s.group}/${s.version}` : `${base}/api/${s.version}`
  const ns = s.namespaced && namespace ? `/namespaces/${encodeURIComponent(namespace)}` : ''
  const n = name ? `/${encodeURIComponent(name)}` : ''
  return `${root}${ns}/${s.resource}${n}`
}

function apiVersionOf(s: Schema) {
  return s.group ? `${s.group}/${s.version}` : s.version
}

// The type ID an object was read as. Not part of the JSON.
const TYPE = Symbol('type')

export function typeOf(o: Obj): string | undefined {
  return (o as any)[TYPE]
}

function tag(type: string, o: Obj): Obj {
  Object.defineProperty(o, TYPE, { value: type, enumerable: false, configurable: true, writable: true })
  return o
}

export function idOf(o: Obj): string {
  const m = o.metadata
  return m.namespace ? `${m.namespace}/${m.name}` : m.name
}

const forbidden = (e: unknown) => e instanceof ApiError && e.status === 403

// The namespaces the user can work in, from the host, for when they can't
// list namespaces themselves.
let mine: Promise<{ name: string; labels?: Record<string, string> }[]> | null = null

export function myNamespaces() {
  if (!mine) {
    mine = request('GET', '/v1/namespaces').then((r: any) => r.items || [])
    mine.catch(() => {
      mine = null
    })
    setTimeout(() => {
      mine = null
    }, 30000)
  }
  return mine
}

// One namespace's objects when namespace is set, else every namespace's
// the user can list.
async function list(type: string, namespace?: string): Promise<Obj[]> {
  const s = schemaOrThrow(type)
  const wrap = (items: any[]) => items.map((o) => tag(type, { apiVersion: apiVersionOf(s), kind: s.kind, ...o }))

  if (namespace && s.namespaced) return wrap((await request('GET', urlFor(s, namespace))).items || [])
  try {
    return wrap((await request('GET', urlFor(s))).items || [])
  } catch (e) {
    if (!forbidden(e)) throw e
  }
  // Kubernetes answers a list across namespaces all or nothing; Rancher
  // filters it by what the user can see. Do the same, one namespace at a
  // time.
  const nss = await myNamespaces()
  if (type === 'namespace') return wrap(nss.map((n) => ({ metadata: { name: n.name, labels: n.labels || {} } })))
  if (!s.namespaced) return []
  const lists = await Promise.all(
    nss.map((n) =>
      request('GET', urlFor(s, n.name)).then(
        (r: any) => r.items || [],
        (e) => {
          if (forbidden(e) || (e instanceof ApiError && e.status === 404)) return []
          throw e
        },
      ),
    ),
  )
  return wrap(lists.flat())
}

// How long a list is reused: one page load asks for the same kinds from
// several components.
const LIST_TTL = 3000
const lists = new Map<string, { at: number; promise: Promise<Obj[]> }>()

export function forget(type: string) {
  for (const k of [...lists.keys()]) if (k === type || k.startsWith(`${type}@`)) lists.delete(k)
}

export type ReadListener = (type: string, namespace?: string, name?: string) => void

// Reads, reported to a listener (a page's live updates).
export class Reader {
  private listener: ReadListener
  constructor(listener: ReadListener = () => {}) {
    this.listener = listener
  }

  findAll(type: string, opts: { namespace?: string; force?: boolean } = {}): Promise<Obj[]> {
    this.listener(type, opts.namespace)
    const key = opts.namespace ? `${type}@${opts.namespace}` : type
    const hit = lists.get(key)
    if (hit && !opts.force && Date.now() - hit.at < LIST_TTL) return hit.promise
    const promise = list(type, opts.namespace)
    lists.set(key, { at: Date.now(), promise })
    promise.catch(() => lists.delete(key))
    return promise
  }

  async find(type: string, namespace: string | undefined, name: string): Promise<Obj> {
    const s = schemaOrThrow(type)
    this.listener(type, s.namespaced ? namespace : undefined, name)
    const o = await request('GET', urlFor(s, s.namespaced ? namespace : undefined, name))
    return tag(type, o)
  }

  // find, or null when it doesn't exist (or the user can't see it).
  async maybe(type: string, namespace: string | undefined, name: string): Promise<Obj | null> {
    try {
      return await this.find(type, namespace, name)
    } catch (e) {
      if (e instanceof ApiError && (e.status === 404 || e.status === 403)) return null
      throw e
    }
  }
}

// Reads that nobody watches: for actions (saving, deleting).
export const kube = new Reader()

// A new object of a kind, with apiVersion and kind filled in.
export function newObject(type: string, data: Partial<Obj> & { metadata: Metadata }): Obj {
  const s = schemaOrThrow(type)
  return tag(type, { apiVersion: apiVersionOf(s), kind: s.kind, ...data })
}

function clean(o: Obj): Obj {
  return JSON.parse(JSON.stringify(o))
}

// Creates the object, or replaces it when it has a resourceVersion.
export async function save(type: string, o: Obj): Promise<Obj> {
  const s = schemaOrThrow(type)
  const ns = o.metadata.namespace
  const body = { apiVersion: apiVersionOf(s), kind: s.kind, ...clean(o) }
  const out = o.metadata.resourceVersion
    ? await request('PUT', urlFor(s, ns, o.metadata.name), body)
    : await request('POST', urlFor(s, ns), body)
  forget(type)
  return tag(type, out)
}

// A JSON merge patch.
export async function patch(type: string, namespace: string | undefined, name: string, body: unknown): Promise<Obj> {
  const s = schemaOrThrow(type)
  const out = await request('PATCH', urlFor(s, namespace, name), body, { contentType: 'application/merge-patch+json' })
  forget(type)
  return tag(type, out)
}

export async function remove(type: string, o: Obj | { metadata: { name: string; namespace?: string } }): Promise<void> {
  const s = schemaOrThrow(type)
  await request('DELETE', urlFor(s, o.metadata.namespace, o.metadata.name))
  forget(type)
}

// The host's API for what needs hyve's own code (internal/hostapi).
export function host<T = any>(path: string, data?: unknown): Promise<T> {
  return request<T>('POST', `/v1/hyve/${path}`, data ?? {})
}
