// A cluster's resources, as Rancher's cluster dashboard shows them: CPU,
// memory and pods, each as what's used (from metrics-server, when the
// cluster has it), what pods have requested, and what the nodes can give
// (allocatable); each node's health; recent warning events. Read through
// the host's /k8s/clusters/<id> proxy as the signed-in user, so it's what
// they may see.
import { ApiError, request } from './api'
import { clusterBase } from './explorer'

// A Kubernetes quantity ("250m", "1.5", "512Mi", "123456n", "1e3") as a
// number: cores for CPU, bytes for memory.
export function quantity(q: string | number | undefined): number {
  if (q === undefined || q === null || q === '') return 0
  if (typeof q === 'number') return q
  const m = /^([+-]?[0-9.]+(?:[eE][+-]?[0-9]+)?)([a-zA-Z]*)$/.exec(q.trim())
  if (!m) return 0
  const n = parseFloat(m[1])
  const unit: Record<string, number> = {
    n: 1e-9, u: 1e-6, m: 1e-3, '': 1, k: 1e3, M: 1e6, G: 1e9, T: 1e12, P: 1e15, E: 1e18,
    Ki: 2 ** 10, Mi: 2 ** 20, Gi: 2 ** 30, Ti: 2 ** 40, Pi: 2 ** 50, Ei: 2 ** 60,
  }
  return n * (unit[m[2]] ?? 1)
}

export type Usage = {
  // What the nodes can give pods (allocatable), and the whole machine.
  allocatable: number
  capacity: number
  requested: number
  limits: number
  // From metrics-server; null without it.
  used: number | null
}

export type NodeHealth = {
  name: string
  roles: string[]
  ready: boolean
  // Pressure conditions and cordoning, in words.
  problems: string[]
  version: string
  cpu: Usage
  memory: Usage
  pods: { allocatable: number; running: number }
}

export type ClusterEvent = { reason: string; message: string; object: string; namespace: string; count: number; last: string }

export type ClusterResources = {
  cpu: Usage
  memory: Usage
  pods: { allocatable: number; running: number }
  nodes: NodeHealth[]
  // Whether metrics-server answers; without it "used" is unknown.
  metrics: boolean
  events: ClusterEvent[] | null
}

const zero = (): Usage => ({ allocatable: 0, capacity: 0, requested: 0, limits: 0, used: null })

// A pod's requests (or limits) for one resource: the larger of its
// containers' sum and any one init container's, as the scheduler counts.
function podResource(pod: any, kind: 'requests' | 'limits', res: 'cpu' | 'memory'): number {
  const sum = (pod.spec?.containers || []).reduce((t: number, c: any) => t + quantity(c.resources?.[kind]?.[res]), 0)
  const init = Math.max(0, ...(pod.spec?.initContainers || []).map((c: any) => quantity(c.resources?.[kind]?.[res])))
  return Math.max(sum, init) + quantity(pod.spec?.overhead?.[res])
}

const PRESSURES: Record<string, string> = {
  MemoryPressure: 'memory pressure',
  DiskPressure: 'disk pressure',
  PIDPressure: 'PID pressure',
  NetworkUnavailable: 'network unavailable',
}

async function maybe<T>(p: Promise<T>): Promise<T | null> {
  try {
    return await p
  } catch (e) {
    if (e instanceof ApiError && [403, 404, 503].includes(e.status)) return null
    throw e
  }
}

// loadResources reads a cluster's nodes, the pods that hold resources
// (not finished ones), node metrics and warning events. With
// events=false, it skips them (the overview's summary).
export async function loadResources(clusterId: string, opts: { events?: boolean } = {}): Promise<ClusterResources> {
  const base = clusterBase(clusterId)
  const [nodes, pods, metrics, events] = await Promise.all([
    request<any>('GET', `${base}/api/v1/nodes`),
    request<any>('GET', `${base}/api/v1/pods?fieldSelector=${encodeURIComponent('status.phase!=Succeeded,status.phase!=Failed')}`),
    maybe(request<any>('GET', `${base}/apis/metrics.k8s.io/v1beta1/nodes`)),
    opts.events === false ? null : maybe(request<any>('GET', `${base}/api/v1/events?fieldSelector=type%3DWarning&limit=200`)),
  ])

  const used = new Map<string, { cpu: number; memory: number }>()
  for (const m of metrics?.items || []) used.set(m.metadata.name, { cpu: quantity(m.usage?.cpu), memory: quantity(m.usage?.memory) })

  const byNode = new Map<string, any[]>()
  for (const p of pods.items || []) {
    const n = p.spec?.nodeName
    if (!n) continue
    if (!byNode.has(n)) byNode.set(n, [])
    byNode.get(n)!.push(p)
  }

  const total: ClusterResources = { cpu: zero(), memory: zero(), pods: { allocatable: 0, running: 0 }, nodes: [], metrics: !!metrics, events: null }
  if (metrics) total.cpu.used = total.memory.used = 0

  for (const n of nodes.items || []) {
    const name = n.metadata.name
    const onNode = byNode.get(name) || []
    const u = used.get(name)
    const usage = (res: 'cpu' | 'memory'): Usage => ({
      allocatable: quantity(n.status?.allocatable?.[res]),
      capacity: quantity(n.status?.capacity?.[res]),
      requested: onNode.reduce((t, p) => t + podResource(p, 'requests', res), 0),
      limits: onNode.reduce((t, p) => t + podResource(p, 'limits', res), 0),
      used: u ? u[res] : null,
    })
    const conds: any[] = n.status?.conditions || []
    const problems = conds.filter((c) => PRESSURES[c.type] && c.status === 'True').map((c) => PRESSURES[c.type])
    if (n.spec?.unschedulable) problems.push('cordoned')
    const node: NodeHealth = {
      name,
      roles: Object.keys(n.metadata.labels || {})
        .filter((k) => k.startsWith('node-role.kubernetes.io/'))
        .map((k) => k.slice('node-role.kubernetes.io/'.length)),
      ready: conds.some((c) => c.type === 'Ready' && c.status === 'True'),
      problems,
      version: n.status?.nodeInfo?.kubeletVersion || '',
      cpu: usage('cpu'),
      memory: usage('memory'),
      pods: { allocatable: quantity(n.status?.allocatable?.pods), running: onNode.length },
    }
    total.nodes.push(node)
    for (const res of ['cpu', 'memory'] as const) {
      const t = total[res]
      const x = node[res]
      t.allocatable += x.allocatable
      t.capacity += x.capacity
      t.requested += x.requested
      t.limits += x.limits
      if (t.used !== null) t.used += x.used ?? 0
    }
    total.pods.allocatable += node.pods.allocatable
    total.pods.running += node.pods.running
  }
  total.nodes.sort((a, b) => a.name.localeCompare(b.name))

  if (events) {
    total.events = (events.items || [])
      .map((e: any) => ({
        reason: e.reason || '',
        message: e.message || '',
        object: `${(e.involvedObject?.kind || '').toLowerCase()}/${e.involvedObject?.name || ''}`,
        namespace: e.involvedObject?.namespace || e.metadata?.namespace || '',
        count: e.count || e.series?.count || 1,
        last: e.lastTimestamp || e.eventTime || e.series?.lastObservedTime || e.metadata?.creationTimestamp || '',
      }))
      .sort((a: ClusterEvent, b: ClusterEvent) => (b.last || '').localeCompare(a.last || ''))
      .slice(0, 20)
  }
  return total
}

// Formatting.
export const cores = (n: number) => (n >= 10 ? n.toFixed(0) : n >= 1 ? n.toFixed(1) : `${Math.round(n * 1000)}m`)

export function bytes(n: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n >= 10 || i === 0 ? n.toFixed(0) : n.toFixed(1)} ${units[i]}`
}

export const percent = (part: number, whole: number) => (whole > 0 ? Math.round((part / whole) * 100) : 0)
