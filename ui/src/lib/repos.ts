// Fleet GitRepos people add (not the ones hyve makes for template
// resources) and the bundles Fleet makes from them. Git owns what's in a
// bundle: the console shows bundles wherever they're useful (Delivery, the
// Overview, a cluster's page) but edits only the GitRepo, on its own page.
import type { Tone } from './format'
import { idOf, type Obj, type Reader } from './kube'
import { FLEET } from './resources'
import { LABELS } from './types'

export const REPO_LABEL = 'fleet.cattle.io/repo-name'

// The Fleet cluster for the management cluster itself.
export const LOCAL_CLUSTER = 'fleet-local/local'

export function isUserRepo(g: Obj): boolean {
  return !g.metadata.labels?.[LABELS.OWNER_KIND]
}

export function bundleState(b: Obj): string {
  return b.status?.display?.state || (b.status?.summary && b.status.summary.ready === b.status.summary.desiredReady ? 'Ready' : 'Pending')
}

// Fleet's message (its Ready condition's), without the wrapper it puts
// around a cluster's: "ErrApplied(1) [Cluster fleet-local/local: …]" or
// "NotReady(1) [Cluster fleet-local/local]; …".
export function bundleMessage(b: Obj): string {
  if (bundleState(b) === 'Ready') return ''
  const m: string = (b.status?.conditions || []).find((c: any) => c.type === 'Ready' && c.status === 'False')?.message || ''
  return m
    .replace(/^\w+\(\d+\) \[Cluster [^:\]]+\]; /, '')
    .replace(/^\w+\(\d+\) \[Cluster [^:\]]+: (.*)\]$/s, '$1')
    .trim()
}

// Fleet's bundle and bundle-deployment states as a pill's tone.
export function fleetTone(state: string): Tone {
  if (!state || state === 'Ready') return 'good'
  if (/^(Err|Error|Failed)/.test(state)) return 'bad'
  if (state === 'OutOfSync' || state === 'Modified' || state === 'NotReady') return 'busy'
  return 'idle'
}

// Fleet names a bundle REPO-PATH-DIR (fleet/arr in nexus-config is
// nexus-config-fleet-arr): the directory under the repo's path is what
// tells a repo's bundles apart.
export function shortBundleName(repo: Obj, bundle: Obj): string {
  const name = repo.metadata.name
  const prefixes = [...(repo.spec?.paths || []).map((p: string) => `${name}-${p.replace(/^\/+|\/+$/g, '').replace(/\//g, '-')}-`), `${name}-`]
  const n = bundle.metadata.name
  const p = prefixes.find((x) => n.startsWith(x) && n.length > x.length)
  return p ? n.slice(p.length) : n
}

// One bundle from a repo, with its state on each Fleet cluster it targets
// ("namespace/name": CAAPF registers a CAPI cluster under its own
// namespace and name; the management cluster is LOCAL_CLUSTER).
export type RepoBundle = { repo: Obj; bundle: Obj; name: string; on: Record<string, string> }

// Every repo bundle. Its states come from BundleDeployments; without the
// right to list those, a bundle's own summary gives what isn't ready.
export async function repoBundles(store: Reader): Promise<RepoBundle[]> {
  const [repos, bundles, bds, fcs] = await Promise.all(
    [FLEET.GIT_REPO, FLEET.BUNDLE, FLEET.BUNDLE_DEPLOYMENT, FLEET.CLUSTER].map((t) => store.findAll(t).catch(() => [] as Obj[])),
  )
  const userRepos = new Map(repos.filter(isUserRepo).map((g) => [idOf(g), g]))
  const fleetCluster: Record<string, string> = {}
  for (const fc of fcs) if (fc.status?.namespace) fleetCluster[fc.status.namespace] = idOf(fc)

  const out = new Map<string, RepoBundle>()
  for (const b of bundles) {
    const repo = userRepos.get(`${b.metadata.namespace}/${b.metadata.labels?.[REPO_LABEL]}`)
    if (!repo) continue
    const on: Record<string, string> = {}
    for (const r of b.status?.summary?.nonReadyResources || []) on[r.name] = r.bundleState || 'NotReady'
    out.set(idOf(b), { repo, bundle: b, name: shortBundleName(repo, b), on })
  }
  for (const bd of bds) {
    const l = bd.metadata.labels || {}
    const rb = out.get(`${l['fleet.cattle.io/bundle-namespace']}/${l['fleet.cattle.io/bundle-name']}`)
    const cluster = fleetCluster[bd.metadata.namespace!]
    if (!rb || !cluster) continue
    rb.on[cluster] = bd.status?.display?.state || (bd.status?.ready ? 'Ready' : 'Pending')
  }
  return [...out.values()].sort((a, b) => idOf(a.repo).localeCompare(idOf(b.repo)) || a.name.localeCompare(b.name))
}
