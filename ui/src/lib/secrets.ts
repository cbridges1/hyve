// The Secrets a template or a cluster needs, and whether they exist: what
// its resources have Fleet copy (downstreamResources), and for a cluster
// the credentials its Cluster API provider reads. Read by the template
// page's and the cluster's Resources tab's Secrets sections. Also a
// Secret's reads and writes, on the host or (for the explorer) a child.
import { ApiError, request } from './api'
import { clusterBase, getObject } from './explorer'
import { kube, save, typeForKind, urlFor, type Obj, type Reader, type Schema } from './kube'
import { clusterItems, listItems, type Entry, type Item } from './resources'
import { LABELS, TYPES } from './types'

export type Need = {
  name: string
  namespace: string
  // What needs it: resource names, or "provider credentials".
  neededBy: string[]
  // null: the user can't read it, and nothing says whether it's missing.
  exists: boolean | null
  type: string
  keys: string[]
  // The operator object that owns it (InfisicalStaticSecret/x), whose
  // syncs would undo an edit.
  managedBy: string
}

// Names of the Secrets an object's downstreamResources copy.
function downstream(o: Obj | undefined): string[] {
  return ((o?.spec?.downstreamResources || []) as any[]).filter((d) => d?.kind === 'Secret' && d.name).map((d) => String(d.name))
}

// An entry's Secrets as written: its own, or its shared Resource's too.
function entrySecrets(e: Entry, ns: string, shared: Obj[]): string[] {
  const own = e.secrets || []
  if (!e.ref) return own
  const res = shared.find((x) => x.metadata.name === e.ref && x.metadata.namespace === (e.refNamespace || ns))
  return [...(res?.spec?.secrets || []), ...own]
}

// An item's Secrets as delivered: on its Fleet object, and for a GitRepo
// on the bundles Fleet made from the repo's fleet.yaml files.
function itemSecrets(it: Item): string[] {
  return [...downstream(it.model), ...(it.bundles || []).flatMap(downstream)]
}

async function state(r: Reader, namespace: string, name: string): Promise<Pick<Need, 'exists' | 'type' | 'keys' | 'managedBy'>> {
  try {
    const s = await r.find(TYPES.SECRET, namespace, name)
    const owner = (s.metadata.ownerReferences || [])[0]
    return {
      exists: true,
      type: s.type || 'Opaque',
      keys: Object.keys(s.data || {}).sort(),
      managedBy: owner ? `${owner.kind}/${owner.name}` : '',
    }
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return { exists: false, type: '', keys: [], managedBy: '' }
    if (e instanceof ApiError && e.status === 403) return { exists: null, type: '', keys: [], managedBy: '' }
    throw e
  }
}

// Fills in each needed Secret's state. missing: what hyve-controller
// reported missing, for Secrets the user can't read.
async function resolve(r: Reader, namespace: string, wanted: Map<string, Set<string>>, missing: Set<string>): Promise<Need[]> {
  const out = await Promise.all(
    [...wanted].map(async ([name, by]) => {
      const st = await state(r, namespace, name)
      if (st.exists === null && missing.has(name)) st.exists = false
      return { name, namespace, neededBy: [...by].sort(), ...st }
    }),
  )
  return out.sort((a, b) => a.name.localeCompare(b.name))
}

function want(m: Map<string, Set<string>>, secret: string, by: string) {
  if (!m.has(secret)) m.set(secret, new Set())
  m.get(secret)!.add(by)
}

// A template's: what its resources copy, from the template namespace.
export async function templateSecrets(r: Reader, tpl: Obj): Promise<Need[]> {
  const ns = tpl.metadata.namespace!
  const [shared, items] = await Promise.all([
    r.findAll(TYPES.RESOURCE).catch(() => [] as Obj[]),
    listItems(r, [{ kind: 'ClusterTemplate', name: tpl.metadata.name, namespace: ns }]).catch(() => [] as Item[]),
  ])
  const wanted = new Map<string, Set<string>>()
  ;(tpl.spec?.resources || []).forEach((e: Entry) => entrySecrets(e, ns, shared).forEach((s) => want(wanted, s, e.name || e.ref || '')))
  items.forEach((it) => itemSecrets(it).forEach((s) => want(wanted, s, it.name)))
  return resolve(r, ns, wanted, new Set(tpl.status?.missingSecrets || []))
}

// The Secret a cluster's infrastructure provider reads its credentials
// from (spec.credentialsRef, as the Civo and Unraid providers have), in
// the cluster's namespace; '' for providers that take credentials another
// way (their own variables, an identity object).
async function providerSecret(r: Reader, cluster: Obj): Promise<string> {
  const ref = cluster.spec?.infrastructureRef
  if (!ref?.kind || !ref?.name) return ''
  // v1beta1 refs give an apiVersion, v1beta2 an apiGroup; only the group counts.
  const apiVersion = ref.apiVersion || (ref.apiGroup ? `${ref.apiGroup}/` : '')
  const type = typeForKind(apiVersion, ref.kind)
  if (!type) return ''
  const infra = await r.maybe(type, cluster.metadata.namespace, ref.name).catch(() => null)
  const cred = infra?.spec?.credentialsRef
  if (!cred?.name || (cred.kind && cred.kind !== 'Secret')) return ''
  return String(cred.name)
}

// A cluster's: what its extras copy, and its provider's credentials, from
// its environment. Its template's are on the template's page.
export async function clusterSecrets(r: Reader, cluster: Obj): Promise<Need[]> {
  const ns = cluster.metadata.namespace!
  const [shared, items, crs, provider] = await Promise.all([
    r.findAll(TYPES.RESOURCE).catch(() => [] as Obj[]),
    clusterItems(r, cluster).catch(() => [] as Item[]),
    r.findAll(TYPES.CLUSTER_RESOURCE).catch(() => [] as Obj[]),
    providerSecret(r, cluster),
  ])
  const mine = crs.filter((cr) => cr.metadata.namespace === ns && cr.spec?.clusterName === cluster.metadata.name)
  const wanted = new Map<string, Set<string>>()
  if (provider) want(wanted, provider, 'provider credentials')
  // A ClusterResource is named <cluster>-<resource>; its label has the resource's own name.
  mine.forEach((cr) => entrySecrets(cr.spec || {}, ns, shared).forEach((s) => want(wanted, s, cr.metadata.labels?.[LABELS.RESOURCE] || cr.metadata.name)))
  items.filter((it) => it.ownerKind === 'ClusterResource').forEach((it) => itemSecrets(it).forEach((s) => want(wanted, s, it.name)))
  const missing = new Set<string>(mine.flatMap((cr) => cr.status?.missingSecrets || []))
  return resolve(r, ns, wanted, missing)
}

// Reads and writes one cluster's Secrets: the host's through the shared
// store, a child's through the explorer's proxy.
export type SecretApi = {
  get: (namespace: string, name: string) => Promise<Obj>
  // Creates it, or replaces it when it has a resourceVersion.
  save: (o: Obj) => Promise<Obj>
}

export const hostSecrets: SecretApi = {
  get: (namespace, name) => kube.find(TYPES.SECRET, namespace, name),
  save: (o) => save(TYPES.SECRET, o),
}

export function clusterSecretApi(id: string, s: Schema): SecretApi {
  return {
    get: (namespace, name) => getObject(id, s, namespace, name),
    save: (o) => {
      const body = { apiVersion: 'v1', kind: 'Secret', ...JSON.parse(JSON.stringify(o)) }
      const ns = o.metadata.namespace
      return o.metadata.resourceVersion
        ? request('PUT', urlFor(s, ns, o.metadata.name, clusterBase(id)), body)
        : request('POST', urlFor(s, ns, undefined, clusterBase(id)), body)
    },
  }
}

export function toBase64(bytes: Uint8Array) {
  let bin = ''
  bytes.forEach((b) => {
    bin += String.fromCharCode(b)
  })
  return btoa(bin)
}

export function fromBase64(b64: string) {
  const bin = atob(b64 || '')
  return Uint8Array.from(bin, (c) => c.charCodeAt(0))
}

// Text when the bytes are UTF-8, else null (a binary value).
export function asText(bytes: Uint8Array) {
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  } catch {
    return null
  }
}
