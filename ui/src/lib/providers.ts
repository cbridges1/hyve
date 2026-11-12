// The installed Cluster API providers, however they were installed (the
// CAPI Operator, or local mode's lean install): every provider's
// controller is a Deployment labelled cluster.x-k8s.io/provider with its
// full name (infrastructure-civo). Read by the Providers page and each
// provider's own page.
import { idOf, schemaFor, type Obj, type Reader } from './kube'
import { TYPES } from './types'

const DEPLOYMENT = 'apps.deployment'
const CRD = 'apiextensions.k8s.io.customresourcedefinition'
export const PROVIDER_LABEL = 'cluster.x-k8s.io/provider'
// The CAPI Operator's provider kinds.
export const OPERATOR_TYPES = ['core', 'bootstrap', 'controlplane', 'infrastructure', 'addon', 'ipam', 'runtimeextension'].map(
  (k) => `operator.cluster.x-k8s.io.${k}provider`,
)
const KINDS = ['infrastructure', 'bootstrap', 'control-plane', 'addon', 'ipam', 'runtime-extension']

export type Provider = {
  d: Obj
  id: string
  // The label's value, infrastructure-civo, and its parts.
  full: string
  name: string
  type: string
  ns: string
  version: string
  image: string
  healthy: boolean
  state: string
}

export function toProvider(d: Obj): Provider {
  const full = d.metadata.labels![PROVIDER_LABEL]
  let type = full === 'fleet' ? 'addon' : 'core'
  let name = full
  KINDS.forEach((t) => {
    if (full.startsWith(`${t}-`)) {
      type = t
      name = full.slice(t.length + 1)
    }
  })
  const image: string = d.spec?.template?.spec?.containers?.[0]?.image || ''
  const i = image.lastIndexOf(':')
  const want = d.spec?.replicas ?? 1
  const ready = d.status?.availableReplicas || 0
  return {
    d,
    id: idOf(d),
    full,
    name,
    type,
    ns: d.metadata.namespace || '',
    version: i > image.lastIndexOf('/') ? image.slice(i + 1) : '',
    image,
    healthy: ready >= want && want > 0,
    state: `${ready}/${want}`,
  }
}

export async function loadProviders(r: Reader): Promise<Provider[]> {
  const deployments = await r.findAll(DEPLOYMENT, { force: true }).catch(() => [] as Obj[])
  return deployments
    .filter((d) => d.metadata.labels?.[PROVIDER_LABEL])
    .map(toProvider)
    .sort((a, b) => `${a.type}${a.name}`.localeCompare(`${b.type}${b.name}`))
}

// The CAPI Operator's provider objects: what removing a provider deletes.
export async function providerObjects(r: Reader): Promise<Obj[]> {
  const out: Obj[] = []
  for (const type of OPERATOR_TYPES) {
    if (schemaFor(type)) out.push(...(await r.findAll(type, { force: true }).catch(() => [] as Obj[])))
  }
  return out
}

// The CAPI Operator object a provider was installed from, if it was.
export function operatorObject(objects: Obj[], p: Provider): Obj | undefined {
  return (
    objects.find((o) => o.metadata.namespace === p.ns && o.metadata.name === p.name) ||
    objects.find((o) => o.metadata.namespace === p.ns && (o.spec?.name || o.metadata.name) === p.name)
  )
}

// The ClusterClasses an infrastructure provider serves: those whose
// infrastructure template is one of its kinds. clusterctl labels a
// provider's CRDs with its name; without access to CRDs, a kind named
// after the provider (CivoClusterTemplate) counts.
export async function classesOf(r: Reader, p: Provider): Promise<Obj[]> {
  const classes = await r.findAll(TYPES.CLUSTER_CLASS).catch(() => [] as Obj[])
  const crds = schemaFor(CRD) ? await r.findAll(CRD).catch(() => [] as Obj[]) : []
  const kinds = new Set(
    crds.filter((c) => c.metadata.labels?.[PROVIDER_LABEL] === p.full).map((c) => `${c.spec?.group}/${c.spec?.names?.kind}`),
  )
  return classes.filter((c) => {
    const ref = c.spec?.infrastructure?.templateRef || c.spec?.infrastructure?.ref
    if (!ref?.kind) return false
    const group = ref.apiGroup || String(ref.apiVersion || '').split('/')[0]
    if (kinds.size) return kinds.has(`${group}/${ref.kind}`)
    return String(ref.kind).toLowerCase().startsWith(p.name.toLowerCase())
  })
}
