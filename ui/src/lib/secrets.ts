// The Secrets a Deployment or a cluster needs, and whether they exist:
// what Deployments have Fleet copy (downstreamResources), and for a cluster
// the credentials its Cluster API provider reads. Read by a Deployment's
// page's and a cluster's Deployments tab's Secrets sections. Also a
// Secret's reads and writes, on the host or (for the explorer) a child.
// A provider's page shows what its ClusterClasses' clusters read their
// credentials from, in every environment.
import { ApiError, request } from './api'
import { clusterBase, getObject } from './explorer'
import { kube, save, typeForKind, urlFor, type Obj, type Reader, type Schema } from './kube'
import { secretsOf, type Deployment } from './delivery'
import { TYPES } from './types'

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

// A Deployment's: what it copies (downstreamResources), from its own
// namespace, where Fleet reads them.
export async function deploymentSecrets(r: Reader, d: Deployment): Promise<Need[]> {
  const wanted = new Map<string, Set<string>>()
  secretsOf(d).forEach((s) => want(wanted, s, d.name))
  return resolve(r, d.namespace, wanted, new Set())
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

// A cluster's: its provider's credentials, and what the Deployments living
// in its environment copy, from the environment.
export async function clusterSecrets(r: Reader, cluster: Obj, deployments: Deployment[] = []): Promise<Need[]> {
  const ns = cluster.metadata.namespace!
  const provider = await providerSecret(r, cluster)
  const wanted = new Map<string, Set<string>>()
  if (provider) want(wanted, provider, 'provider credentials')
  deployments.filter((d) => d.namespace === ns).forEach((d) => secretsOf(d).forEach((s) => want(wanted, s, d.name)))
  return resolve(r, ns, wanted, new Set())
}

// A credentialsRef.name a ClusterClass's patches set from a variable.
const CREDENTIALS_PATH = /\/credentialsRef\/name$/

// What ClusterClasses' clusters read their provider's credentials from: the
// Secret the infrastructure template's credentialsRef names, set by a
// patch from a variable (Civo's credentialsSecret) or fixed in the
// template. Each template on the class gives the variable its value, or the
// class's default does. Providers read it in the cluster's own namespace,
// so it's wanted in every environment, before any cluster is made there.
// classes: these ClusterClasses' (one provider's), else every one's.
export async function providerCredentials(r: Reader, environments: string[], only?: Obj[]): Promise<Need[]> {
  const [classes, templates] = await Promise.all([
    only ? Promise.resolve(only) : r.findAll(TYPES.CLUSTER_CLASS).catch(() => [] as Obj[]),
    r.findAll(TYPES.TEMPLATE).catch(() => [] as Obj[]),
  ])
  const wanted = new Map<string, Set<string>>()
  for (const c of classes) {
    const cname = c.metadata.name
    const vars = new Set<string>()
    for (const p of c.spec?.patches || []) {
      for (const d of p.definitions || []) {
        for (const j of d.jsonPatches || []) {
          if (CREDENTIALS_PATH.test(j.path || '') && j.valueFrom?.variable) vars.add(String(j.valueFrom.variable))
        }
      }
    }
    const names = new Set<string>()
    if (vars.size) {
      const users = templates.filter((t) => t.spec?.clusterClassRef?.name === cname && (t.metadata.namespace === c.metadata.namespace))
      for (const v of vars) {
        const def = (c.spec?.variables || []).find((x: any) => x.name === v)?.schema?.openAPIV3Schema?.default
        if (def) names.add(String(def))
        users.forEach((t) => t.spec?.variables?.[v] && names.add(String(t.spec.variables[v])))
      }
    } else {
      // Fixed in the infrastructure cluster template.
      const ref = c.spec?.infrastructure?.templateRef || c.spec?.infrastructure?.ref
      const type = ref?.kind && typeForKind(ref.apiVersion || '', ref.kind)
      const infra = type ? await r.maybe(type, c.metadata.namespace, ref.name).catch(() => null) : null
      const cred = infra?.spec?.template?.spec?.credentialsRef
      if (cred?.name && (!cred.kind || cred.kind === 'Secret')) names.add(String(cred.name))
    }
    names.forEach((n) => want(wanted, n, `${cname} clusters`))
  }
  const per = await Promise.all(environments.map((env) => resolve(r, env, wanted, new Set())))
  return per.flat()
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
