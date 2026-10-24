// Helpers the cluster pages share: providers, expiry extension.
import { save, type Obj } from './kube'
import { ANNOTATIONS, TYPES } from './types'

// Which Cluster API provider a template's clusters run on, from its
// ClusterClass's infrastructure template kind: CivoClusterTemplate is
// "civo", UnraidClusterTemplate "unraid", DockerClusterTemplate "docker".
export function providerOfClass(cls: Obj | undefined | null): string {
  const ref = cls?.spec?.infrastructure?.templateRef || cls?.spec?.infrastructure?.ref || {}
  const kind: string = ref.kind || ''
  return kind.replace(/ClusterTemplate$/, '').toLowerCase()
}

export function classOf(template: Obj | undefined | null, classes: Obj[]): Obj | undefined {
  const ref = template?.spec?.clusterClassRef || {}
  const ns = ref.namespace || template?.metadata?.namespace
  return classes.find((c) => c.metadata.namespace === ns && c.metadata.name === ref.name)
}

export const EXTEND_HOURS = 8

export function isoSeconds(d: Date): string {
  return d.toISOString().replace(/\.\d{3}Z$/, 'Z')
}

// Sets a cluster's hyve.io/expires-at.
export function setExpiry(c: Obj, value: string): Promise<Obj> {
  const o: Obj = JSON.parse(JSON.stringify(c))
  o.metadata.annotations = { ...(o.metadata.annotations || {}), [ANNOTATIONS.EXPIRES_AT]: value }
  return save(TYPES.CLUSTER, o)
}

// Pushes a cluster's expiry EXTEND_HOURS past now or its current expiry,
// whichever is later.
export function extendExpiry(c: Obj): Promise<Obj> {
  const cur = Date.parse(c.metadata.annotations?.[ANNOTATIONS.EXPIRES_AT] || '')
  const base = Math.max(Date.now(), Number.isNaN(cur) ? 0 : cur)
  return setExpiry(c, isoSeconds(new Date(base + EXTEND_HOURS * 3600 * 1000)))
}
