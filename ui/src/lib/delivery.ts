// Deployments, as the CLI's internal/delivery has them: a Deployment is one
// Fleet GitRepo, HelmOp or Bundle, and its targets name ClusterGroups.
// hyve keeps a group for each template (template-NAME) and environment
// (env-NAME) in the template namespace; CAAPF keeps one for each
// ClusterClass. Nothing here is hyve's own kind: a Deployment written by
// hand, in git or with kubectl, is read the same way.
import type { Tone } from './format'
import { type Obj, type Reader } from './kube'
import { LOCAL_FLEET_CLUSTER } from './repos'
import { FLEET, type Entry } from './resources'
import { DEFAULT_TEMPLATE_NAMESPACE, LABELS, TYPES } from './types'

export const CLUSTER_GROUP = 'fleet.cattle.io.clustergroup'
export const HOST_WORKSPACE = 'fleet-local'
export const HOST_CLUSTER = 'local'
export const CANARY_LABEL = 'hyve.io/canary'
const GROUP_KIND_LABEL = 'hyve.io/group-kind'
const CLASS_LABEL = 'clusterclass-name.fleet.addons.cluster.x-k8s.io'

export type TargetKind = 'template' | 'class' | 'env' | 'cluster' | 'group' | 'all' | 'host' | 'custom'

export type Target = { kind: TargetKind; name?: string; env?: string; raw?: any }

export const TARGET_KINDS: { kind: TargetKind; label: string }[] = [
  { kind: 'template', label: 'Template' },
  { kind: 'class', label: 'Cluster class' },
  { kind: 'env', label: 'Environment' },
  { kind: 'cluster', label: 'Cluster' },
  { kind: 'group', label: 'Group' },
  { kind: 'all', label: 'All clusters' },
  { kind: 'host', label: 'Host cluster' },
]

export function targetKey(t: Target): string {
  switch (t.kind) {
    case 'all':
    case 'host':
      return t.kind
    case 'cluster':
      return `cluster/${t.env}/${t.name}`
    case 'custom':
      return `custom/${JSON.stringify(t.raw)}`
  }
  return `${t.kind}/${t.name}`
}

// In words, for chips and lists.
export function targetLabel(t: Target): string {
  switch (t.kind) {
    case 'template':
      return `Template ${t.name}`
    case 'class':
      return `Every ${t.name} cluster`
    case 'env':
      return `Environment ${t.name}`
    case 'cluster':
      return `Cluster ${t.env}/${t.name}`
    case 'group':
      return `Group ${t.name}`
    case 'all':
      return 'All clusters'
    case 'host':
      return 'Host cluster'
  }
  return 'Custom target'
}

export function templateGroup(name: string) {
  return `template-${name}`
}
export function envGroup(name: string) {
  return `env-${name}`
}

// Where a new Deployment lives: fleet-local for the host, an environment
// when that environment's clusters are all it targets, else the template
// namespace.
export function home(ts: Target[], workspace = DEFAULT_TEMPLATE_NAMESPACE): string {
  if (!ts.length) throw new Error('Pick at least one target.')
  const hosts = ts.filter((t) => t.kind === 'host').length
  if (hosts === ts.length) return HOST_WORKSPACE
  if (hosts) throw new Error("The host cluster can't be a target together with other clusters: make a separate Deployment for it.")
  const envs = new Set(ts.map((t) => (t.kind === 'cluster' ? t.env : t.kind === 'env' ? t.name : '\0')))
  const [only] = [...envs]
  return envs.size === 1 && only && only !== '\0' ? only : workspace
}

// Fleet's targets for a Deployment living in namespace ns.
export function fleetTargets(ns: string, ts: Target[], workspace = DEFAULT_TEMPLATE_NAMESPACE): any[] {
  return ts.map((t) => {
    if (t.kind === 'custom') return t.raw
    if (ns === HOST_WORKSPACE) {
      if (t.kind !== 'host') throw new Error(`A Deployment in ${HOST_WORKSPACE} only reaches the host cluster.`)
      return { clusterName: HOST_CLUSTER }
    }
    if (ns !== workspace) {
      if (t.kind === 'cluster' && t.env === ns) return { clusterName: t.name }
      if (t.kind === 'env' && t.name === ns) return { clusterSelector: {} }
      throw new Error(`This Deployment lives in environment ${ns}, so it can only target that environment's clusters (not ${targetLabel(t)}): make a new one for that.`)
    }
    switch (t.kind) {
      case 'template':
        return { clusterGroup: templateGroup(t.name!) }
      case 'class':
      case 'group':
        return { clusterGroup: t.name }
      case 'env':
        return { clusterGroup: envGroup(t.name!) }
      case 'cluster':
        return { clusterSelector: { matchLabels: { [LABELS.ENVIRONMENT]: t.env, [LABELS.CLUSTER]: t.name } } }
      case 'all':
        return { clusterSelector: { matchLabels: { [LABELS.MANAGED_BY]: 'hyve' } } }
    }
    throw new Error(`The host cluster is reached from ${HOST_WORKSPACE}: make a separate Deployment for it.`)
  })
}

export function isClassGroup(g: Obj): boolean {
  return !!g.spec?.selector?.matchLabels?.[CLASS_LABEL]
}

// A Fleet object's targets, read back. classGroups: the names of CAAPF's
// class groups in its namespace.
export function decodeTargets(o: Obj, classGroups: Set<string>, workspace = DEFAULT_TEMPLATE_NAMESPACE): Target[] {
  const ns = o.metadata.namespace || ''
  const raw: any[] = o.spec?.targets || []
  if (!raw.length && ns === HOST_WORKSPACE) return [{ kind: 'host' }]
  return raw.map((m) => {
    const custom: Target = { kind: 'custom', raw: m }
    const keys = Object.keys(m || {}).filter((k) => k !== 'name')
    if (keys.length !== 1) return custom
    if (typeof m.clusterName === 'string') {
      if (ns === HOST_WORKSPACE && m.clusterName === HOST_CLUSTER) return { kind: 'host' }
      if (ns !== workspace && ns !== HOST_WORKSPACE) return { kind: 'cluster', env: ns, name: m.clusterName }
      return custom
    }
    if (typeof m.clusterGroup === 'string' && ns === workspace) {
      const g: string = m.clusterGroup
      if (g.startsWith('template-')) return { kind: 'template', name: g.slice(9) }
      if (g.startsWith('env-')) return { kind: 'env', name: g.slice(4) }
      return { kind: classGroups.has(g) ? 'class' : 'group', name: g }
    }
    const s = m.clusterSelector
    if (s && typeof s === 'object') {
      if (!Object.keys(s).length && ns !== workspace && ns !== HOST_WORKSPACE) return { kind: 'env', name: ns }
      const ml = s.matchLabels
      if (Object.keys(s).length !== 1 || !ml || ns !== workspace) return custom
      const mk = Object.keys(ml)
      if (mk.length === 1 && ml[LABELS.MANAGED_BY] === 'hyve') return { kind: 'all' }
      if (mk.length === 2 && ml[LABELS.ENVIRONMENT] && ml[LABELS.CLUSTER]) return { kind: 'cluster', env: ml[LABELS.ENVIRONMENT], name: ml[LABELS.CLUSTER] }
    }
    return custom
  })
}

export type Rollout = 'all' | 'one-at-a-time' | 'canary' | 'custom'

export const ROLLOUTS: { value: Rollout; label: string }[] = [
  { value: 'all', label: 'All at once' },
  { value: 'one-at-a-time', label: 'One cluster at a time' },
  { value: 'canary', label: 'Canary clusters first' },
]

export function rolloutStrategy(r: Rollout): any | undefined {
  switch (r) {
    // As the CLI's: Fleet updates a cluster only while fewer than
    // maxUnavailable are unavailable; partitions go in order.
    case 'one-at-a-time':
      return { maxUnavailable: 1 }
    case 'canary':
      return {
        maxUnavailable: 1,
        partitions: [
          { name: 'canary', clusterSelector: { matchLabels: { [CANARY_LABEL]: 'true' } } },
          { name: 'rest', clusterSelector: { matchExpressions: [{ key: CANARY_LABEL, operator: 'DoesNotExist' }] } },
        ],
      }
  }
  return undefined
}

function same(a: unknown, b: unknown): boolean {
  return JSON.stringify(sortKeys(a)) === JSON.stringify(sortKeys(b))
}
function sortKeys(v: any): any {
  if (Array.isArray(v)) return v.map(sortKeys)
  if (v && typeof v === 'object') return Object.fromEntries(Object.keys(v).sort().map((k) => [k, sortKeys(v[k])]))
  return v
}

export function rolloutOf(o: Obj): Rollout {
  const rs = o.spec?.rolloutStrategy
  if (!rs || !Object.keys(rs).length) return 'all'
  for (const r of ['one-at-a-time', 'canary'] as Rollout[]) if (same(rs, rolloutStrategy(r))) return r
  return 'custom'
}

export function kindType(kind: string): string {
  return kind === 'GitRepo' ? FLEET.GIT_REPO : kind === 'HelmOp' ? FLEET.HELM_OP : FLEET.BUNDLE
}

export type SourceKind = 'git' | 'helm' | 'manifests'

export function sourceKind(o: Obj): SourceKind {
  if (o.kind === 'GitRepo') return 'git'
  if (o.kind === 'HelmOp' || o.spec?.helm?.chart) return 'helm'
  return 'manifests'
}

export function describeSource(o: Obj): string {
  const s = o.spec || {}
  switch (o.kind) {
    case 'GitRepo':
      return `${s.repo || ''}${s.revision ? `@${s.revision}` : s.branch ? `@${s.branch}` : ''}${s.paths?.length ? ` ${s.paths.join(', ')}` : ''}`
    case 'HelmOp': {
      const h = s.helm || {}
      return `${h.chart ? `${h.repo ? `${h.repo} ` : ''}${h.chart}` : h.repo || ''}${h.version ? `@${h.version}` : ''}`
    }
  }
  if (s.helm?.chart) return `${s.helm.chart}${s.helm.version ? `@${s.helm.version}` : ''}`
  return `${(s.resources || []).length} manifests`
}

// A Fleet object's spec as a resource form's entry, and back.
export function entryOf(o: Obj): Entry {
  const spec = JSON.parse(JSON.stringify(o.spec || {}))
  for (const k of ['targets', 'rolloutStrategy', 'paused']) delete spec[k]
  const e: Entry = { name: o.metadata.name }
  if (o.kind === 'GitRepo') e.gitRepo = spec
  else if (o.kind === 'HelmOp') e.helmOp = spec
  else e.bundle = spec
  return e
}

// The Fleet object for a form's entry, targets and rollout; existing, when
// editing, keeps whatever the form doesn't model.
export function deploymentObject(f: { name: string; entry: Entry; targets: Target[]; rollout: Rollout; paused: boolean; namespace?: string }, existing?: Obj): Obj {
  const e = f.entry
  const kind = e.helmOp ? 'HelmOp' : e.gitRepo ? 'GitRepo' : 'Bundle'
  if (existing && existing.kind !== kind) throw new Error(`${f.name} is a ${existing.kind}; its source can't change kind. Make a new Deployment instead.`)
  const ns = existing?.metadata.namespace || f.namespace || home(f.targets)
  const spec: any = JSON.parse(JSON.stringify(e.helmOp || e.gitRepo || e.bundle || {}))
  if (kind === 'HelmOp' && !spec.helm?.releaseName) spec.helm = { ...(spec.helm || {}), releaseName: f.name }
  if (e.namespace) {
    const key = kind === 'GitRepo' ? 'targetNamespace' : 'defaultNamespace'
    if (!spec[key]) spec[key] = e.namespace
  }
  if (e.secrets?.length) {
    const ds: any[] = spec.downstreamResources || []
    for (const s of e.secrets) if (!ds.some((d) => d.kind === 'Secret' && d.name === s)) ds.push({ kind: 'Secret', name: s })
    spec.downstreamResources = ds
  }
  spec.targets = fleetTargets(ns, f.targets)
  if (kind === 'GitRepo') {
    if (f.rollout !== 'all' && f.rollout !== 'custom') throw new Error("A git source's rollout is set in its fleet.yaml (rolloutStrategy).")
  } else if (f.rollout === 'custom') {
    if (existing?.spec?.rolloutStrategy) spec.rolloutStrategy = existing.spec.rolloutStrategy
  } else {
    const rs = rolloutStrategy(f.rollout)
    if (rs) spec.rolloutStrategy = rs
  }
  if (f.paused) spec.paused = true
  if (existing) {
    const modelled = new Set([
      'helm', 'resources', 'repo', 'branch', 'revision', 'paths', 'targets', 'rolloutStrategy', 'paused',
      'defaultNamespace', 'targetNamespace', 'downstreamResources', 'correctDrift',
    ])
    for (const [k, v] of Object.entries(existing.spec || {})) if (!(k in spec) && !modelled.has(k)) spec[k] = v
    return { ...existing, spec }
  }
  return { apiVersion: 'fleet.cattle.io/v1alpha1', kind, metadata: { name: f.name, namespace: ns }, spec }
}

// BundleDeployment state and message.
export function bdState(bd: Obj): string {
  return bd.status?.display?.state || (bd.status?.ready ? 'Ready' : 'Pending')
}
export function bdMessage(bd: Obj): string {
  if (bd.status?.display?.message) return bd.status.display.message
  const c = (bd.status?.conditions || []).find((x: any) => x.status === 'False' && x.message)
  return c?.message || ''
}

// The Fleet cluster ("namespace/name") a BundleDeployment is for.
export function bdCluster(bd: Obj): string {
  const l = bd.metadata.labels || {}
  return `${l['fleet.cattle.io/cluster-namespace'] || ''}/${l['fleet.cattle.io/cluster'] || ''}`
}

// Whether a BundleDeployment belongs to a Deployment.
export function bdOf(bd: Obj, d: Obj): boolean {
  const l = bd.metadata.labels || {}
  if (l['fleet.cattle.io/bundle-namespace'] !== d.metadata.namespace) return false
  return d.kind === 'GitRepo' ? l['fleet.cattle.io/repo-name'] === d.metadata.name : l['fleet.cattle.io/bundle-name'] === d.metadata.name
}

export type ClusterState = { cluster: string; state: string; message: string }

export function worst(states: ClusterState[]): ClusterState[] {
  const by = new Map<string, ClusterState>()
  for (const s of states) {
    const cur = by.get(s.cluster)
    if (!cur || (cur.state === 'Ready' && s.state !== 'Ready')) by.set(s.cluster, s)
  }
  return [...by.values()].sort((a, b) => a.cluster.localeCompare(b.cluster))
}

export type Deployment = {
  model: Obj
  name: string
  namespace: string
  kind: string
  type: string
  source: string
  sourceKind: SourceKind
  targets: Target[]
  rollout: Rollout
  paused: boolean
  // Fleet's bundles behind it: a GitRepo's, a HelmOp's, or the Bundle itself.
  bundles: Obj[]
  // Its state on each cluster it reached, from its BundleDeployments.
  states: ClusterState[]
  bds: Obj[]
}

// Bundles Fleet made for a GitRepo or HelmOp (or anything else owns), not
// Deployments of their own.
export function generated(b: Obj): boolean {
  const l = b.metadata.labels || {}
  return !!(l['fleet.cattle.io/repo-name'] || l['fleet.cattle.io/fleet-helm-name'] || (b.metadata.ownerReferences || []).length)
}

export type Delivery = {
  deployments: Deployment[]
  groups: Obj[]
  clusters: Obj[]
  templates: Obj[]
  environments: string[]
  classes: string[]
  // The Fleet clusters' BundleDeployments could be read (an admin); else
  // states come from the Fleet objects' summaries only.
  detailed: boolean
}

// Everything the Fleet pages show, read as the signed-in user.
export async function loadDelivery(r: Reader, workspace = DEFAULT_TEMPLATE_NAMESPACE): Promise<Delivery> {
  const none = () => [] as Obj[]
  const [helmOps, gitRepos, bundles, groups, clusters, templates, namespaces, bdsRaw] = await Promise.all([
    r.findAll(FLEET.HELM_OP).catch(none),
    r.findAll(FLEET.GIT_REPO).catch(none),
    r.findAll(FLEET.BUNDLE).catch(none),
    r.findAll(CLUSTER_GROUP).catch(none),
    r.findAll(TYPES.CLUSTER).catch(none),
    r.findAll(TYPES.TEMPLATE).catch(none),
    r.findAll(TYPES.NAMESPACE).catch(none),
    r.findAll(FLEET.BUNDLE_DEPLOYMENT).catch(() => null),
  ])
  const bds = bdsRaw || []
  const environments = namespaces
    .filter((n) => n.metadata.labels?.[LABELS.ENVIRONMENT] !== undefined)
    .map((n) => n.metadata.name)
    .sort()
  const homes = new Set([workspace, HOST_WORKSPACE, ...environments])
  const classGroups = new Set(groups.filter((g) => g.metadata.namespace === workspace && isClassGroup(g)).map((g) => g.metadata.name))
  const classes = [...new Set(groups.filter(isClassGroup).map((g) => g.metadata.name))].sort()
  const objs = [...gitRepos, ...helmOps, ...bundles.filter((b) => !generated(b))].filter((o) => homes.has(o.metadata.namespace || ''))
  const deployments = objs.map((o): Deployment => {
    const mine = o.kind === 'Bundle' ? [o] : bundles.filter((b) => b.metadata.namespace === o.metadata.namespace && b.metadata.labels?.[o.kind === 'GitRepo' ? 'fleet.cattle.io/repo-name' : 'fleet.cattle.io/fleet-helm-name'] === o.metadata.name)
    const myBds = bds.filter((bd) => bdOf(bd, o))
    return {
      model: o,
      name: o.metadata.name,
      namespace: o.metadata.namespace || '',
      kind: o.kind || '',
      type: kindType(o.kind || ''),
      source: describeSource(o),
      sourceKind: sourceKind(o),
      targets: decodeTargets(o, classGroups, workspace),
      rollout: rolloutOf(o),
      paused: !!o.spec?.paused,
      bundles: mine,
      bds: myBds,
      states: worst(myBds.map((bd) => ({ cluster: bdCluster(bd), state: bdState(bd), message: bdMessage(bd) }))),
    }
  })
  deployments.sort((a, b) => `${a.namespace}/${a.name}`.localeCompare(`${b.namespace}/${b.name}`))
  return { deployments, groups, clusters, templates, environments, classes, detailed: bdsRaw !== null }
}

// "ready/total" clusters and Fleet's overall state, from the object's own
// status (which everyone who can read it can see).
export function readiness(d: Deployment): { ready: string; state: string; message: string } {
  const disp = d.model.status?.display || {}
  let ready: string = disp.readyBundleDeployments || disp.readyClusters || ''
  const sum = d.model.status?.summary
  if (!ready && sum) ready = `${sum.ready || 0}/${sum.desiredReady || 0}`
  if (d.states.length) ready = `${d.states.filter((s) => s.state === 'Ready').length}/${d.states.length}`
  const bad = d.states.find((s) => s.state !== 'Ready')
  const [have, want] = (ready || '').split('/')
  let state = bad?.state || disp.state || (want && want !== '0' && have === want ? 'Ready' : '')
  const cond = (d.model.status?.conditions || []).find((c: any) => c.status === 'False' && c.type !== 'Ready' && c.message)
  let message = bad?.message || ''
  if (disp.error && disp.message) {
    state = 'Error'
    message = disp.message
  } else if (cond) {
    message = message || `${cond.type}: ${cond.message}`
  }
  // No cluster yet: Fleet hasn't matched any, or hasn't made the bundle.
  if (ready === '0/0' || !ready) state = state && state !== 'Ready' ? state : 'NoTargets'
  return { ready, state, message }
}

export function stateTone(state: string): Tone {
  if (!state || state === 'Ready') return 'good'
  if (/^(Err|Error|Failed|Modified)/.test(state)) return 'bad'
  if (state === 'NoTargets') return 'idle'
  return 'busy'
}

// Why a target reaches a CAPI cluster ('' when it doesn't). The host is
// the Fleet cluster fleet-local/local.
export function reaches(t: Target, cluster: Obj | 'host', groups: Obj[], workspace = DEFAULT_TEMPLATE_NAMESPACE): string {
  if (cluster === 'host') return t.kind === 'host' ? 'the host cluster' : ''
  const l = cluster.metadata.labels || {}
  const ns = cluster.metadata.namespace
  switch (t.kind) {
    case 'template':
      return l[LABELS.TEMPLATE] === t.name ? `template ${t.name}` : ''
    case 'class':
      return cluster.spec?.topology?.classRef?.name === t.name ? `class ${t.name}` : ''
    case 'env':
      return ns === t.name ? `environment ${t.name}` : ''
    case 'cluster':
      return ns === t.env && cluster.metadata.name === t.name ? 'this cluster' : ''
    case 'all':
      return 'all clusters'
    case 'group': {
      const g = groups.find((x) => x.metadata.namespace === workspace && x.metadata.name === t.name)
      const ml = g?.spec?.selector?.matchLabels
      if (!ml) return ''
      return Object.entries(ml).every(([k, v]) => l[k] === v) ? `group ${t.name}` : ''
    }
  }
  return ''
}

// The Fleet cluster id ("namespace/name") of a CAPI cluster, or the host.
export function fleetId(cluster: Obj | 'host'): string {
  return cluster === 'host' ? LOCAL_FLEET_CLUSTER : `${cluster.metadata.namespace}/${cluster.metadata.name}`
}

// The Deployments that reach a cluster, and why; or that have a
// BundleDeployment there.
export function forCluster(dl: Delivery, cluster: Obj | 'host'): { d: Deployment; why: string[]; state?: ClusterState }[] {
  const id = fleetId(cluster)
  const out: { d: Deployment; why: string[]; state?: ClusterState }[] = []
  for (const d of dl.deployments) {
    const why = d.targets.map((t) => reaches(t, cluster, dl.groups)).filter(Boolean)
    if (cluster !== 'host' && d.namespace === HOST_WORKSPACE) why.length = 0
    const state = d.states.find((s) => s.cluster === id)
    if (why.length || state) out.push({ d, why: [...new Set(why)], state })
  }
  return out
}

// The Deployments that reach every cluster of a template: aimed at it, its
// class or all clusters.
export function forTemplate(dl: Delivery, tpl: Obj): Deployment[] {
  const name = tpl.metadata.name
  const cls = tpl.spec?.clusterClassRef?.name
  return dl.deployments.filter((d) => d.targets.some((t) => (t.kind === 'template' && t.name === name) || (t.kind === 'class' && t.name === cls) || t.kind === 'all'))
}

// The Secrets a Deployment copies (downstreamResources, on it and on the
// bundles a GitRepo's fleet.yaml files made).
export function secretsOf(d: Deployment): string[] {
  const of = (o: Obj) => ((o.spec?.downstreamResources || []) as any[]).filter((x) => x?.kind === 'Secret' && x.name).map((x) => String(x.name))
  return [...new Set([...of(d.model), ...d.bundles.flatMap(of)])].sort()
}

// The groups hyve keeps, and CAAPF's, and the rest.
export function groupKind(g: Obj): 'template' | 'environment' | 'class' | 'custom' {
  const k = g.metadata.labels?.[GROUP_KIND_LABEL]
  if (k === 'template' || k === 'environment') return k
  return isClassGroup(g) ? 'class' : 'custom'
}

// TYPE/NAME, as the CLI's --to takes it (cluster/ENV/NAME, all, host).
export function parseTarget(s: string): Target | null {
  const p = s.split('/')
  if (p.length === 1 && (s === 'all' || s === 'host')) return { kind: s }
  if (p.length === 3 && p[0] === 'cluster' && p[1] && p[2]) return { kind: 'cluster', env: p[1], name: p[2] }
  if (p.length === 2 && p[1] && ['template', 'class', 'env', 'group'].includes(p[0])) return { kind: p[0] as TargetKind, name: p[1] }
  return null
}

// The --to arguments for a Deployment's targets ('' for a custom one).
export function targetArg(t: Target): string {
  return t.kind === 'custom' ? '' : targetKey(t)
}
